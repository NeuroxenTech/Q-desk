-- Q-DESK incremental migration (run AFTER schema.sql / 003)
-- Adds a classification level to documents for the evidence-file upload flow
-- (POST /api/upload creates a documents row with fir_number, title and
-- classification_level when is_new_document=true).

ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS classification_level TEXT NOT NULL DEFAULT 'RESTRICTED';

-- Guard the classification against typos/free-form input. Values follow the
-- standard law-enforcement four-level scale.
ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS documents_classification_check;

ALTER TABLE documents
    ADD CONSTRAINT documents_classification_check
    CHECK (classification_level IN ('RESTRICTED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET'));