-- Every person's data gets an owner. Identity is an internal id rather than the
-- provider's subject, so changing identity provider or instance only rewrites
-- users.clerk_subject. User 1 is the original owner: every existing row is theirs,
-- and their subject stays empty until a later change binds it.
CREATE TABLE users (
 id INTEGER PRIMARY KEY,
 clerk_subject TEXT UNIQUE,
 created_at TEXT NOT NULL
);
INSERT INTO users(id,created_at) VALUES (1,CURRENT_TIMESTAMP);

-- Only added to, never rebuilt: with foreign keys on, dropping these parents would
-- cascade into their sets, items and feedback. Those children are owned through
-- their parent. The column cannot reference users, as SQLite refuses a referencing
-- column with a non-null default, and the default exists only to fill these rows.
ALTER TABLE sessions ADD COLUMN user_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE plans ADD COLUMN user_id INTEGER NOT NULL DEFAULT 1;
CREATE INDEX idx_sessions_user_date ON sessions(user_id, date DESC, id DESC);
CREATE INDEX idx_plans_user ON plans(user_id);

-- These four are keyed by what one person would name them, so each key becomes per
-- user. Nothing references them, which makes rebuilding safe.
CREATE TABLE exercise_notes_new (
 user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 exercise TEXT NOT NULL,
 note TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY (user_id, exercise)
);
INSERT INTO exercise_notes_new(user_id,exercise,note,updated_at)
 SELECT 1,exercise,note,updated_at FROM exercise_notes;
DROP TABLE exercise_notes;
ALTER TABLE exercise_notes_new RENAME TO exercise_notes;

CREATE TABLE exercise_groups_new (
 user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 exercise TEXT NOT NULL,
 muscle_group TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY (user_id, exercise)
);
INSERT INTO exercise_groups_new(user_id,exercise,muscle_group,updated_at)
 SELECT 1,exercise,muscle_group,updated_at FROM exercise_groups;
DROP TABLE exercise_groups;
ALTER TABLE exercise_groups_new RENAME TO exercise_groups;
CREATE INDEX idx_exercise_groups_muscle ON exercise_groups(user_id, muscle_group);

CREATE TABLE volume_landmarks_new (
 user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 muscle_group TEXT NOT NULL,
 mev INTEGER NOT NULL DEFAULT 0,
 mrv INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL,
 PRIMARY KEY (user_id, muscle_group)
);
INSERT INTO volume_landmarks_new(user_id,muscle_group,mev,mrv,updated_at)
 SELECT 1,muscle_group,mev,mrv,updated_at FROM volume_landmarks;
DROP TABLE volume_landmarks;
ALTER TABLE volume_landmarks_new RENAME TO volume_landmarks;

-- A key shared between users would return one user's stored response to another.
CREATE TABLE idempotency_keys_new (
 user_id INTEGER NOT NULL DEFAULT 1 REFERENCES users(id),
 key TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 response TEXT NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY (user_id, key)
);
INSERT INTO idempotency_keys_new(user_id,key,fingerprint,response,created_at)
 SELECT 1,key,fingerprint,response,created_at FROM idempotency_keys;
DROP TABLE idempotency_keys;
ALTER TABLE idempotency_keys_new RENAME TO idempotency_keys;
