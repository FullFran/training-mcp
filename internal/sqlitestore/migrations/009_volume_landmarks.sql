-- Personal volume landmarks per muscle group. The whole model the source
-- spreadsheet is built on assumes these are individual: the same 15 weekly sets
-- can be under one lifter's MEV and over another's MRV. Treating every group
-- identically, as the first version did, is the part that was wrong.
CREATE TABLE IF NOT EXISTS volume_landmarks (
 muscle_group TEXT PRIMARY KEY,
 mev INTEGER NOT NULL DEFAULT 0,
 mrv INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL
);
