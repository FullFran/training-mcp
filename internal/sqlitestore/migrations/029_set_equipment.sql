-- equipment is the machine or equipment a set was done on, as free text
-- normalized like exercise names (training.normalizeEquipment). NULL means not
-- stated. Past sets stay NULL: it is never inferred or backfilled. The
-- performance engine compares sets only when their equipment matches.
ALTER TABLE sets ADD COLUMN equipment TEXT CHECK (equipment IS NULL OR (equipment <> '' AND length(equipment) <= 80));
