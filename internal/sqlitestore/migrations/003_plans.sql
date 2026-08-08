-- A plan is a reusable workout template: an ordered list of exercises with a
-- target set count and a prescribed rep range and RPE. Load is deliberately not
-- planned — that is what you fill in at the gym.
CREATE TABLE IF NOT EXISTS plans (
 id INTEGER PRIMARY KEY,
 name TEXT NOT NULL,
 notes TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS plan_items (
 id INTEGER PRIMARY KEY,
 plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
 position INTEGER NOT NULL,
 exercise TEXT NOT NULL,
 target_sets INTEGER NOT NULL,
 rep_min INTEGER NOT NULL DEFAULT 0,
 rep_max INTEGER NOT NULL DEFAULT 0,
 target_rpe REAL NOT NULL DEFAULT 0,
 UNIQUE(plan_id, position)
);
CREATE INDEX IF NOT EXISTS idx_plan_items_plan ON plan_items(plan_id, position);

-- Sessions may follow a plan. The name is snapshotted so renaming or deleting a
-- plan never rewrites what a past session says it was.
ALTER TABLE sessions ADD COLUMN plan_id INTEGER REFERENCES plans(id) ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN plan_name TEXT NOT NULL DEFAULT '';
