-- Weight units. Every weight stays stored in kg; weight_unit is how the person
-- types and reads them by default: 'kg' or 'lb'. NULL means never chosen, and
-- kg applies, so existing users see no change.
ALTER TABLE users ADD COLUMN weight_unit TEXT;
-- lb_corrected_at marks a set whose stored weight was corrected from lb to kg
-- (a past set typed in lb but stored as kg), so the correction is never applied
-- twice. NULL for every set until then.
ALTER TABLE sets ADD COLUMN lb_corrected_at TEXT;
