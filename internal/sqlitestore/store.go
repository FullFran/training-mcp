package sqlitestore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"sort"

	_ "modernc.org/sqlite"

	"github.com/fullfran/training-mcp/internal/training"
)

//go:embed migrations/*.sql
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
	for _, m := range mustMigrations() {
		if _, err = db.Exec(m); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

// mustMigrations returns every migration in filename order. Each one is
// idempotent (CREATE TABLE IF NOT EXISTS), so replaying them all on start is
// safe and keeps an existing database up to date.
func mustMigrations() []string {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		panic(err)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, n := range names {
		b, err := migrationFS.ReadFile(n)
		if err != nil {
			panic(err)
		}
		out = append(out, string(b))
	}
	return out
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
	total = training.NormalizeSI(total)
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
		v.SI = training.NormalizeSI(v.SI)
		out.Sets = append(out.Sets, v)
		out.TotalSI += v.SI
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.TotalSI = training.NormalizeSI(out.TotalSI)
	return out, nil
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
		v.TotalSI = training.NormalizeSI(v.TotalSI)
		out = append(out, v)
	}
	return out, rows.Err()
}

// RecentExercises returns the newest set of each distinct exercise, most
// recently used first. MAX(id) picks the latest row per exercise because ids
// are monotonic within a table lifetime.
func (s *Store) RecentExercises(ctx context.Context, limit int) ([]training.ExerciseMemory, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT exercise,weight_kg,reps,rpe FROM sets
WHERE id IN (SELECT MAX(id) FROM sets GROUP BY exercise)
ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.ExerciseMemory
	for rows.Next() {
		var v training.ExerciseMemory
		if err = rows.Scan(&v.Exercise, &v.WeightKG, &v.Reps, &v.RPE); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteSession removes a session and, by ON DELETE CASCADE, all of its sets.
// It returns how many sets went with it so the caller can report the cost of an
// irreversible delete.
func (s *Store) DeleteSession(ctx context.Context, id int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sets WHERE session_id=?`, id).Scan(&n)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected == 0 {
		return 0, training.ErrNotFound
	}
	return n, tx.Commit()
}

func (s *Store) ExerciseHistory(ctx context.Context, exercise string, limit int) (training.ExerciseHistory, error) {
	out := training.ExerciseHistory{Exercise: exercise, Sets: []training.ExerciseSet{}}
	err := s.db.QueryRowContext(ctx, `SELECT muscle_group FROM exercise_groups WHERE exercise=?`, exercise).Scan(&out.MuscleGroup)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT st.id,st.session_id,se.date,st.weight_kg,st.reps,st.rpe,st.si
FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE st.exercise=?
ORDER BY se.date DESC, st.position DESC LIMIT ?`, exercise, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v training.ExerciseSet
		if err = rows.Scan(&v.SetID, &v.SessionID, &v.Date, &v.WeightKG, &v.Reps, &v.RPE, &v.SI); err != nil {
			return out, err
		}
		v.SI = training.NormalizeSI(v.SI)
		v.Est1RM = training.Epley1RM(v.WeightKG, v.Reps)
		out.Sets = append(out.Sets, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	// The record is computed over the whole history, not just the page returned.
	var best training.ExerciseSet
	err = s.db.QueryRowContext(ctx, `
SELECT st.id,st.session_id,se.date,st.weight_kg,st.reps,st.rpe,st.si
FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE st.exercise=?
ORDER BY st.weight_kg*(1+CAST(st.reps AS REAL)/30) DESC, se.date DESC LIMIT 1`, exercise).
		Scan(&best.SetID, &best.SessionID, &best.Date, &best.WeightKG, &best.Reps, &best.RPE, &best.SI)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	best.SI = training.NormalizeSI(best.SI)
	best.Est1RM = training.Epley1RM(best.WeightKG, best.Reps)
	out.Best = &best
	return out, nil
}

// WeeklyVolume buckets SI per muscle group by training week. The week start is
// computed as the Monday on or before the session date.
func (s *Store) WeeklyVolume(ctx context.Context, f training.ListFilter) ([]training.WeeklyVolume, error) {
	q := `SELECT date(se.date, '-' || ((CAST(strftime('%w', se.date) AS INTEGER) + 6) % 7) || ' days') AS wk,
COALESCE(g.muscle_group,''),COALESCE(SUM(st.si),0),COUNT(st.id)
FROM sets st
JOIN sessions se ON se.id=st.session_id
LEFT JOIN exercise_groups g ON g.exercise=st.exercise
WHERE 1=1`
	args := []any{}
	if f.From != "" {
		q += ` AND se.date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND se.date<=?`
		args = append(args, f.To)
	}
	q += ` GROUP BY wk,2 ORDER BY wk DESC,3 DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.WeeklyVolume
	for rows.Next() {
		var v training.WeeklyVolume
		if err = rows.Scan(&v.WeekStart, &v.MuscleGroup, &v.TotalSI, &v.Sets); err != nil {
			return nil, err
		}
		v.TotalSI = training.NormalizeSI(v.TotalSI)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SetExerciseGroup(ctx context.Context, g training.ExerciseGroup) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO exercise_groups(exercise,muscle_group,updated_at) VALUES (?,?,CURRENT_TIMESTAMP)
ON CONFLICT(exercise) DO UPDATE SET muscle_group=excluded.muscle_group, updated_at=CURRENT_TIMESTAMP`,
		g.Exercise, g.MuscleGroup)
	return err
}

func (s *Store) ExerciseGroups(ctx context.Context) ([]training.ExerciseGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exercise,muscle_group FROM exercise_groups ORDER BY muscle_group,exercise`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.ExerciseGroup
	for rows.Next() {
		var v training.ExerciseGroup
		if err = rows.Scan(&v.Exercise, &v.MuscleGroup); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VolumeByGroup left-joins the catalogue so sets whose exercise has no mapping
// still appear, grouped under an empty muscle group.
func (s *Store) VolumeByGroup(ctx context.Context, f training.ListFilter) ([]training.GroupVolume, error) {
	q := `SELECT COALESCE(g.muscle_group,''),COALESCE(SUM(st.si),0),COUNT(st.id)
FROM sets st
JOIN sessions se ON se.id=st.session_id
LEFT JOIN exercise_groups g ON g.exercise=st.exercise
WHERE 1=1`
	args := []any{}
	if f.From != "" {
		q += ` AND se.date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND se.date<=?`
		args = append(args, f.To)
	}
	q += ` GROUP BY 1 ORDER BY 2 DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.GroupVolume
	for rows.Next() {
		var v training.GroupVolume
		if err = rows.Scan(&v.MuscleGroup, &v.TotalSI, &v.Sets); err != nil {
			return nil, err
		}
		v.TotalSI = training.NormalizeSI(v.TotalSI)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) total(ctx context.Context, id int64) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(si),0) FROM sets WHERE session_id=?`, id).Scan(&total)
	return training.NormalizeSI(total), err
}
