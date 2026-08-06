PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS sessions (id INTEGER PRIMARY KEY, date TEXT NOT NULL, started_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sets (
 id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 position INTEGER NOT NULL, exercise TEXT NOT NULL, weight_kg REAL NOT NULL, reps INTEGER NOT NULL,
 rpe REAL NOT NULL, si REAL NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(session_id, position)
);
CREATE INDEX IF NOT EXISTS idx_sessions_date ON sessions(date DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_sets_session_position ON sets(session_id, position);
