-- Q-DESK incremental migration (run AFTER schema.sql / 004)
-- Records where the uploaded evidence file is stored (Supabase Storage object
-- key) directly on the documents row, kept in sync with the latest upload for
-- a document. Idempotent: safe to re-run.

ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS storage_path TEXT;

-- setDocumentStoragePath runs as the acting officer, so documents needs a
-- FOR UPDATE policy. Scoped the same as the existing officer SELECT policy:
-- only rows whose FIR the officer is assigned to are updatable. This lets the
-- upload flow refresh storage_path on existing-document uploads without
-- granting officers any write access to rows outside their cases.
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