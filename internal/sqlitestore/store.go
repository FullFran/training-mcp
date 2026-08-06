package sqlitestore

import (
	"context"
	"database/sql"
	"embed"
	"errors"

	_ "modernc.org/sqlite"

	"github.com/fullfran/training-mcp/internal/training"
)

//go:embed migrations/001_init.sql
var migrationFS embed.FS

// Store is the SQLite adapter. The unexported failpoint is only used by
// same-package tests to prove transaction rollback at an intermediate write boundary.
type Store struct {
	db        *sql.DB
	failpoint func(string) error
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if _, err = db.Exec(string(mustMigration())); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func mustMigration() []byte {
	b, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		panic(err)
	}
	return b
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Start(ctx context.Context, session training.Session) (training.Session, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO sessions(date, started_at) VALUES (?, CURRENT_TIMESTAMP)`, session.Date)
	if err != nil {
		return training.Session{}, err
	}
	session.ID, err = res.LastInsertId()
	return session, err
}
func (s *Store) AddSet(ctx context.Context, in training.AddSetInput) (training.Set, float64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Set{}, 0, err
	}
	defer tx.Rollback()
	var pos int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM sets WHERE session_id=?`, in.SessionID).Scan(&pos); err != nil {
		return training.Set{}, 0, err
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=?`, in.SessionID).Scan(&exists); err != nil || exists == 0 {
		if exists == 0 {
			return training.Set{}, 0, training.ErrNotFound
		}
		return training.Set{}, 0, err
	}
	si := training.CalculateSI(in.RPE)
	res, err := tx.ExecContext(ctx, `INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at) VALUES (?,?,?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, in.SessionID, pos, in.Exercise, in.WeightKG, in.Reps, in.RPE, si)
	if err != nil {
		return training.Set{}, 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return training.Set{}, 0, err
	}
	var total float64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(si),0) FROM sets WHERE session_id=?`, in.SessionID).Scan(&total); err != nil {
		return training.Set{}, 0, err
	}
	if err = tx.Commit(); err != nil {
		return training.Set{}, 0, err
	}
	return training.Set{ID: id, SessionID: in.SessionID, Position: pos, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, SI: si}, total, nil
}
func (s *Store) UpdateSet(ctx context.Context, id int64, p training.SetPatch) (training.Set, float64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Set{}, 0, err
	}
	defer tx.Rollback()
	var set training.Set
	err = tx.QueryRowContext(ctx, `SELECT id,session_id,position,exercise,weight_kg,reps,rpe,si FROM sets WHERE id=?`, id).Scan(&set.ID, &set.SessionID, &set.Position, &set.Exercise, &set.WeightKG, &set.Reps, &set.RPE, &set.SI)
	if errors.Is(err, sql.ErrNoRows) {
		return training.Set{}, 0, training.ErrNotFound
	}
	if err != nil {
		return training.Set{}, 0, err
	}
	if p.Exercise != nil {
		set.Exercise = *p.Exercise
	}
	if p.WeightKG != nil {
		set.WeightKG = *p.WeightKG
	}
	if p.Reps != nil {
		set.Reps = *p.Reps
	}
	if p.RPE != nil {
		set.RPE = *p.RPE
	}
	set.SI = training.CalculateSI(set.RPE)
	_, err = tx.ExecContext(ctx, `UPDATE sets SET exercise=?,weight_kg=?,reps=?,rpe=?,si=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, set.Exercise, set.WeightKG, set.Reps, set.RPE, set.SI, id)
	if err != nil {
		return training.Set{}, 0, err
	}
	if err = tx.Commit(); err != nil {
		return training.Set{}, 0, err
	}
	total, err := s.total(ctx, set.SessionID)
	return set, total, err
}
func (s *Store) DeleteSet(ctx context.Context, id int64) (int64, float64, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	var sessionID int64
	if err = tx.QueryRowContext(ctx, `SELECT session_id FROM sets WHERE id=?`, id).Scan(&sessionID); errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, training.ErrNotFound
	}
	if err != nil {
		return 0, 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sets WHERE id=?`, id); err != nil {
		return 0, 0, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM sets WHERE session_id=? ORDER BY position,id`, sessionID)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var v int64
		if err = rows.Scan(&v); err != nil {
			return 0, 0, 0, err
		}
		ids = append(ids, v)
	}
	if err = rows.Err(); err != nil {
		return 0, 0, 0, err
	}
	for _, v := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE sets SET position=-position-1000000 WHERE id=?`, v); err != nil {
			return 0, 0, 0, err
		}
	}
	for i, v := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE sets SET position=? WHERE id=?`, i+1, v); err != nil {
			return 0, 0, 0, err
		}
	}
	if s.failpoint != nil {
		if err = s.failpoint("delete-resequence"); err != nil {
			return 0, 0, 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	total, err := s.total(ctx, sessionID)
	return sessionID, total, len(ids), err
}
func (s *Store) GetSession(ctx context.Context, id int64) (training.Session, error) {
	var out training.Session
	err := s.db.QueryRowContext(ctx, `SELECT id,date FROM sessions WHERE id=?`, id).Scan(&out.ID, &out.Date)
	if errors.Is(err, sql.ErrNoRows) {
		return out, training.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,position,exercise,weight_kg,reps,rpe,si FROM sets WHERE session_id=? ORDER BY position`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v training.Set
		if err = rows.Scan(&v.ID, &v.SessionID, &v.Position, &v.Exercise, &v.WeightKG, &v.Reps, &v.RPE, &v.SI); err != nil {
			return out, err
		}
		out.Sets = append(out.Sets, v)
		out.TotalSI += v.SI
	}
	return out, rows.Err()
}
func (s *Store) ListSessions(ctx context.Context, f training.ListFilter) ([]training.SessionSummary, error) {
	q := `SELECT s.id,s.date,COUNT(st.id),COALESCE(SUM(st.si),0) FROM sessions s LEFT JOIN sets st ON st.session_id=s.id WHERE 1=1`
	args := []any{}
	if f.From != "" {
		q += ` AND s.date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND s.date<=?`
		args = append(args, f.To)
	}
	q += ` GROUP BY s.id ORDER BY s.date DESC,s.id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.SessionSummary
	for rows.Next() {
		var v training.SessionSummary
		if err = rows.Scan(&v.ID, &v.Date, &v.SetCount, &v.TotalSI); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) total(ctx context.Context, id int64) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(si),0) FROM sets WHERE session_id=?`, id).Scan(&total)
	return total, err
}
