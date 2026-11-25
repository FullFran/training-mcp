-- Periodization (#41), slice 1. Purely additive: four new per-user tables and
-- three nullable link columns on sessions. Existing rows are untouched and
-- every existing session stays unplaced (NULL), so training with routines and
-- no block behaves exactly as before. No methodology is stored: a microcycle
-- carries a free label and optional modifiers, never a rule.

-- goals: what a person trains toward, in their own words.
CREATE TABLE goals (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id),
	text TEXT NOT NULL CHECK (text <> '' AND length(text) <= 500),
	target_date TEXT,
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','achieved','dropped')),
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX goals_user ON goals(user_id);

-- blocks: a run of microcycles with an intent. advance is how it moves on
-- (sequence: when a microcycle's sessions are done or it is skipped; calendar:
-- by dates from start_date). start_date is set when it is activated, ended_on
-- when it is closed. At most one block per user is active.
CREATE TABLE blocks (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id),
	goal_id INTEGER REFERENCES goals(id),
	intent TEXT NOT NULL CHECK (intent <> '' AND length(intent) <= 500),
	advance TEXT NOT NULL DEFAULT 'sequence' CHECK (advance IN ('sequence','calendar')),
	microcycle_days INTEGER NOT NULL DEFAULT 7 CHECK (microcycle_days BETWEEN 1 AND 28),
	status TEXT NOT NULL DEFAULT 'sketch' CHECK (status IN ('sketch','planned','active','done')),
	start_date TEXT,
	ended_on TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX blocks_user ON blocks(user_id);
CREATE UNIQUE INDEX blocks_one_active ON blocks(user_id) WHERE status = 'active';

-- block_microcycles: one row per microcycle, so a block's length is its row
-- count. sets_delta and effort_delta are optional modifiers kept for the
-- prescription; NULL means none. own_rotation 1 means block_rotations rows with
-- this microcycle_id replace the block's rotation (none: no sessions planned).
CREATE TABLE block_microcycles (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id),
	block_id INTEGER NOT NULL REFERENCES blocks(id),
	position INTEGER NOT NULL,
	label TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 80),
	sets_delta INTEGER,
	effort_delta REAL,
	own_rotation INTEGER NOT NULL DEFAULT 0 CHECK (own_rotation IN (0,1)),
	status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done','skipped')),
	closed_on TEXT,
	UNIQUE (block_id, position)
);
CREATE INDEX block_microcycles_user ON block_microcycles(user_id);

-- block_rotations: the ordered routines of a block (microcycle_id NULL) or of
-- one microcycle. plan_name is a snapshot, so a deleted routine stays readable.
CREATE TABLE block_rotations (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id),
	block_id INTEGER NOT NULL REFERENCES blocks(id),
	microcycle_id INTEGER REFERENCES block_microcycles(id),
	position INTEGER NOT NULL,
	plan_id INTEGER REFERENCES plans(id) ON DELETE SET NULL,
	plan_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX block_rotations_block ON block_rotations(block_id, microcycle_id, position);
CREATE INDEX block_rotations_user ON block_rotations(user_id);
CREATE INDEX block_rotations_plan ON block_rotations(plan_id);

-- Where a session counts. block_off_plan NULL: never placed (no block was
-- active, or the session predates periodization). 0: counts in
-- block_microcycle_id. 1: off-plan, kept and never rejected; block_id is then
-- the block it fell outside of, if any.
ALTER TABLE sessions ADD COLUMN block_id INTEGER REFERENCES blocks(id);
ALTER TABLE sessions ADD COLUMN block_microcycle_id INTEGER REFERENCES block_microcycles(id);
ALTER TABLE sessions ADD COLUMN block_off_plan INTEGER CHECK (block_off_plan IS NULL OR block_off_plan IN (0,1));
CREATE INDEX sessions_block ON sessions(block_id);
CREATE INDEX sessions_block_microcycle ON sessions(block_microcycle_id);
