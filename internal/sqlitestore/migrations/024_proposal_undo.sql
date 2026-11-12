-- Undoing an accepted proposal. undo_image holds every row the proposal
-- changed, as it was before and right after (see sqlitestore/undo.go); '' means
-- it cannot be undone (answered before this migration, rejected, failed, or an
-- undo itself). undone_by is the history entry that undid it, 0 while it stands.
ALTER TABLE proposals ADD COLUMN undo_image TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN undone_by INTEGER NOT NULL DEFAULT 0;
