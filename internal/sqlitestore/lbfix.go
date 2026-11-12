package sqlitestore

import (
	"context"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// LbSets reads the user's sets in a scope the service already validated:
// one session, one exercise between two dates, or sets by id. Oldest first.
func (s *userStore) LbSets(ctx context.Context, scope training.LbScope) ([]training.LbSet, error) {
	return lbSets(ctx, s.db, s.userID, scope)
}

func lbSets(ctx context.Context, q queryer, userID int64, scope training.LbScope) ([]training.LbSet, error) {
	query := `SELECT st.id,st.session_id,se.date,st.exercise,st.reps,st.weight_kg,st.lb_corrected_at IS NOT NULL
		FROM sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=?`
	args := []any{userID}
	switch {
	case scope.SessionID > 0:
		query += ` AND st.session_id=?`
		args = append(args, scope.SessionID)
	case scope.Exercise != "":
		query += ` AND st.exercise=? AND se.date BETWEEN ? AND ?`
		args = append(args, scope.Exercise, scope.From, scope.To)
	case len(scope.SetIDs) > 0:
		query += ` AND st.id IN (?` + strings.Repeat(`,?`, len(scope.SetIDs)-1) + `)`
		for _, id := range scope.SetIDs {
			args = append(args, id)
		}
	default:
		return nil, training.ErrValidation
	}
	rows, err := q.QueryContext(ctx, query+` ORDER BY se.date, st.session_id, st.position, st.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.LbSet
	for rows.Next() {
		var v training.LbSet
		if err := rows.Scan(&v.SetID, &v.SessionID, &v.Date, &v.Exercise, &v.Reps, &v.OldKG, &v.Corrected); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CorrectLbSets writes the corrected weights in one transaction, after checking
// again inside it that every set is the user's, uncorrected and unchanged.
func (s *userStore) CorrectLbSets(ctx context.Context, sets []training.LbSet, at time.Time) error {
	if len(sets) == 0 {
		return training.ErrValidation
	}
	defer s.writing(ctx)()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := make([]int64, len(sets))
	for i, set := range sets {
		ids[i] = set.SetID
	}
	now, err := lbSets(ctx, tx, s.userID, training.LbScope{SetIDs: ids})
	if err != nil {
		return err
	}
	if len(now) != len(sets) {
		return training.ErrNotFound
	}
	byID := map[int64]training.LbSet{}
	for _, v := range now {
		byID[v.SetID] = v
	}
	stamp := at.UTC().Format(time.RFC3339)
	for _, set := range sets {
		cur := byID[set.SetID]
		if cur.Corrected {
			return training.ErrLbCorrected
		}
		if cur.OldKG != set.OldKG {
			return training.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sets SET weight_kg=?,lb_corrected_at=? WHERE id=? AND lb_corrected_at IS NULL`, set.NewKG, stamp, set.SetID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
