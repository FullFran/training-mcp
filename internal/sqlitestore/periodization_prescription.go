package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/fullfran/training-mcp/internal/training"
)

var _ training.PrescriptionStore = (*userStore)(nil)

// ApplyPrescription writes a prescription into the session's copy of its
// routine and records it with the session, both or neither.
func (s *userStore) ApplyPrescription(ctx context.Context, sessionID int64, p training.Prescription) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	defer s.writing(ctx)()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE sessions SET block_modifiers=? WHERE id=? AND user_id=?`, string(data), sessionID, s.userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return orNotFound(err)
	}
	for _, it := range p.Items {
		if _, err := tx.ExecContext(ctx, `UPDATE session_items SET target_sets=?,target_rpe=? WHERE session_id=? AND exercise=?`,
			it.Sets, it.TargetRPE, sessionID, it.Exercise); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SessionPrescription reads the prescription a session kept.
func (s *userStore) SessionPrescription(ctx context.Context, sessionID int64) (training.Prescription, bool, error) {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT block_modifiers FROM sessions WHERE id=? AND user_id=?`, sessionID, s.userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return training.Prescription{}, false, training.ErrNotFound
	}
	if err != nil || !raw.Valid {
		return training.Prescription{}, false, err
	}
	var p training.Prescription
	if err := json.Unmarshal([]byte(raw.String), &p); err != nil {
		return training.Prescription{}, false, err
	}
	return p, true, nil
}
