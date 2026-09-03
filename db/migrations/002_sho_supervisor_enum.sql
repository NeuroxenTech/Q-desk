-- Q-DESK incremental migration — PART 1 (run this ALONE, first)
-- Adds SHO_SUPERVISOR to the lea_role enum.
--
-- IMPORTANT: PostgreSQL forbids *using* a newly added enum value in the same
-- transaction that adds it (55P04). Run ONLY this statement, let it commit,
-- then run 003_sho_supervisor_rbac.sql.

ALTER TYPE lea_role ADD VALUE IF NOT EXISTS 'SHO_SUPERVISOR';