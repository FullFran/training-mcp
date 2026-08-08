-- Starting a session from a plan COPIES the plan's items here. From then on the
-- session owns its prescription: adjusting today never edits the template, and
-- editing the template never rewrites what a past session says it planned.
CREATE TABLE IF NOT EXISTS session_items (
 id INTEGER PRIMARY KEY,
 session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 position INTEGER NOT NULL,
 exercise TEXT NOT NULL,
 target_sets INTEGER NOT NULL,
 rep_min INTEGER NOT NULL DEFAULT 0,
 rep_max INTEGER NOT NULL DEFAULT 0,
 target_rpe REAL NOT NULL DEFAULT 0,
 skipped INTEGER NOT NULL DEFAULT 0,
 UNIQUE(session_id, exercise)
);
CREATE INDEX IF NOT EXISTS idx_session_items_session ON session_items(session_id, position);

-- Backfill: sessions already started from a plan get a snapshot of it, so
-- existing planned sessions keep working after this change.
INSERT INTO session_items (session_id, position, exercise, target_sets, rep_min, rep_max, target_rpe)
SELECT s.id, i.position, i.exercise, i.target_sets, i.rep_min, i.rep_max, i.target_rpe
FROM sessions s JOIN plan_items i ON i.plan_id = s.plan_id
WHERE s.plan_id IS NOT NULL;
