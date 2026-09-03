-- Q-DESK incremental migration (run AFTER schema.sql)
-- Adds SHO_SUPERVISOR role + supervisor/system-admin RLS for the cross-case
-- document_versions audit view (/api/versions).

-- 1. Add SHO_SUPERVISOR to the lea_role enum.
ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'SHO_SUPERVISOR';

-- 2. Drop the old SYSTEM_ADMIN content guard. It filtered ALL rows for
--    SYSTEM_ADMIN (RLS cannot redact a single column), which would have made
--    the admin see zero versions. Content is withheld at the application layer
--    instead (see GET /api/versions in backend/handlers_versions.go).
DROP POLICY IF EXISTS admin_no_content_payload ON document_versions;

-- 3. Supervisors and SYSTEM_ADMIN may SELECT all station document_versions
--    metadata + hashes for the cross-case audit view.
CREATE POLICY supervisor_fir_versions ON document_versions
    FOR SELECT
    USING (current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR'));

-- 4. Supervisors and SYSTEM_ADMIN may SELECT all station documents (needed so
--    the versions query's documents join is not RLS-filtered for them).
CREATE POLICY supervisor_fir_documents ON documents
    FOR SELECT
    USING (current_setting('app.current_user_role') IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR'));

-- 5. Seed a station-house supervisor for testing the all-cases scope.
INSERT INTO users (badge_number, full_name, rank) VALUES
    ('IND-SHO-999', 'Station House Officer', 'SHO_SUPERVISOR')
ON CONFLICT (badge_number) DO NOTHING;