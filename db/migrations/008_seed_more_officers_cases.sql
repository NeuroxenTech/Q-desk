-- Q-DESK incremental migration (run AFTER schema.sql / 007)
-- Seeds additional station officers across all ranks and a set of new cases
-- (documents + case assignments) so the multi-rank RBAC and the dual-approval
-- download flow have real data to exercise against.
--
-- Idempotent: safe to re-run. users and case_assignments use ON CONFLICT; the
-- documents inserts are guarded so re-runs never duplicate a FIR row.

-- ============================================================
-- 1. MORE OFFICERS (all ranks)
-- ============================================================

INSERT INTO users (badge_number, full_name, rank) VALUES
    ('IND-IO-403',  'Inspector Meera Nair',          'INSPECTOR'),
    ('IND-IO-404',  'Inspector Arvind Joshi',        'INSPECTOR'),
    ('IND-SI-205',  'Sub Inspector Ravi Menon',      'SUB_INSPECTOR'),
    ('IND-SI-206',  'Sub Inspector Kavita Sharma',   'SUB_INSPECTOR'),
    ('IND-HC-109',  'Head Constable Dinesh Yadav',   'HEAD_CONSTABLE'),
    ('IND-HC-110',  'Head Constable Priya Verma',    'HEAD_CONSTABLE'),
    ('IND-CON-501', 'Constable Sunil Kumar',         'CONSTABLE'),
    ('IND-CON-502', 'Constable Ramesh Patel',        'CONSTABLE'),
    ('IND-SHO-002', 'Addl. Station House Officer',   'SHO_SUPERVISOR'),
    ('IND-SHO-003', 'Circle Inspector Rajan Pillai', 'SHO_SUPERVISOR')
ON CONFLICT (badge_number) DO NOTHING;

-- ============================================================
-- 2. MORE CASES (documents + assignments)
-- ============================================================

-- FIR-2026-0115 — vehicle theft, led by IND-IO-403.
INSERT INTO documents (fir_number, title, classification_level, created_by)
SELECT 'FIR-2026-0115', 'Vehicle theft investigation: FIR-2026-0115', 'RESTRICTED', id
FROM users
WHERE badge_number = 'IND-IO-403'
  AND NOT EXISTS (SELECT 1 FROM documents WHERE fir_number = 'FIR-2026-0115');

-- FIR-2026-0202 — cyber fraud, led by IND-IO-404.
INSERT INTO documents (fir_number, title, classification_level, created_by)
SELECT 'FIR-2026-0202', 'Cyber fraud case: FIR-2026-0202', 'CONFIDENTIAL', id
FROM users
WHERE badge_number = 'IND-IO-404'
  AND NOT EXISTS (SELECT 1 FROM documents WHERE fir_number = 'FIR-2026-0202');

-- FIR-2026-0307 — arms seizure, led by IND-IO-403.
INSERT INTO documents (fir_number, title, classification_level, created_by)
SELECT 'FIR-2026-0307', 'Arms and ammunition seizure: FIR-2026-0307', 'SECRET', id
FROM users
WHERE badge_number = 'IND-IO-403'
  AND NOT EXISTS (SELECT 1 FROM documents WHERE fir_number = 'FIR-2026-0307');

-- FIR-2026-0412 — missing person inquiry, led by IND-SI-205.
INSERT INTO documents (fir_number, title, classification_level, created_by)
SELECT 'FIR-2026-0412', 'Missing person inquiry: FIR-2026-0412', 'RESTRICTED', id
FROM users
WHERE badge_number = 'IND-SI-205'
  AND NOT EXISTS (SELECT 1 FROM documents WHERE fir_number = 'FIR-2026-0412');

-- FIR-2026-0518 — property dispute complaint, led by IND-SI-206.
INSERT INTO documents (fir_number, title, classification_level, created_by)
SELECT 'FIR-2026-0518', 'Property dispute complaint: FIR-2026-0518', 'CONFIDENTIAL', id
FROM users
WHERE badge_number = 'IND-SI-206'
  AND NOT EXISTS (SELECT 1 FROM documents WHERE fir_number = 'FIR-2026-0518');

-- FIR-2026-0625 — custodial duty register, led by IND-HC-110.
INSERT INTO documents (fir_number, title, classification_level, created_by)
SELECT 'FIR-2026-0625', 'Custodial duty register: FIR-2026-0625', 'RESTRICTED', id
FROM users
WHERE badge_number = 'IND-HC-110'
  AND NOT EXISTS (SELECT 1 FROM documents WHERE fir_number = 'FIR-2026-0625');

-- Case assignments: lead investigating officers plus a supporting officer on
-- the larger cases (so multi-officer case access can be exercised).
INSERT INTO case_assignments (fir_number, user_id)
SELECT c.fir, u.id
FROM (VALUES
    ('FIR-2026-0115', 'IND-IO-403'),
    ('FIR-2026-0115', 'IND-SI-205'),
    ('FIR-2026-0202', 'IND-IO-404'),
    ('FIR-2026-0202', 'IND-SI-206'),
    ('FIR-2026-0307', 'IND-IO-403'),
    ('FIR-2026-0307', 'IND-HC-109'),
    ('FIR-2026-0412', 'IND-SI-205'),
    ('FIR-2026-0412', 'IND-CON-501'),
    ('FIR-2026-0518', 'IND-SI-206'),
    ('FIR-2026-0625', 'IND-HC-110'),
    ('FIR-2026-0625', 'IND-CON-502')
) AS c(fir, badge)
JOIN users u ON u.badge_number = c.badge
ON CONFLICT (fir_number, user_id) DO NOTHING;

-- Assign the two extra supervisors to every case so either can review content
-- and backstop the dual-approval inbox for these FIRs.
INSERT INTO case_assignments (fir_number, user_id)
SELECT d.fir_number, u.id
FROM (SELECT fir_number FROM documents WHERE fir_number IN
        ('FIR-2026-0115','FIR-2026-0202','FIR-2026-0307','FIR-2026-0412','FIR-2026-0518','FIR-2026-0625')) d
CROSS JOIN users u
WHERE u.badge_number IN ('IND-SHO-002', 'IND-SHO-003')
ON CONFLICT (fir_number, user_id) DO NOTHING;