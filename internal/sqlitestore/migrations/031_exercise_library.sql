-- A person's own layer of the exercise library (#137): entries that win over
-- the curated and public layers embedded in the binary, and aliases from the
-- names they use to an entry. Both are per person and keyed by the folded name
-- (training.FoldExerciseName), so case, accents and punctuation do not make a
-- second row. Muscle lists are JSON arrays of training.MuscleGroups. based_on
-- names the shared entry an own entry corrects ("curated:<name>" or
-- "public:<id>"), empty for a new exercise. An alias's target is an entry
-- name, resolved again on every read. Nothing existing changes.
CREATE TABLE exercise_library_entries (
 user_id INTEGER NOT NULL REFERENCES users(id),
 match_key TEXT NOT NULL CHECK (match_key <> ''),
 name TEXT NOT NULL CHECK (name <> '' AND length(name) <= 120),
 primary_muscles TEXT NOT NULL CHECK (json_valid(primary_muscles) AND json_type(primary_muscles) = 'array' AND json_array_length(primary_muscles) > 0),
 secondary_muscles TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(secondary_muscles) AND json_type(secondary_muscles) = 'array'),
 based_on TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL,
 PRIMARY KEY (user_id, match_key)
);
CREATE TABLE exercise_aliases (
 user_id INTEGER NOT NULL REFERENCES users(id),
 match_key TEXT NOT NULL CHECK (match_key <> ''),
 alias TEXT NOT NULL CHECK (alias <> '' AND length(alias) <= 120),
 target TEXT NOT NULL CHECK (target <> '' AND length(target) <= 120),
 updated_at TEXT NOT NULL,
 PRIMARY KEY (user_id, match_key)
);
