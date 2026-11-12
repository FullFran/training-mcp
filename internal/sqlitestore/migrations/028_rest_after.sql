-- rest_after_seconds is how long the person rested after a set, stored only
-- when they stopped the rest timer. NULL for every set until then, and never
-- backfilled: a rest is never inferred from the gap between two sets.
ALTER TABLE sets ADD COLUMN rest_after_seconds INTEGER CHECK (rest_after_seconds IS NULL OR rest_after_seconds >= 0);
