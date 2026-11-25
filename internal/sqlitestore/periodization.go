package sqlitestore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/fullfran/training-mcp/internal/training"
)

// The periodization port (#41). Every statement names the user, so another
// account's goal, block, microcycle or session behaves as a missing one.

var _ training.PeriodizationStore = (*userStore)(nil)

func nullText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullID(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (s *userStore) SaveGoal(ctx context.Context, g training.Goal) (training.Goal, error) {
	defer s.writing(ctx)()
	if g.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO goals(user_id,text,target_date,status) VALUES (?,?,?,?)`,
			s.userID, g.Text, nullText(g.TargetDate), g.Status)
		if err != nil {
			return training.Goal{}, err
		}
		if g.ID, err = res.LastInsertId(); err != nil {
			return training.Goal{}, err
		}
		return g, nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE goals SET text=?,target_date=?,status=? WHERE id=? AND user_id=?`,
		g.Text, nullText(g.TargetDate), g.Status, g.ID, s.userID)
	if err != nil {
		return training.Goal{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return training.Goal{}, orNotFound(err)
	}
	return g, nil
}

func orNotFound(err error) error {
	if err != nil {
		return err
	}
	return training.ErrNotFound
}

const goalCols = `id,text,COALESCE(target_date,''),status`

func scanGoal(row interface{ Scan(...any) error }) (training.Goal, error) {
	var g training.Goal
	err := row.Scan(&g.ID, &g.Text, &g.TargetDate, &g.Status)
	return g, err
}

func (s *userStore) GetGoal(ctx context.Context, id int64) (training.Goal, error) {
	g, err := scanGoal(s.db.QueryRowContext(ctx, `SELECT `+goalCols+` FROM goals WHERE id=? AND user_id=?`, id, s.userID))
	if errors.Is(err, sql.ErrNoRows) {
		return training.Goal{}, training.ErrNotFound
	}
	return g, err
}

func (s *userStore) Goals(ctx context.Context) ([]training.Goal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+goalCols+` FROM goals WHERE user_id=? ORDER BY id`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *userStore) GetBlock(ctx context.Context, id int64) (training.Block, error) {
	return s.getBlock(ctx, s.db, id)
}

func (s *userStore) getBlock(ctx context.Context, q queryRower, id int64) (training.Block, error) {
	var b training.Block
	err := q.QueryRowContext(ctx, `SELECT id,COALESCE(goal_id,0),intent,advance,microcycle_days,status,COALESCE(start_date,''),COALESCE(ended_on,'')
FROM blocks WHERE id=? AND user_id=?`, id, s.userID).Scan(&b.ID, &b.GoalID, &b.Intent, &b.Advance, &b.MicrocycleDays, &b.Status, &b.StartDate, &b.EndedOn)
	if errors.Is(err, sql.ErrNoRows) {
		return training.Block{}, training.ErrNotFound
	}
	if err != nil {
		return training.Block{}, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id,position,label,COALESCE(sets_delta,0),COALESCE(effort_delta,0),own_rotation,status,COALESCE(closed_on,'')
FROM block_microcycles WHERE block_id=? AND user_id=? ORDER BY position`, b.ID, s.userID)
	if err != nil {
		return training.Block{}, err
	}
	index := map[int64]int{}
	for rows.Next() {
		var m training.Microcycle
		if err = rows.Scan(&m.ID, &m.Position, &m.Label, &m.SetsDelta, &m.EffortDelta, &m.OwnRotation, &m.Status, &m.ClosedOn); err != nil {
			rows.Close()
			return training.Block{}, err
		}
		index[m.ID] = len(b.Microcycles)
		b.Microcycles = append(b.Microcycles, m)
	}
	if err = closeRows(rows); err != nil {
		return training.Block{}, err
	}
	rows, err = q.QueryContext(ctx, `SELECT COALESCE(microcycle_id,0),COALESCE(plan_id,0),plan_name
FROM block_rotations WHERE block_id=? AND user_id=? ORDER BY microcycle_id IS NOT NULL, microcycle_id, position`, b.ID, s.userID)
	if err != nil {
		return training.Block{}, err
	}
	for rows.Next() {
		var mid int64
		var r training.RotationEntry
		if err = rows.Scan(&mid, &r.PlanID, &r.PlanName); err != nil {
			rows.Close()
			return training.Block{}, err
		}
		if mid == 0 {
			b.Rotation = append(b.Rotation, r)
		} else if i, ok := index[mid]; ok {
			b.Microcycles[i].Rotation = append(b.Microcycles[i].Rotation, r)
		}
	}
	return b, closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *userStore) blockIDs(ctx context.Context, query string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, query, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *userStore) Blocks(ctx context.Context) ([]training.Block, error) {
	ids, err := s.blockIDs(ctx, `SELECT id FROM blocks WHERE user_id=? ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]training.Block, 0, len(ids))
	for _, id := range ids {
		b, err := s.GetBlock(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *userStore) ActiveBlock(ctx context.Context) (training.Block, bool, error) {
	ids, err := s.blockIDs(ctx, `SELECT id FROM blocks WHERE user_id=? AND status='active'`)
	if err != nil || len(ids) == 0 {
		return training.Block{}, false, err
	}
	b, err := s.GetBlock(ctx, ids[0])
	return b, err == nil, err
}

func (s *userStore) BlockOfMicrocycle(ctx context.Context, id int64) (int64, error) {
	var block int64
	err := s.db.QueryRowContext(ctx, `SELECT block_id FROM block_microcycles WHERE id=? AND user_id=?`, id, s.userID).Scan(&block)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, training.ErrNotFound
	}
	return block, err
}

const linkCols = `id,date,COALESCE(plan_id,0),plan_name,COALESCE(block_id,0),COALESCE(block_microcycle_id,0),block_off_plan`

func scanLink(row interface{ Scan(...any) error }) (training.SessionLink, error) {
	var l training.SessionLink
	var offPlan sql.NullInt64
	if err := row.Scan(&l.SessionID, &l.Date, &l.PlanID, &l.PlanName, &l.BlockID, &l.MicrocycleID, &offPlan); err != nil {
		return training.SessionLink{}, err
	}
	l.Placed, l.OffPlan = offPlan.Valid, offPlan.Valid && offPlan.Int64 == 1
	return l, nil
}

func (s *userStore) SessionLink(ctx context.Context, id int64) (training.SessionLink, error) {
	l, err := scanLink(s.db.QueryRowContext(ctx, `SELECT `+linkCols+` FROM sessions WHERE id=? AND user_id=?`, id, s.userID))
	if errors.Is(err, sql.ErrNoRows) {
		return training.SessionLink{}, training.ErrNotFound
	}
	return l, err
}

func (s *userStore) BlockSessions(ctx context.Context, block int64) ([]training.SessionLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+linkCols+` FROM sessions WHERE user_id=? AND block_id=? AND block_off_plan IS NOT NULL ORDER BY date, id`, s.userID, block)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.SessionLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *userStore) LinkSession(ctx context.Context, link training.SessionLink, block *training.Block) error {
	defer s.writing(ctx)()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if link.MicrocycleID != 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM block_microcycles WHERE id=? AND block_id=? AND user_id=?`, link.MicrocycleID, link.BlockID, s.userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return training.ErrNotFound
		}
	} else if link.BlockID != 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks WHERE id=? AND user_id=?`, link.BlockID, s.userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return training.ErrNotFound
		}
	}
	offPlan := 0
	if link.OffPlan {
		offPlan = 1
	}
	res, err := tx.ExecContext(ctx, `UPDATE sessions SET block_id=?,block_microcycle_id=?,block_off_plan=? WHERE id=? AND user_id=?`,
		nullID(link.BlockID), nullID(link.MicrocycleID), offPlan, link.SessionID, s.userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return orNotFound(err)
	}
	if block != nil {
		if _, err := s.saveBlock(ctx, tx, *block); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *userStore) SaveBlock(ctx context.Context, b training.Block) (training.Block, error) {
	defer s.writing(ctx)()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Block{}, err
	}
	defer tx.Rollback()
	id, err := s.saveBlock(ctx, tx, b)
	if err != nil {
		return training.Block{}, err
	}
	saved, err := s.getBlock(ctx, tx, id)
	if err != nil {
		return training.Block{}, err
	}
	return saved, tx.Commit()
}

// saveBlock writes a block, its microcycles and its rotations inside tx. Rows
// that did not change are left as they are, so an undo image holds only what
// the change touched.
func (s *userStore) saveBlock(ctx context.Context, tx *sql.Tx, b training.Block) (int64, error) {
	if b.GoalID != 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM goals WHERE id=? AND user_id=?`, b.GoalID, s.userID).Scan(&n); err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, training.ErrNotFound
		}
	}
	var stored training.Block
	if b.ID == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO blocks(user_id,goal_id,intent,advance,microcycle_days,status,start_date,ended_on) VALUES (?,?,?,?,?,?,?,?)`,
			s.userID, nullID(b.GoalID), b.Intent, b.Advance, b.MicrocycleDays, b.Status, nullText(b.StartDate), nullText(b.EndedOn))
		if err != nil {
			return 0, err
		}
		if b.ID, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	} else {
		var err error
		if stored, err = s.getBlock(ctx, tx, b.ID); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE blocks SET goal_id=?,intent=?,advance=?,microcycle_days=?,status=?,start_date=?,ended_on=? WHERE id=? AND user_id=?`,
			nullID(b.GoalID), b.Intent, b.Advance, b.MicrocycleDays, b.Status, nullText(b.StartDate), nullText(b.EndedOn), b.ID, s.userID); err != nil {
			return 0, err
		}
	}
	// Microcycles dropped from the list go first, with their own rotations.
	keep := map[int64]bool{}
	for _, m := range b.Microcycles {
		keep[m.ID] = true
	}
	for _, m := range stored.Microcycles {
		if keep[m.ID] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM block_rotations WHERE microcycle_id=? AND user_id=?`, m.ID, s.userID); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM block_microcycles WHERE id=? AND user_id=?`, m.ID, s.userID); err != nil {
			return 0, err
		}
	}
	storedRotation := map[int64][]training.RotationEntry{0: stored.Rotation}
	for _, m := range stored.Microcycles {
		storedRotation[m.ID] = m.Rotation
	}
	if err := s.writeRotation(ctx, tx, b.ID, 0, storedRotation[0], b.Rotation); err != nil {
		return 0, err
	}
	for _, m := range b.Microcycles {
		var sets, effort any
		if m.SetsDelta != 0 {
			sets = m.SetsDelta
		}
		if m.EffortDelta != 0 {
			effort = m.EffortDelta
		}
		var closed any = nullText(m.ClosedOn)
		if m.ID == 0 {
			res, err := tx.ExecContext(ctx, `INSERT INTO block_microcycles(user_id,block_id,position,label,sets_delta,effort_delta,own_rotation,status,closed_on) VALUES (?,?,?,?,?,?,?,?,?)`,
				s.userID, b.ID, m.Position, m.Label, sets, effort, m.OwnRotation, m.Status, closed)
			if err != nil {
				return 0, err
			}
			if m.ID, err = res.LastInsertId(); err != nil {
				return 0, err
			}
		} else {
			res, err := tx.ExecContext(ctx, `UPDATE block_microcycles SET position=?,label=?,sets_delta=?,effort_delta=?,own_rotation=?,status=?,closed_on=? WHERE id=? AND block_id=? AND user_id=?`,
				m.Position, m.Label, sets, effort, m.OwnRotation, m.Status, closed, m.ID, b.ID, s.userID)
			if err != nil {
				return 0, err
			}
			if n, err := res.RowsAffected(); err != nil || n == 0 {
				return 0, orNotFound(err)
			}
		}
		var want []training.RotationEntry
		if m.OwnRotation {
			want = m.Rotation
		}
		if err := s.writeRotation(ctx, tx, b.ID, m.ID, storedRotation[m.ID], want); err != nil {
			return 0, err
		}
	}
	return b.ID, nil
}

// writeRotation replaces a rotation (microcycle 0: the block's) only when it
// changed. A routine is stored by id when it is still the user's, by its name
// alone once it was deleted.
func (s *userStore) writeRotation(ctx context.Context, tx *sql.Tx, block, microcycle int64, stored, want []training.RotationEntry) error {
	if len(stored) == len(want) {
		same := true
		for i := range want {
			if stored[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM block_rotations WHERE block_id=? AND microcycle_id IS ? AND user_id=?`, block, nullID(microcycle), s.userID); err != nil {
		return err
	}
	for i, r := range want {
		if r.PlanID != 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans WHERE id=? AND user_id=?`, r.PlanID, s.userID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return training.ErrNotFound
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO block_rotations(user_id,block_id,microcycle_id,position,plan_id,plan_name) VALUES (?,?,?,?,?,?)`,
			s.userID, block, nullID(microcycle), i+1, nullID(r.PlanID), r.PlanName); err != nil {
			return err
		}
	}
	return nil
}
