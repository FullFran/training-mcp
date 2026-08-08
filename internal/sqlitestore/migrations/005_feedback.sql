-- Post-session feedback per muscle group, the input to the volume-landmark
-- progression the source spreadsheet automated. Only groups actually trained
-- need a row, so recording it stays a handful of taps.
CREATE TABLE IF NOT EXISTS session_feedback (
 id INTEGER PRIMARY KEY,
 session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 muscle_group TEXT NOT NULL,
 fatigue INTEGER NOT NULL,
 pump INTEGER NOT NULL,
 recovery INTEGER NOT NULL,
 created_at TEXT NOT NULL,
 UNIQUE(session_id, muscle_group)
);
CREATE INDEX IF NOT EXISTS idx_session_feedback_group ON session_feedback(muscle_group);
