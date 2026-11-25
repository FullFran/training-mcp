package sqlitestore

import (
	"context"
	"database/sql"

	"github.com/fullfran/training-mcp/internal/training"
)

// undoneActivations refuses an undo that would take a block out of active
// while sessions are linked to it or one of its microcycles is closed: a
// sketch or planned block never holds what only an active block produces.
// It runs after the rows are written back, inside the same transaction, so it
// reads the state the undo would leave. Sessions linked by hand before the
// activation also hold it, which keeps the rule simple and the refusal safe.
func (s *userStore) undoneActivations(ctx context.Context, tx *sql.Tx, changes []undoChange) error {
	for _, c := range changes {
		if c.Table != "blocks" || c.Before == nil || c.After == nil {
			continue
		}
		if c.After["status"].v != training.BlockActive || c.Before["status"].v == training.BlockActive {
			continue
		}
		id := c.Key["id"].v
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE block_id=? AND user_id=?`, id, s.userID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return &training.UndoConflict{Reason: training.UndoChanged, Table: "sessions"}
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM block_microcycles WHERE block_id=? AND user_id=? AND status<>'pending'`, id, s.userID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return &training.UndoConflict{Reason: training.UndoChanged, Table: "block_microcycles"}
		}
	}
	return nil
}
