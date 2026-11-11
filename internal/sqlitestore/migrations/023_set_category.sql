-- Warm-up sets become a category of their own (issue #86). A warm-up is kept
-- and shown, but it never counts: its SI is zero and every count, volume,
-- progress figure, best set and estimate reads working sets only.
ALTER TABLE sets ADD COLUMN category TEXT NOT NULL DEFAULT 'working'
  CHECK (category IN ('working', 'warmup'));

-- Before this column existed, warm-ups were recorded by writing a warm-up word
-- in the free-text technique. Those sets become warm-ups with the technique
-- cleared, so the word does not linger as a fake intensity technique.
UPDATE sets SET category = 'warmup', technique = '', si = 0
WHERE lower(trim(technique)) IN ('warmup', 'warm-up', 'calentamiento');

-- Weights are kept to 2 decimals from now on. Older writes could store more,
-- such as 65.77089365 from a pound conversion done outside the importer: that
-- precision was never measured, it only leaked into the display and the
-- one-tap pre-fill. Rounding moves a weight by less than 0.005 kg, and the
-- importer already matches existing sets on weights rounded to 2 decimals, so
-- duplicate detection is unchanged.
UPDATE sets SET weight_kg = round(weight_kg, 2) WHERE weight_kg <> round(weight_kg, 2);
