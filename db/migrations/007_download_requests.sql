-- Q-DESK incremental migration (run AFTER schema.sql / 006)
-- Chain-of-custody download requests with dual-approval release.
--
-- A download_requests row is created by an officer for a document and must be
-- approved by `required_approvals` (default 2) distinct SHO_SUPERVISOR+
-- officials (never the requester) before the officer can execute a download
-- of the evidence + a signed chain-of-custody certificate. Approvals are
-- recorded as signed download_approvals rows (ML-DSA-65 over
-- request_id|decision|decided_at) so a decision is non-repudiable.
--
-- Idempotent: safe to re-run against a database that already has the objects.

-- ============================================================
-- ENUMS
-- ============================================================

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'download_request_status') THEN
        ALTER TYPE download_request_status ADD VALUE IF NOT EXISTS 'pending';
        ALTER TYPE download_request_status ADD VALUE IF NOT EXISTS 'approved';
        ALTER TYPE download_request_status ADD VALUE IF NOT EXISTS 'rejected';
        ALTER TYPE download_request_status ADD VALUE IF NOT EXISTS 'expired';
        ALTER TYPE download_request_status ADD VALUE IF NOT EXISTS 'downloaded';
    ELSE
        CREATE TYPE download_request_status AS ENUM (
            'pending', 'approved', 'rejected', 'expired', 'downloaded'
        );
    END IF;
END
$$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'download_decision') THEN
        ALTER TYPE download_decision ADD VALUE IF NOT EXISTS 'approved';
        ALTER TYPE download_decision ADD VALUE IF NOT EXISTS 'rejected';
    ELSE
        CREATE TYPE download_decision AS ENUM ('approved', 'rejected');
    END IF;
END
$$;

-- ============================================================
-- DOWNLOAD REQUESTS
-- ============================================================

CREATE TABLE IF NOT EXISTS download_requests (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id         UUID NOT NULL REFERENCES documents(id),
    reason              TEXT NOT NULL,
    requested_by_badge  TEXT NOT NULL REFERENCES users(badge_number),
    status              download_request_status NOT NULL DEFAULT 'pending',
    required_approvals  INT  NOT NULL DEFAULT 2 CHECK (required_approvals >= 1),
    requested_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    downloaded_at       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_download_requests_status ON download_requests(status);
CREATE INDEX IF NOT EXISTS idx_download_requests_requester ON download_requests(requested_by_badge);
CREATE INDEX IF NOT EXISTS idx_download_requests_document ON download_requests(document_id);

-- ============================================================
-- DOWNLOAD APPROVALS
-- ============================================================

CREATE TABLE IF NOT EXISTS download_approvals (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    download_request_id   UUID NOT NULL REFERENCES download_requests(id) ON DELETE CASCADE,
    approved_by_badge     TEXT NOT NULL REFERENCES users(badge_number),
    decision              download_decision NOT NULL,
    decided_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    signature             TEXT NOT NULL,

    UNIQUE (download_request_id, approved_by_badge)
);

CREATE INDEX IF NOT EXISTS idx_download_approvals_request ON download_approvals(download_request_id);
CREATE INDEX IF NOT EXISTS idx_download_approvals_badge ON download_approvals(approved_by_badge);

-- ============================================================
-- ROW-LEVEL SECURITY
-- ============================================================

ALTER TABLE download_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE download_approvals ENABLE ROW LEVEL SECURITY;

-- A request row is visible to its own requester and to every SHO_SUPERVISOR+
-- official (who may be asked to decide on it). Investigating officers only
-- ever see their own requests.
DROP POLICY IF EXISTS dl_req_self_select ON download_requests;
CREATE POLICY dl_req_self_select ON download_requests
    FOR SELECT
    USING (
        requested_by_badge = (
            SELECT badge_number FROM users WHERE id::text = current_setting('app.current_user_id')
        )
        OR current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR')
    );

-- INSERT is only permitted for the officer themselves, and only against a
-- document the mirror-same RLS read policies would hand them (a case
-- assignment for investigating officers, all station documents for
-- SHO_SUPERVISOR). SYSTEM_ADMIN is intentionally excluded — mirroring the
-- existing "no content access" rule for platform admins.
DROP POLICY IF EXISTS dl_req_officer_insert ON download_requests;
CREATE POLICY dl_req_officer_insert ON download_requests
    FOR INSERT
    WITH CHECK (
        requested_by_badge = (
            SELECT badge_number FROM users WHERE id::text = current_setting('app.current_user_id')
        )
        AND EXISTS (
            SELECT 1 FROM documents d
            WHERE d.id = document_id
              AND (
                  EXISTS (
                      SELECT 1 FROM case_assignments ca
                      WHERE ca.fir_number = d.fir_number
                        AND ca.user_id::text = current_setting('app.current_user_id')
                  )
                  OR current_setting('app.current_user_role') = 'SHO_SUPERVISOR'
              )
        )
    );

-- Status transitions (approved / rejected / expired / downloaded) are
-- performed by the requester (execute) or by SHO_SUPERVISOR+ (decide /
-- expiry). The application enforces the exact transition rules; this policy
-- only bounds WHO may update a row.
DROP POLICY IF EXISTS dl_req_update ON download_requests;
CREATE POLICY dl_req_update ON download_requests
    FOR UPDATE
    USING (
        requested_by_badge = (
            SELECT badge_number FROM users WHERE id::text = current_setting('app.current_user_id')
        )
        OR current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR')
    );

-- Only SHO_SUPERVISOR+ may record a signed decision.
DROP POLICY IF EXISTS dl_appr_insert ON download_approvals;
CREATE POLICY dl_appr_insert ON download_approvals
    FOR INSERT
    WITH CHECK (
        current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR')
    );

-- An approval row is visible to the official who made it, to every
-- SHO_SUPERVISOR+ official (decision-making context), and to the ORIGINAL
-- requester of the parent request (so they can see "2 of 2 approvals" and,
-- after a rejection, who rejected it and when).
DROP POLICY IF EXISTS dl_appr_select ON download_approvals;
CREATE POLICY dl_appr_select ON download_approvals
    FOR SELECT
    USING (
        approved_by_badge = (
            SELECT badge_number FROM users WHERE id::text = current_setting('app.current_user_id')
        )
        OR current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR')
        OR EXISTS (
            SELECT 1 FROM download_requests dr
            WHERE dr.id = download_request_id
              AND dr.requested_by_badge = (
                  SELECT badge_number FROM users WHERE id::text = current_setting('app.current_user_id')
              )
        )
    );