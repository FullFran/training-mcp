-- Per-exercise notes inside a routine: cues, tempo, "go to failure on the last
-- set". They travel into the session so they are readable while training, and
-- through session_progress so an assistant reading the MCP sees the intent
-- behind the numbers, not just the numbers.
ALTER TABLE plan_items ADD COLUMN notes TEXT NOT NULL DEFAULT '';
ALTER TABLE session_items ADD COLUMN notes TEXT NOT NULL DEFAULT '';
