-- Q-DESK incremental migration — PART 2 (run AFTER 002_sho_supervisor_enum.sql)
-- Supervisors + SYSTEM_ADMIN see all station document versions for the
-- cross-case audit view (/api/versions). Content stays withheld for
-- SYSTEM_ADMIN at the application layer.

-- 1. Drop the old SYSTEM_ADMIN content guard. It filtered ALL rows for
--    SYSTEM_ADMIN (RLS cannot redact a single column), which would have made
--    the admin see zero versions.
DROP POLICY IF EXISTS admin_no_content_payload ON document_versions;

-- 2. Supervisors and SYSTEM_ADMIN may SELECT all station document_versions
--    metadata + hashes for the cross-case audit view.
CREATE POLICY supervisor_fir_versions ON document_versions
    FOR SELECT
    USING (current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR'));

-- 3. Supervisors and SYSTEM_ADMIN may SELECT all station documents (so the
--    versions query's documents join is not RLS-filtered for them).
CREATE POLICY supervisor_fir_documents ON documents
    FOR SELECT
    USING (current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR'));

-- 4. Seed a station-house supervisor for testing the all-cases scope.
INSERT INTO users (badge_number, full_name, rank) VALUES
    ('IND-SHO-999', 'Station House Officer', 'SHO_SUPERVISOR')
ON CONFLICT (badge_number) DO NOTHING;