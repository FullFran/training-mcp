-- Maps an exercise name to the muscle group it trains. Keyed by the exercise
-- name because sets carry the name, not an id; a rename is a catalogue edit.
CREATE TABLE IF NOT EXISTS exercise_groups (
 exercise TEXT PRIMARY KEY,
 muscle_group TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_exercise_groups_muscle ON exercise_groups(muscle_group);
