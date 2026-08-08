-- Intensity techniques applied to a set: drop set, rest-pause, myo-reps, sets
-- taken without pausing, and so on. Kept as a property OF the set rather than a
-- separate exercise, so the volume still counts toward the right muscle group
-- and progression on the base movement stays comparable.
ALTER TABLE sets ADD COLUMN technique TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_sets_technique ON sets(technique) WHERE technique <> '';
