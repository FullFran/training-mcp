-- Marks a set whose RPE was not stated. It still counts one SI unit, so totals
-- stay comparable, but the stored RPE is a placeholder and must never be read as
-- a measurement. Existing sets default to measured; correcting any that were
-- imputed is a separate, explicit data change.
ALTER TABLE sets ADD COLUMN rpe_imputed INTEGER NOT NULL DEFAULT 0;
