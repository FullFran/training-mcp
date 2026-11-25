-- Periodization slices 2 and 3 (#41). Additive only: no row changes.
--
-- block_modifiers is what was applied to a session's copy of its routine when
-- it started inside a microcycle with modifiers, as JSON: the block, the
-- microcycle, the deltas and, per exercise, the base value and the result.
-- NULL means nothing was applied. It cannot be derived later: the microcycle's
-- deltas stay editable, the routine can change, the session can be moved by
-- hand, and applying them may have failed.
ALTER TABLE sessions ADD COLUMN block_modifiers TEXT;

-- closed_by is how a microcycle was closed: placement (its last routine was
-- done), hand (complete_microcycle or skip_microcycle) or mode_change (its
-- dates passed when the block went back to sequence). NULL for a microcycle
-- that is pending or was closed before this column existed.
ALTER TABLE block_microcycles ADD COLUMN closed_by TEXT
 CHECK (closed_by IS NULL OR closed_by IN ('placement','hand','mode_change'));
