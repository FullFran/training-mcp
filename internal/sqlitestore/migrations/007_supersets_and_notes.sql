-- A superset label groups exercises performed back to back, resting only after
-- the whole round. Exercises sharing a label within a plan or session belong to
-- the same group; empty means a normal standalone exercise.
ALTER TABLE plan_items ADD COLUMN superset TEXT NOT NULL DEFAULT '';
ALTER TABLE session_items ADD COLUMN superset TEXT NOT NULL DEFAULT '';
ALTER TABLE sets ADD COLUMN superset TEXT NOT NULL DEFAULT '';

-- Setup notes that persist per exercise: seat height, grip width, pin position.
-- Written once, shown every time that exercise comes up.
CREATE TABLE IF NOT EXISTS exercise_notes (
 exercise TEXT PRIMARY KEY,
 note TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
