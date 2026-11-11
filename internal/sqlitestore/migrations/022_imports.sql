-- History imported from another logger (Hevy, Strong). Only added to: nothing
-- here rewrites an existing row.

-- One confirmed import. content_hash is the file's SHA-256, so the history can
-- say a file was already imported; per-set keys below are what stop duplicates.
CREATE TABLE imports (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 source TEXT NOT NULL CHECK (source IN ('hevy','strong')),
 file_name TEXT NOT NULL,
 content_hash TEXT NOT NULL,
 sessions INTEGER NOT NULL,
 sets INTEGER NOT NULL,
 imputed INTEGER NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX idx_imports_user ON imports(user_id, id DESC);

-- Every set an import recorded, by a natural key derived from the source row:
-- the app, the workout's start, the exercise as the app named it and the set's
-- order within that exercise. A key already here is never imported again, even
-- when the set it created was later deleted or the file is exported again with
-- more workouts in it.
CREATE TABLE imported_sets (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 natural_key TEXT NOT NULL,
 import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
 set_id INTEGER REFERENCES sets(id) ON DELETE SET NULL,
 PRIMARY KEY (user_id, natural_key)
);

-- How the person named each of the app's exercises, remembered for the next import.
CREATE TABLE import_exercise_map (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 source TEXT NOT NULL,
 source_name TEXT NOT NULL,
 exercise TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY (user_id, source, source_name)
);

-- The workout's own notes, as the other app had them. Empty for sessions logged here.
ALTER TABLE sessions ADD COLUMN notes TEXT NOT NULL DEFAULT '';
