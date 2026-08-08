package sqlitestore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

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
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// migrate applies each migration exactly once, recording its version in
// schema_migrations. Version tracking is required rather than replaying: some
// migrations use ALTER TABLE, which is not idempotent.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	applied := map[int]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}

	for _, name := range mustMigrationNames() {
		version, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %q has no numeric version prefix", name)
		}
		if applied[version] {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		// Each migration and its bookkeeping commit together, so a failure
		// never leaves a half-applied schema marked as done.
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func mustMigrationNames() []string {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		panic(err)
	}
	sort.Strings(names)
	return names
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Start(ctx context.Context, session training.Session) (training.Session, error) {
	// The plan name is snapshotted alongside the id so history stays readable
	// even if the plan is later renamed or deleted.
	if session.PlanID > 0 && session.PlanName == "" {
		p, err := s.GetPlan(ctx, session.PlanID)
		if err != nil {
			return training.Session{}, err
		}
		session.PlanName = p.Name
	}
	var planID any
	if session.PlanID > 0 {
		planID = session.PlanID
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO sessions(date, started_at, plan_id, plan_name) VALUES (?, CURRENT_TIMESTAMP, ?, ?)`,
		session.Date, planID, session.PlanName)
	if err != nil {
		return training.Session{}, err
	}
	if session.ID, err = res.LastInsertId(); err != nil {
		return training.Session{}, err
	}
	if session.PlanID > 0 {
		// Copy the plan into the session so adjusting today never edits the
		// template, and editing the template never rewrites this session.
		if _, err = s.db.ExecContext(ctx, `
INSERT INTO session_items (session_id,position,exercise,target_sets,rep_min,rep_max,target_rpe)
SELECT ?,position,exercise,target_sets,rep_min,rep_max,target_rpe FROM plan_items WHERE plan_id=?`,
			session.ID, session.PlanID); err != nil {
			return training.Session{}, err
		}
	}
	return session, nil
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
	res, err := tx.ExecContext(ctx, `INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,technique,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, in.SessionID, pos, in.Exercise, in.WeightKG, in.Reps, in.RPE, si, in.Technique)
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
	return training.Set{ID: id, SessionID: in.SessionID, Position: pos, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, SI: si, Technique: in.Technique}, total, nil
}
func (s *Store) UpdateSet(ctx context.Context, id int64, p training.SetPatch) (training.Set, float64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Set{}, 0, err
	}
	defer tx.Rollback()
	var set training.Set
	err = tx.QueryRowContext(ctx, `SELECT id,session_id,position,exercise,weight_kg,reps,rpe,si,technique FROM sets WHERE id=?`, id).Scan(&set.ID, &set.SessionID, &set.Position, &set.Exercise, &set.WeightKG, &set.Reps, &set.RPE, &set.SI, &set.Technique)
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
	if p.Technique != nil {
		set.Technique = *p.Technique
	}
	set.SI = training.CalculateSI(set.RPE)
	_, err = tx.ExecContext(ctx, `UPDATE sets SET exercise=?,weight_kg=?,reps=?,rpe=?,si=?,technique=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, set.Exercise, set.WeightKG, set.Reps, set.RPE, set.SI, set.Technique, id)
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
	var planID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,date,plan_id,plan_name FROM sessions WHERE id=?`, id).
		Scan(&out.ID, &out.Date, &planID, &out.PlanName)
	out.PlanID = planID.Int64
	if errors.Is(err, sql.ErrNoRows) {
		return out, training.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,position,exercise,weight_kg,reps,rpe,si,technique FROM sets WHERE session_id=? ORDER BY position`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v training.Set
		if err = rows.Scan(&v.ID, &v.SessionID, &v.Position, &v.Exercise, &v.WeightKG, &v.Reps, &v.RPE, &v.SI, &v.Technique); err != nil {
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
	q := `SELECT s.id,s.date,COUNT(st.id),COALESCE(SUM(st.si),0),s.plan_name FROM sessions s LEFT JOIN sets st ON st.session_id=s.id WHERE 1=1`
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
		if err = rows.Scan(&v.ID, &v.Date, &v.SetCount, &v.TotalSI, &v.PlanName); err != nil {
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

func (s *Store) CreatePlan(ctx context.Context, p training.Plan) (training.Plan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Plan{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO plans(name,notes,created_at) VALUES (?,?,CURRENT_TIMESTAMP)`, p.Name, p.Notes)
	if err != nil {
		return training.Plan{}, err
	}
	if p.ID, err = res.LastInsertId(); err != nil {
		return training.Plan{}, err
	}
	for i := range p.Items {
		p.Items[i].Position = i + 1
		it := p.Items[i]
		if _, err = tx.ExecContext(ctx, `INSERT INTO plan_items(plan_id,position,exercise,target_sets,rep_min,rep_max,target_rpe) VALUES (?,?,?,?,?,?,?)`,
			p.ID, it.Position, it.Exercise, it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE); err != nil {
			return training.Plan{}, err
		}
		p.TotalSets += it.TargetSets
	}
	return p, tx.Commit()
}

func (s *Store) ListPlans(ctx context.Context) ([]training.Plan, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id,p.name,p.notes,COALESCE(SUM(i.target_sets),0),COUNT(i.id)
FROM plans p LEFT JOIN plan_items i ON i.plan_id=p.id
GROUP BY p.id ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.Plan
	for rows.Next() {
		var p training.Plan
		var items int
		if err = rows.Scan(&p.ID, &p.Name, &p.Notes, &p.TotalSets, &items); err != nil {
			return nil, err
		}
		// Items are left empty here; the list view only needs the totals.
		p.Items = make([]training.PlanItem, 0, items)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPlan(ctx context.Context, id int64) (training.Plan, error) {
	var p training.Plan
	err := s.db.QueryRowContext(ctx, `SELECT id,name,notes FROM plans WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return p, training.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT position,exercise,target_sets,rep_min,rep_max,target_rpe FROM plan_items WHERE plan_id=? ORDER BY position`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var it training.PlanItem
		if err = rows.Scan(&it.Position, &it.Exercise, &it.TargetSets, &it.RepMin, &it.RepMax, &it.TargetRPE); err != nil {
			return p, err
		}
		p.Items = append(p.Items, it)
		p.TotalSets += it.TargetSets
	}
	return p, rows.Err()
}

func (s *Store) DeletePlan(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plans WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return training.ErrNotFound
	}
	return nil
}

// SessionProgress lists the session's planned exercises with how many sets are
// done, then any exercise performed that the plan did not prescribe.
func (s *Store) SessionProgress(ctx context.Context, sessionID int64) ([]training.PlanProgress, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=?`, sessionID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, training.ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT i.exercise, i.target_sets, i.rep_min, i.rep_max, i.target_rpe, i.skipped,
       (SELECT COUNT(*) FROM sets st WHERE st.session_id=i.session_id AND st.exercise=i.exercise),
       COALESCE(g.muscle_group,'')
FROM session_items i LEFT JOIN exercise_groups g ON g.exercise=i.exercise
WHERE i.session_id=? ORDER BY i.position`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	planned := map[string]bool{}
	var out []training.PlanProgress
	for rows.Next() {
		var v training.PlanProgress
		if err = rows.Scan(&v.Exercise, &v.TargetSets, &v.RepMin, &v.RepMax, &v.TargetRPE, &v.Skipped, &v.DoneSets, &v.MuscleGroup); err != nil {
			return nil, err
		}
		planned[v.Exercise] = true
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	extra, err := s.db.QueryContext(ctx, `
SELECT st.exercise, COUNT(*), COALESCE(g.muscle_group,'')
FROM sets st LEFT JOIN exercise_groups g ON g.exercise=st.exercise
WHERE st.session_id=? GROUP BY st.exercise ORDER BY MIN(st.position)`, sessionID)
	if err != nil {
		return nil, err
	}
	defer extra.Close()
	for extra.Next() {
		var v training.PlanProgress
		if err = extra.Scan(&v.Exercise, &v.DoneSets, &v.MuscleGroup); err != nil {
			return nil, err
		}
		if !planned[v.Exercise] {
			out = append(out, v)
		}
	}
	return out, extra.Err()
}

// SetSessionItem adds an exercise to today's session plan, or replaces its
// prescription if it is already there. New items go last.
func (s *Store) SetSessionItem(ctx context.Context, sessionID int64, it training.PlanItem) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=?`, sessionID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return training.ErrNotFound
	}
	var pos int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM session_items WHERE session_id=?`, sessionID).Scan(&pos); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO session_items (session_id,position,exercise,target_sets,rep_min,rep_max,target_rpe)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(session_id,exercise) DO UPDATE SET
 target_sets=excluded.target_sets, rep_min=excluded.rep_min,
 rep_max=excluded.rep_max, target_rpe=excluded.target_rpe`,
		sessionID, pos, it.Exercise, it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE)
	return err
}

// PatchSessionItem changes only the fields given, so bumping the set count does
// not require restating the rep range.
func (s *Store) PatchSessionItem(ctx context.Context, sessionID int64, exercise string, p training.SessionItemPatch) error {
	var it training.PlanItem
	var skipped bool
	err := s.db.QueryRowContext(ctx, `SELECT exercise,target_sets,rep_min,rep_max,target_rpe,skipped FROM session_items WHERE session_id=? AND exercise=?`,
		sessionID, exercise).Scan(&it.Exercise, &it.TargetSets, &it.RepMin, &it.RepMax, &it.TargetRPE, &skipped)
	if errors.Is(err, sql.ErrNoRows) {
		return training.ErrNotFound
	}
	if err != nil {
		return err
	}
	if p.TargetSets != nil {
		it.TargetSets = *p.TargetSets
	}
	if p.RepMin != nil {
		it.RepMin = *p.RepMin
	}
	if p.RepMax != nil {
		it.RepMax = *p.RepMax
	}
	if p.TargetRPE != nil {
		it.TargetRPE = *p.TargetRPE
	}
	if p.Skipped != nil {
		skipped = *p.Skipped
	}
	_, err = s.db.ExecContext(ctx, `UPDATE session_items SET target_sets=?,rep_min=?,rep_max=?,target_rpe=?,skipped=? WHERE session_id=? AND exercise=?`,
		it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE, skipped, sessionID, exercise)
	return err
}

// SwapSessionItem substitutes one exercise for another in place, keeping its
// position and prescription. This is the occupied-machine case.
func (s *Store) SwapSessionItem(ctx context.Context, sessionID int64, from, to string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE session_items SET exercise=? WHERE session_id=? AND exercise=?`, to, sessionID, from)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return training.ErrNotFound
	}
	return nil
}

// RemoveSessionItem drops an exercise from today's plan. Sets already logged
// against it are untouched and resurface as off-plan work.
func (s *Store) RemoveSessionItem(ctx context.Context, sessionID int64, exercise string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM session_items WHERE session_id=? AND exercise=?`, sessionID, exercise)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return training.ErrNotFound
	}
	return nil
}

func (s *Store) SetFeedback(ctx context.Context, sessionID int64, f training.Feedback) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=?`, sessionID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return training.ErrNotFound
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO session_feedback (session_id,muscle_group,fatigue,pump,recovery,created_at)
VALUES (?,?,?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(session_id,muscle_group) DO UPDATE SET
 fatigue=excluded.fatigue, pump=excluded.pump, recovery=excluded.recovery, created_at=CURRENT_TIMESTAMP`,
		sessionID, f.MuscleGroup, f.Fatigue, f.Pump, f.Recovery)
	return err
}

func (s *Store) SessionFeedback(ctx context.Context, sessionID int64) ([]training.Feedback, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT muscle_group,fatigue,pump,recovery FROM session_feedback WHERE session_id=? ORDER BY muscle_group`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.Feedback
	for rows.Next() {
		var f training.Feedback
		if err = rows.Scan(&f.MuscleGroup, &f.Fatigue, &f.Pump, &f.Recovery); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// LatestFeedback returns the most recent rating per muscle group, which is what
// next week's volume should respond to.
func (s *Store) LatestFeedback(ctx context.Context) (map[string]training.Feedback, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT muscle_group,fatigue,pump,recovery FROM session_feedback
WHERE id IN (SELECT MAX(id) FROM session_feedback GROUP BY muscle_group)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]training.Feedback{}
	for rows.Next() {
		var f training.Feedback
		if err = rows.Scan(&f.MuscleGroup, &f.Fatigue, &f.Pump, &f.Recovery); err != nil {
			return nil, err
		}
		out[f.MuscleGroup] = f
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
SELECT st.id,st.session_id,se.date,st.weight_kg,st.reps,st.rpe,st.si,st.technique
FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE st.exercise=?
ORDER BY se.date DESC, st.position DESC LIMIT ?`, exercise, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v training.ExerciseSet
		if err = rows.Scan(&v.SetID, &v.SessionID, &v.Date, &v.WeightKG, &v.Reps, &v.RPE, &v.SI, &v.Technique); err != nil {
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
SELECT st.id,st.session_id,se.date,st.weight_kg,st.reps,st.rpe,st.si,st.technique
FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE st.exercise=? AND st.technique=''
ORDER BY st.weight_kg*(1+CAST(st.reps AS REAL)/30) DESC, se.date DESC LIMIT 1`, exercise).
		Scan(&best.SetID, &best.SessionID, &best.Date, &best.WeightKG, &best.Reps, &best.RPE, &best.SI, &best.Technique)
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
