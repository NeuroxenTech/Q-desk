-- Q-DESK incremental migration (run AFTER schema.sql / 005)
-- Enables branching + merging of document versions.
--
-- A branch is a new document_versions row whose tree_path extends the source
-- version's path (e.g. a branch off '1.2' gets '1.2.1'), chained to the source
-- version's hash via parent_sha256_hash. A merge is a version that brings a
-- branch back into a chosen mainline version: it is chained to the merge
-- target via parent_sha256_hash and records the branch's hash in the new
-- merged_from_hash column so BOTH parents are cryptographically traceable.
--
-- Idempotent: safe to re-run.

ALTER TABLE document_versions
    ADD COLUMN IF NOT EXISTS merged_from_hash TEXT;
