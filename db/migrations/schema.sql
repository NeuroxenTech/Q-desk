-- Q-DESK Evidence Management System Schema
-- PostgreSQL with ltree extension for hierarchical document paths
-- This file is idempotent: it can be re-run safely against a database that
-- already has the objects. Incremental changes live in db/migrations/.

CREATE EXTENSION IF NOT EXISTS ltree;

-- ============================================================
-- ENUMS
-- ============================================================

-- CREATE TYPE has no IF NOT EXISTS; guard it with a DO block so re-runs skip
-- the definition instead of erroring with 42710 "type already exists". New
-- values are added to an existing enum idempotently.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'lea_role') THEN
        ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'SYSTEM_ADMIN';
        ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'SHO_SUPERVISOR';
        ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'INSPECTOR';
        ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'SUB_INSPECTOR';
        ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'HEAD_CONSTABLE';
        ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'CONSTABLE';
    ELSE
        CREATE TYPE lea_role AS ENUM (
            'SYSTEM_ADMIN',
            'SHO_SUPERVISOR',
            'INSPECTOR',
            'SUB_INSPECTOR',
            'HEAD_CONSTABLE',
            'CONSTABLE'
        );
    END IF;
END
$$;

-- ============================================================
-- USERS
-- ============================================================

CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    badge_number    TEXT UNIQUE NOT NULL,
    full_name       TEXT NOT NULL,
    rank            lea_role NOT NULL,
    phone           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- DOCUMENTS
-- ============================================================

CREATE TABLE IF NOT EXISTS documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fir_number      TEXT NOT NULL,
    title           TEXT NOT NULL,
    classification_level TEXT NOT NULL DEFAULT 'RESTRICTED'
                    CHECK (classification_level IN ('RESTRICTED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET')),
    storage_path    TEXT,
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_documents_fir_number ON documents(fir_number);

-- ============================================================
-- DOCUMENT VERSIONS
-- ============================================================

CREATE TABLE IF NOT EXISTS document_versions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id         UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    version_number      INTEGER NOT NULL,
    tree_path           ltree NOT NULL,
    sha256_hash         TEXT NOT NULL,
    parent_sha256_hash  TEXT,
    signature           TEXT NOT NULL,
    content_payload     BYTEA,
    created_by          UUID NOT NULL REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (document_id, version_number)
);

CREATE INDEX IF NOT EXISTS idx_doc_versions_tree_path ON document_versions USING gist(tree_path);
CREATE INDEX IF NOT EXISTS idx_doc_versions_sha256 ON document_versions(sha256_hash);

-- ============================================================
-- CASE ASSIGNMENTS
-- ============================================================

CREATE TABLE IF NOT EXISTS case_assignments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fir_number  TEXT NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id),
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (fir_number, user_id)
);

CREATE INDEX IF NOT EXISTS idx_case_assignments_fir ON case_assignments(fir_number);
CREATE INDEX IF NOT EXISTS idx_case_assignments_user ON case_assignments(user_id);

-- ============================================================
-- TICKETS ISSUED
-- ============================================================

CREATE TABLE IF NOT EXISTS tickets_issued (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_code     TEXT UNIQUE NOT NULL,
    fir_number      TEXT NOT NULL,
    issued_to       UUID NOT NULL REFERENCES users(id),
    issued_by       UUID NOT NULL REFERENCES users(id),
    expires_at      TIMESTAMPTZ NOT NULL,
    consumed        BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tickets_fir ON tickets_issued(fir_number);
CREATE INDEX IF NOT EXISTS idx_tickets_issued_to ON tickets_issued(issued_to);

-- ============================================================
-- AUDIT LOGS (hash-chained)
-- ============================================================

CREATE TABLE IF NOT EXISTS audit_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action          TEXT NOT NULL,
    actor_id        UUID REFERENCES users(id),
    fir_number      TEXT,
    prev_log_hash   TEXT,
    log_hash        TEXT NOT NULL,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_fir ON audit_logs(fir_number);
CREATE INDEX IF NOT EXISTS idx_audit_logs_log_hash ON audit_logs(log_hash);

-- ============================================================
-- ROW-LEVEL SECURITY
-- ============================================================

ALTER TABLE documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE document_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE tickets_issued ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;

-- Supervisors (SHO_SUPERVISOR) and SYSTEM_ADMIN may SELECT all station
-- document_versions metadata + hashes for the cross-case audit view. Content
-- payload is withheld for SYSTEM_ADMIN at the application layer (see
-- /api/versions); RLS cannot redact a single column, so the backend simply
-- never selects content_payload when acting as SYSTEM_ADMIN.
DROP POLICY IF EXISTS supervisor_fir_versions ON document_versions;
CREATE POLICY supervisor_fir_versions ON document_versions
    FOR SELECT
    USING (current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR'));

-- Supervisors and SYSTEM_ADMIN may SELECT all station documents.
DROP POLICY IF EXISTS supervisor_fir_documents ON documents;
CREATE POLICY supervisor_fir_documents ON documents
    FOR SELECT
    USING (current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR'));

-- Officers can only SELECT documents they have a case_assignment for
DROP POLICY IF EXISTS officer_fir_scoped_access ON documents;
CREATE POLICY officer_fir_scoped_access ON documents
    FOR SELECT
    USING (
        EXISTS (
            SELECT 1
            FROM case_assignments ca
            WHERE ca.fir_number = documents.fir_number
              AND ca.user_id::text = current_setting('app.current_user_id')
        )
    );

-- Officers can only SELECT document_versions for documents they have access to
DROP POLICY IF EXISTS officer_version_fir_scoped ON document_versions;
CREATE POLICY officer_version_fir_scoped ON document_versions
    FOR SELECT
    USING (
        EXISTS (
            SELECT 1
            FROM documents d
            JOIN case_assignments ca ON ca.fir_number = d.fir_number
            WHERE d.id = document_versions.document_id
              AND ca.user_id::text = current_setting('app.current_user_id')
        )
    );

-- Officers can only see their own case assignments
DROP POLICY IF EXISTS officer_own_assignments ON case_assignments;
CREATE POLICY officer_own_assignments ON case_assignments
    FOR SELECT
    USING (
        user_id::text = current_setting('app.current_user_id')
        OR current_setting('app.current_user_role') = 'SYSTEM_ADMIN'
    );

-- Officers may update (storage_path refresh) only documents in their cases
DROP POLICY IF EXISTS officer_fir_document_update ON documents;
CREATE POLICY officer_fir_document_update ON documents
    FOR UPDATE
    USING (
        EXISTS (
            SELECT 1
            FROM case_assignments ca
            WHERE ca.fir_number = documents.fir_number
              AND ca.user_id::text = current_setting('app.current_user_id')
        )
    );

-- Officers can only see tickets issued to them or by them
DROP POLICY IF EXISTS officer_own_tickets ON tickets_issued;
CREATE POLICY officer_own_tickets ON tickets_issued
    FOR SELECT
    USING (
        issued_to::text = current_setting('app.current_user_id')
        OR issued_by::text = current_setting('app.current_user_id')
        OR current_setting('app.current_user_role') = 'SYSTEM_ADMIN'
    );

-- ============================================================
-- SEED DATA
-- ============================================================

-- Officers
INSERT INTO users (badge_number, full_name, rank) VALUES
    ('IND-SHO-999', 'Station House Officer',        'SHO_SUPERVISOR'),
    ('IND-IO-402',  'Inspector Rajesh Kumar',      'INSPECTOR'),
    ('IND-FOR-108', 'Head Constable Vikram Singh',  'HEAD_CONSTABLE'),
    ('IND-SHO-001', 'Sub Inspector Anita Desai',    'SUB_INSPECTOR'),
    ('IND-ADM-999', 'System Administrator',         'SYSTEM_ADMIN')
ON CONFLICT (badge_number) DO NOTHING;

-- FIR-2026-0089 assigned to IND-IO-402
INSERT INTO case_assignments (fir_number, user_id)
SELECT 'FIR-2026-0089', id FROM users WHERE badge_number = 'IND-IO-402'
ON CONFLICT (fir_number, user_id) DO NOTHING;