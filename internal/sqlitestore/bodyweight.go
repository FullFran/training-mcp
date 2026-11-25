package sqlitestore

import (
	"context"
	"database/sql"

	"github.com/fullfran/training-mcp/internal/training"
)

// PutBodyweight stores the entry of its date, replacing the one already there
// and keeping when that day was first recorded.
func (s *userStore) PutBodyweight(ctx context.Context, b training.Bodyweight) error {
	defer s.writing(ctx)()
	var note any
	if b.Note != "" {
		note = b.Note
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO bodyweight(user_id,date,weight_kg,note,created_at,updated_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,date) DO UPDATE SET weight_kg=excluded.weight_kg, note=excluded.note, updated_at=CURRENT_TIMESTAMP`,
		s.userID, b.Date, b.WeightKG, note)
	return err
}

// Bodyweights are the user's entries from..to, both included, oldest first.
func (s *userStore) Bodyweights(ctx context.Context, from, to string) ([]training.Bodyweight, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT date,weight_kg,note FROM bodyweight WHERE user_id=? AND date BETWEEN ? AND ? ORDER BY date`, s.userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.Bodyweight
	for rows.Next() {
		var b training.Bodyweight
		var note sql.NullString
		if err := rows.Scan(&b.Date, &b.WeightKG, &note); err != nil {
			return nil, err
		}
		b.Note = note.String
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBodyweight removes the user's entry of a date; ErrNotFound when the
// user has none that day.
func (s *userStore) DeleteBodyweight(ctx context.Context, date string) error {
	defer s.writing(ctx)()
	res, err := s.db.ExecContext(ctx, `DELETE FROM bodyweight WHERE user_id=? AND date=?`, s.userID, date)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return training.ErrNotFound
	}
	return nil
}

// SetSessionNotes replaces the notes of one of the user's sessions.
func (s *userStore) SetSessionNotes(ctx context.Context, sessionID int64, notes string) error {
	defer s.writing(ctx)()
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET notes=? WHERE id=? AND user_id=?`, notes, sessionID, s.userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return training.ErrNotFound
	}
	return nil
}
