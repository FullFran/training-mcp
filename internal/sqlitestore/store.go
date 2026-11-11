package sqlitestore

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

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

	names := mustMigrationNames()
	versions, err := migrationVersions(names)
	if err != nil {
		return err
	}
	for i, name := range names {
		version := versions[i]
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

// migrationVersions reads each migration's number. Versions are recorded by
// number, so two files sharing one would leave one silently unapplied: that
// is refused before anything runs.
func migrationVersions(names []string) ([]int, error) {
	versions := make([]int, len(names))
	seen := map[int]string{}
	for i, name := range names {
		version, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("migration %q has no numeric version prefix", name)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", other, name, version)
		}
		seen[version] = name
		versions[i] = version
	}
	return versions, nil
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

// Snapshot writes a consistent copy of the whole database to path. VACUUM INTO
// takes the copy inside a read transaction, so it is safe while the server is
// serving and the result is a plain SQLite file that can simply be copied back.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	if strings.ContainsAny(path, "'\"") {
		return training.ErrValidation
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO '`+path+`'`)
	return err
}

// ForUser returns the data methods bound to one user. Nothing else in this package
// reaches user data, so every statement knows whose row it is touching.
func (s *Store) ForUser(userID int64) training.UserStore {
	return &userStore{db: s.db, failpoint: s.failpoint, userID: userID}
}

func (s *Store) UserBySubject(ctx context.Context, subject string) (int64, bool, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE clerk_subject=?`, subject).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return userID, err == nil, err
}

// ProvisionUser is one statement, as a client's first calls arrive together.
func (s *Store) ProvisionUser(ctx context.Context, subject string) (int64, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO users(clerk_subject,created_at) VALUES (?,CURRENT_TIMESTAMP)
		ON CONFLICT(clerk_subject) DO UPDATE SET clerk_subject=excluded.clerk_subject RETURNING id`, subject).Scan(&userID)
	return userID, err
}

// userStore is the Store as seen by one user.
type userStore struct {
	db        *sql.DB
	failpoint func(string) error
	userID    int64
}

func (s *userStore) Start(ctx context.Context, session training.Session) (training.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Session{}, err
	}
	defer tx.Rollback()
	if session.PlanID > 0 {
		// The plan name is snapshotted alongside the id so history stays readable
		// even if the plan is later renamed or deleted. The plan comes from the
		// statement that stores it, so it cannot change owner in between.
		res, err := tx.ExecContext(ctx, `
INSERT INTO sessions(user_id, date, started_at, plan_id, plan_name)
SELECT user_id, ?, CURRENT_TIMESTAMP, id, COALESCE(NULLIF(?,''),name) FROM plans WHERE id=? AND user_id=?`,
			session.Date, session.PlanName, session.PlanID, s.userID)
		if err != nil {
			return training.Session{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return training.Session{}, err
		}
		if n == 0 {
			return training.Session{}, training.ErrNotFound
		}
		if session.ID, err = res.LastInsertId(); err != nil {
			return training.Session{}, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT plan_name FROM sessions WHERE id=?`, session.ID).Scan(&session.PlanName); err != nil {
			return training.Session{}, err
		}
		// Copy the plan into the session so adjusting today never edits the
		// template, and editing the template never rewrites this session.
		if _, err = tx.ExecContext(ctx, `
INSERT INTO session_items (session_id,position,exercise,target_sets,rep_min,rep_max,target_rpe,superset,notes)
SELECT ?,position,exercise,target_sets,rep_min,rep_max,target_rpe,superset,notes FROM plan_items
WHERE plan_id IN (SELECT id FROM plans WHERE id=? AND user_id=?)`,
			session.ID, session.PlanID, s.userID); err != nil {
			return training.Session{}, err
		}
	} else {
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions(user_id, date, started_at, plan_id, plan_name) VALUES (?, ?, CURRENT_TIMESTAMP, NULL, ?)`,
			s.userID, session.Date, session.PlanName)
		if err != nil {
			return training.Session{}, err
		}
		if session.ID, err = res.LastInsertId(); err != nil {
			return training.Session{}, err
		}
	}
	return session, tx.Commit()
}
func (s *userStore) AddSet(ctx context.Context, in training.AddSetInput) (training.Set, float64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Set{}, 0, err
	}
	defer tx.Rollback()
	set, total, err := insertSet(ctx, tx, s.userID, in)
	if err != nil {
		return training.Set{}, 0, err
	}
	if err = tx.Commit(); err != nil {
		return training.Set{}, 0, err
	}
	return set, total, nil
}

// insertSet appends one set to a session inside an open transaction, returning
// it with the session's recalculated total. Every path that records a set goes
// through here so position and SI cannot be computed two ways.
func insertSet(ctx context.Context, tx *sql.Tx, userID int64, in training.AddSetInput) (training.Set, float64, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=? AND user_id=?`, in.SessionID, userID).Scan(&exists)
	if err != nil {
		return training.Set{}, 0, err
	}
	if exists == 0 {
		return training.Set{}, 0, training.ErrNotFound
	}
	var pos int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM sets WHERE session_id=?`, in.SessionID).Scan(&pos); err != nil {
		return training.Set{}, 0, err
	}
	si := training.CalculateSI(in.RPE)
	res, err := tx.ExecContext(ctx, `INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,rpe_imputed,si,technique,superset,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, in.SessionID, pos, in.Exercise, in.WeightKG, in.Reps, in.RPE, in.RPEImputed, si, in.Technique, in.Superset)
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
	return training.Set{ID: id, SessionID: in.SessionID, Position: pos, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, RPEImputed: in.RPEImputed, SI: si, Technique: in.Technique, Superset: in.Superset}, total, nil
}

// LogSets records a batch of sets into the given date's session, creating it if
// needed, all or nothing. A key already recorded returns its original result.
//
// The first statement is a write on purpose: it takes SQLite's write lock before
// anything is read, so concurrent calls queue on busy_timeout rather than failing
// when a read transaction tries to upgrade, and each one sees the previous result.
func (s *userStore) LogSets(ctx context.Context, in training.LogSetsRecord) (training.LogSetsResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.LogSetsResult{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(user_id,key,fingerprint,response,created_at) VALUES (?,?,?,'',CURRENT_TIMESTAMP) ON CONFLICT(user_id,key) DO NOTHING`, s.userID, in.Key, in.Fingerprint)
	if err != nil {
		return training.LogSetsResult{}, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return training.LogSetsResult{}, err
	}
	if inserted == 0 {
		var fingerprint, response string
		if err = tx.QueryRowContext(ctx, `SELECT fingerprint,response FROM idempotency_keys WHERE user_id=? AND key=?`, s.userID, in.Key).Scan(&fingerprint, &response); err != nil {
			return training.LogSetsResult{}, err
		}
		if fingerprint != in.Fingerprint {
			return training.LogSetsResult{}, training.ErrIdempotencyConflict
		}
		var out training.LogSetsResult
		if err = json.Unmarshal([]byte(response), &out); err != nil {
			return training.LogSetsResult{}, err
		}
		out.Replayed = true
		return out, nil
	}

	out := training.LogSetsResult{}
	err = tx.QueryRowContext(ctx, `SELECT id,date,plan_name FROM sessions WHERE user_id=? AND date=? ORDER BY id DESC LIMIT 1`, s.userID, in.Date).Scan(&out.Session.ID, &out.Session.Date, &out.Session.PlanName)
	if errors.Is(err, sql.ErrNoRows) {
		created, err := tx.ExecContext(ctx, `INSERT INTO sessions(user_id, date, started_at, plan_id, plan_name) VALUES (?, ?, CURRENT_TIMESTAMP, NULL, '')`, s.userID, in.Date)
		if err != nil {
			return training.LogSetsResult{}, err
		}
		if out.Session.ID, err = created.LastInsertId(); err != nil {
			return training.LogSetsResult{}, err
		}
		out.Session.Date = in.Date
	} else if err != nil {
		return training.LogSetsResult{}, err
	}

	// A session started from a plan carries each exercise's superset round.
	var superset string
	err = tx.QueryRowContext(ctx, `SELECT superset FROM session_items WHERE session_id=? AND exercise=?`, out.Session.ID, in.Exercise).Scan(&superset)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return training.LogSetsResult{}, err
	}
	for _, set := range in.Sets {
		stored, total, err := insertSet(ctx, tx, s.userID, training.AddSetInput{
			SessionID: out.Session.ID, Exercise: in.Exercise, WeightKG: set.WeightKG,
			Reps: set.Reps, RPE: set.RPE, RPEImputed: set.RPEImputed, Technique: set.Technique, Superset: superset,
		})
		if err != nil {
			return training.LogSetsResult{}, err
		}
		out.Sets = append(out.Sets, stored)
		out.Session.TotalSI = total
	}
	if s.failpoint != nil {
		if err = s.failpoint("log-sets-inserted"); err != nil {
			return training.LogSetsResult{}, err
		}
	}
	response, err := json.Marshal(out)
	if err != nil {
		return training.LogSetsResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE idempotency_keys SET response=? WHERE user_id=? AND key=?`, string(response), s.userID, in.Key); err != nil {
		return training.LogSetsResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < datetime('now','-30 days')`); err != nil {
		return training.LogSetsResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return training.LogSetsResult{}, err
	}
	return out, nil
}
func (s *userStore) UpdateSet(ctx context.Context, id int64, p training.SetPatch) (training.Set, float64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Set{}, 0, err
	}
	defer tx.Rollback()
	var set training.Set
	err = tx.QueryRowContext(ctx, `SELECT st.id,st.session_id,st.position,st.exercise,st.weight_kg,st.reps,st.rpe,st.rpe_imputed,st.si,st.technique FROM sets st JOIN sessions se ON se.id=st.session_id WHERE st.id=? AND se.user_id=?`, id, s.userID).Scan(&set.ID, &set.SessionID, &set.Position, &set.Exercise, &set.WeightKG, &set.Reps, &set.RPE, &set.RPEImputed, &set.SI, &set.Technique)
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
		// Stating an RPE turns a placeholder into a measurement.
		set.RPE, set.RPEImputed = *p.RPE, false
	}
	if p.Technique != nil {
		set.Technique = *p.Technique
	}
	set.SI = training.CalculateSI(set.RPE)
	_, err = tx.ExecContext(ctx, `UPDATE sets SET exercise=?,weight_kg=?,reps=?,rpe=?,rpe_imputed=?,si=?,technique=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND session_id=?`, set.Exercise, set.WeightKG, set.Reps, set.RPE, set.RPEImputed, set.SI, set.Technique, id, set.SessionID)
	if err != nil {
		return training.Set{}, 0, err
	}
	if err = tx.Commit(); err != nil {
		return training.Set{}, 0, err
	}
	total, err := s.total(ctx, set.SessionID)
	return set, total, err
}
func (s *userStore) DeleteSet(ctx context.Context, id int64) (int64, float64, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	var sessionID int64
	if err = tx.QueryRowContext(ctx, `SELECT st.session_id FROM sets st JOIN sessions se ON se.id=st.session_id WHERE st.id=? AND se.user_id=?`, id, s.userID).Scan(&sessionID); errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, training.ErrNotFound
	}
	if err != nil {
		return 0, 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sets WHERE id=? AND session_id=?`, id, sessionID); err != nil {
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
func (s *userStore) GetSession(ctx context.Context, id int64) (training.Session, error) {
	var out training.Session
	var planID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,date,plan_id,plan_name FROM sessions WHERE id=? AND user_id=?`, id, s.userID).
		Scan(&out.ID, &out.Date, &planID, &out.PlanName)
	out.PlanID = planID.Int64
	if errors.Is(err, sql.ErrNoRows) {
		return out, training.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,position,exercise,weight_kg,reps,rpe,rpe_imputed,si,technique,superset FROM sets WHERE session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?) ORDER BY position`, id, s.userID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v training.Set
		if err = rows.Scan(&v.ID, &v.SessionID, &v.Position, &v.Exercise, &v.WeightKG, &v.Reps, &v.RPE, &v.RPEImputed, &v.SI, &v.Technique, &v.Superset); err != nil {
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
func (s *userStore) ListSessions(ctx context.Context, f training.ListFilter) ([]training.SessionSummary, error) {
	q := `SELECT s.id,s.date,COUNT(st.id),COALESCE(SUM(st.si),0),s.plan_name FROM sessions s LEFT JOIN sets st ON st.session_id=s.id WHERE s.user_id=?`
	args := []any{s.userID}
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
func (s *userStore) RecentExercises(ctx context.Context, limit int) ([]training.ExerciseMemory, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT exercise,weight_kg,reps,rpe,rpe_imputed FROM sets
WHERE id IN (SELECT MAX(st.id) FROM sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=? GROUP BY st.exercise)
ORDER BY id DESC LIMIT ?`, s.userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.ExerciseMemory
	for rows.Next() {
		var v training.ExerciseMemory
		if err = rows.Scan(&v.Exercise, &v.WeightKG, &v.Reps, &v.RPE, &v.RPEImputed); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *userStore) CreatePlan(ctx context.Context, p training.Plan) (training.Plan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Plan{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO plans(user_id,name,notes,created_at) VALUES (?,?,?,CURRENT_TIMESTAMP)`, s.userID, p.Name, p.Notes)
	if err != nil {
		return training.Plan{}, err
	}
	if p.ID, err = res.LastInsertId(); err != nil {
		return training.Plan{}, err
	}
	for i := range p.Items {
		p.Items[i].Position = i + 1
		it := p.Items[i]
		if _, err = tx.ExecContext(ctx, `INSERT INTO plan_items(plan_id,position,exercise,target_sets,rep_min,rep_max,target_rpe,superset,notes) VALUES (?,?,?,?,?,?,?,?,?)`,
			p.ID, it.Position, it.Exercise, it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE, it.Superset, it.Notes); err != nil {
			return training.Plan{}, err
		}
		p.TotalSets += it.TargetSets
	}
	return p, tx.Commit()
}

func (s *userStore) ListPlans(ctx context.Context) ([]training.Plan, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id,p.name,p.notes,COALESCE(SUM(i.target_sets),0),COUNT(i.id)
FROM plans p LEFT JOIN plan_items i ON i.plan_id=p.id
WHERE p.user_id=?
GROUP BY p.id ORDER BY p.name`, s.userID)
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

func (s *userStore) GetPlan(ctx context.Context, id int64) (training.Plan, error) {
	var p training.Plan
	err := s.db.QueryRowContext(ctx, `SELECT id,name,notes FROM plans WHERE id=? AND user_id=?`, id, s.userID).Scan(&p.ID, &p.Name, &p.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return p, training.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT position,exercise,target_sets,rep_min,rep_max,target_rpe,superset,notes FROM plan_items WHERE plan_id IN (SELECT id FROM plans WHERE id=? AND user_id=?) ORDER BY position`, id, s.userID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var it training.PlanItem
		if err = rows.Scan(&it.Position, &it.Exercise, &it.TargetSets, &it.RepMin, &it.RepMax, &it.TargetRPE, &it.Superset, &it.Notes); err != nil {
			return p, err
		}
		p.Items = append(p.Items, it)
		p.TotalSets += it.TargetSets
	}
	return p, rows.Err()
}

// UpdatePlan edits a plan's name and notes, leaving its exercises alone.
func (s *userStore) UpdatePlan(ctx context.Context, id int64, p training.PlanPatch) error {
	var name, notes string
	err := s.db.QueryRowContext(ctx, `SELECT name,notes FROM plans WHERE id=? AND user_id=?`, id, s.userID).Scan(&name, &notes)
	if errors.Is(err, sql.ErrNoRows) {
		return training.ErrNotFound
	}
	if err != nil {
		return err
	}
	if p.Name != nil {
		name = *p.Name
	}
	if p.Notes != nil {
		notes = *p.Notes
	}
	_, err = s.db.ExecContext(ctx, `UPDATE plans SET name=?,notes=? WHERE id=? AND user_id=?`, name, notes, id, s.userID)
	return err
}

// SetPlanItem adds an exercise to a plan, or replaces its prescription if it is
// already there. New items go last. Matched by exercise name in code rather
// than by a unique constraint, so existing plans need no schema change.
func (s *userStore) SetPlanItem(ctx context.Context, planID int64, it training.PlanItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans WHERE id=? AND user_id=?`, planID, s.userID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return training.ErrNotFound
	}
	var pos int
	err = tx.QueryRowContext(ctx, `SELECT position FROM plan_items WHERE plan_id=? AND exercise=?`, planID, it.Exercise).Scan(&pos)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM plan_items WHERE plan_id=?`, planID).Scan(&pos); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO plan_items(plan_id,position,exercise,target_sets,rep_min,rep_max,target_rpe,superset,notes) VALUES (?,?,?,?,?,?,?,?,?)`,
			planID, pos, it.Exercise, it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE, it.Superset, it.Notes)
	case err != nil:
		return err
	default:
		_, err = tx.ExecContext(ctx, `UPDATE plan_items SET target_sets=?,rep_min=?,rep_max=?,target_rpe=?,superset=?,notes=? WHERE plan_id=? AND exercise=?`,
			it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE, it.Superset, it.Notes, planID, it.Exercise)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RemovePlanItem drops an exercise and closes the gap in the ordering.
func (s *userStore) RemovePlanItem(ctx context.Context, planID int64, exercise string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM plan_items WHERE plan_id IN (SELECT id FROM plans WHERE id=? AND user_id=?) AND exercise=?`, planID, s.userID, exercise)
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
	if err = resequencePlan(ctx, tx, planID); err != nil {
		return err
	}
	return tx.Commit()
}

// MovePlanItem shifts an exercise up or down by one place. Order matters: it is
// the order the session is performed in.
func (s *userStore) MovePlanItem(ctx context.Context, planID int64, exercise string, delta int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pos int
	err = tx.QueryRowContext(ctx, `SELECT i.position FROM plan_items i JOIN plans p ON p.id=i.plan_id WHERE i.plan_id=? AND i.exercise=? AND p.user_id=?`, planID, exercise, s.userID).Scan(&pos)
	if errors.Is(err, sql.ErrNoRows) {
		return training.ErrNotFound
	}
	if err != nil {
		return err
	}
	var neighbour string
	var neighbourPos int
	order := "ASC"
	cmp := ">"
	if delta < 0 {
		order, cmp = "DESC", "<"
	}
	err = tx.QueryRowContext(ctx,
		`SELECT exercise,position FROM plan_items WHERE plan_id=? AND position`+cmp+`? ORDER BY position `+order+` LIMIT 1`,
		planID, pos).Scan(&neighbour, &neighbourPos)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // already at the end; moving further is a no-op, not an error
	}
	if err != nil {
		return err
	}
	// Park one row out of the way: (plan_id, position) is unique.
	if _, err = tx.ExecContext(ctx, `UPDATE plan_items SET position=-1 WHERE plan_id=? AND exercise=?`, planID, exercise); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plan_items SET position=? WHERE plan_id=? AND exercise=?`, pos, planID, neighbour); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plan_items SET position=? WHERE plan_id=? AND exercise=?`, neighbourPos, planID, exercise); err != nil {
		return err
	}
	return tx.Commit()
}

// resequencePlan compacts positions to 1..n in their current order.
func resequencePlan(ctx context.Context, tx *sql.Tx, planID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT exercise FROM plan_items WHERE plan_id=? ORDER BY position`, planID)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	// Park them all negative first, or renumbering collides with the unique key.
	for i, n := range names {
		if _, err = tx.ExecContext(ctx, `UPDATE plan_items SET position=? WHERE plan_id=? AND exercise=?`, -(i + 1), planID, n); err != nil {
			return err
		}
	}
	for i, n := range names {
		if _, err = tx.ExecContext(ctx, `UPDATE plan_items SET position=? WHERE plan_id=? AND exercise=?`, i+1, planID, n); err != nil {
			return err
		}
	}
	return nil
}

func (s *userStore) DeletePlan(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plans WHERE id=? AND user_id=?`, id, s.userID)
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
func (s *userStore) SessionProgress(ctx context.Context, sessionID int64) ([]training.PlanProgress, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=? AND user_id=?`, sessionID, s.userID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, training.ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT i.superset, i.notes, i.exercise, i.target_sets, i.rep_min, i.rep_max, i.target_rpe, i.skipped,
       (SELECT COUNT(*) FROM sets st WHERE st.session_id=i.session_id AND st.exercise=i.exercise),
       COALESCE(g.muscle_group,'')
FROM session_items i LEFT JOIN exercise_groups g ON g.user_id=? AND g.exercise=i.exercise
WHERE i.session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?) ORDER BY i.position`, s.userID, sessionID, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	planned := map[string]bool{}
	var out []training.PlanProgress
	for rows.Next() {
		var v training.PlanProgress
		if err = rows.Scan(&v.Superset, &v.Notes, &v.Exercise, &v.TargetSets, &v.RepMin, &v.RepMax, &v.TargetRPE, &v.Skipped, &v.DoneSets, &v.MuscleGroup); err != nil {
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
FROM sets st LEFT JOIN exercise_groups g ON g.user_id=? AND g.exercise=st.exercise
WHERE st.session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?) GROUP BY st.exercise ORDER BY MIN(st.position)`, s.userID, sessionID, s.userID)
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
func (s *userStore) SetSessionItem(ctx context.Context, sessionID int64, it training.PlanItem) error {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO session_items (session_id,position,exercise,target_sets,rep_min,rep_max,target_rpe,superset,notes)
SELECT id,(SELECT COALESCE(MAX(position),0)+1 FROM session_items WHERE session_id=?),?,?,?,?,?,?,?
FROM sessions WHERE id=? AND user_id=?
ON CONFLICT(session_id,exercise) DO UPDATE SET
 target_sets=excluded.target_sets, rep_min=excluded.rep_min,
 rep_max=excluded.rep_max, target_rpe=excluded.target_rpe,
 superset=excluded.superset, notes=excluded.notes`,
		sessionID, it.Exercise, it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE, it.Superset, it.Notes, sessionID, s.userID)
	if err != nil {
		return err
	}
	return mustHaveAffected(res)
}

// PatchSessionItem changes only the fields given, so bumping the set count does
// not require restating the rep range.
func (s *userStore) PatchSessionItem(ctx context.Context, sessionID int64, exercise string, p training.SessionItemPatch) error {
	var it training.PlanItem
	var skipped bool
	err := s.db.QueryRowContext(ctx, `SELECT i.exercise,i.target_sets,i.rep_min,i.rep_max,i.target_rpe,i.skipped FROM session_items i JOIN sessions se ON se.id=i.session_id WHERE i.session_id=? AND i.exercise=? AND se.user_id=?`,
		sessionID, exercise, s.userID).Scan(&it.Exercise, &it.TargetSets, &it.RepMin, &it.RepMax, &it.TargetRPE, &skipped)
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
	_, err = s.db.ExecContext(ctx, `UPDATE session_items SET target_sets=?,rep_min=?,rep_max=?,target_rpe=?,skipped=? WHERE session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?) AND exercise=?`,
		it.TargetSets, it.RepMin, it.RepMax, it.TargetRPE, skipped, sessionID, s.userID, exercise)
	return err
}

// SwapSessionItem substitutes one exercise for another in place, keeping its
// position and prescription. This is the occupied-machine case.
func (s *userStore) SwapSessionItem(ctx context.Context, sessionID int64, from, to string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE session_items SET exercise=? WHERE session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?) AND exercise=?`, to, sessionID, s.userID, from)
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
func (s *userStore) RemoveSessionItem(ctx context.Context, sessionID int64, exercise string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM session_items WHERE session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?) AND exercise=?`, sessionID, s.userID, exercise)
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

func (s *userStore) SetFeedback(ctx context.Context, sessionID int64, f training.Feedback) error {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO session_feedback (session_id,muscle_group,fatigue,pump,recovery,created_at)
SELECT id,?,?,?,?,CURRENT_TIMESTAMP FROM sessions WHERE id=? AND user_id=?
ON CONFLICT(session_id,muscle_group) DO UPDATE SET
 fatigue=excluded.fatigue, pump=excluded.pump, recovery=excluded.recovery, created_at=CURRENT_TIMESTAMP`,
		f.MuscleGroup, f.Fatigue, f.Pump, f.Recovery, sessionID, s.userID)
	if err != nil {
		return err
	}
	return mustHaveAffected(res)
}

// mustHaveAffected turns a statement scoped to the user that touched nothing into
// the error a missing id gets.
func mustHaveAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return training.ErrNotFound
	}
	return nil
}

func (s *userStore) SessionFeedback(ctx context.Context, sessionID int64) ([]training.Feedback, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT muscle_group,fatigue,pump,recovery FROM session_feedback WHERE session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?) ORDER BY muscle_group`, sessionID, s.userID)
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
func (s *userStore) LatestFeedback(ctx context.Context) (map[string]training.Feedback, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT muscle_group,fatigue,pump,recovery FROM session_feedback
WHERE id IN (SELECT MAX(f.id) FROM session_feedback f JOIN sessions se ON se.id=f.session_id WHERE se.user_id=? GROUP BY f.muscle_group)`, s.userID)
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

func (s *userStore) SetExerciseNote(ctx context.Context, n training.ExerciseNote) error {
	if strings.TrimSpace(n.Note) == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM exercise_notes WHERE user_id=? AND exercise=?`, s.userID, n.Exercise)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO exercise_notes(user_id,exercise,note,updated_at) VALUES (?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,exercise) DO UPDATE SET note=excluded.note, updated_at=CURRENT_TIMESTAMP`,
		s.userID, n.Exercise, n.Note)
	return err
}

func (s *userStore) ExerciseNotes(ctx context.Context) ([]training.ExerciseNote, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exercise,note FROM exercise_notes WHERE user_id=? ORDER BY exercise`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.ExerciseNote
	for rows.Next() {
		var n training.ExerciseNote
		if err = rows.Scan(&n.Exercise, &n.Note); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SupersetFor reports the superset label an exercise carries in a session's
// plan, so a logged set can be stamped without the caller stating it.
func (s *userStore) SupersetFor(ctx context.Context, sessionID int64, exercise string) (string, error) {
	var label string
	err := s.db.QueryRowContext(ctx, `SELECT i.superset FROM session_items i JOIN sessions se ON se.id=i.session_id WHERE i.session_id=? AND i.exercise=? AND se.user_id=?`,
		sessionID, exercise, s.userID).Scan(&label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return label, err
}

// RecentFeedback returns up to limit most recent ratings per muscle group,
// newest first, so a recommendation can look at a trend rather than a moment.
func (s *userStore) RecentFeedback(ctx context.Context, limit int) (map[string][]training.Feedback, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT f.muscle_group,f.fatigue,f.pump,f.recovery FROM session_feedback f JOIN sessions se ON se.id=f.session_id
WHERE se.user_id=? ORDER BY f.muscle_group, f.id DESC`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]training.Feedback{}
	for rows.Next() {
		var f training.Feedback
		if err = rows.Scan(&f.MuscleGroup, &f.Fatigue, &f.Pump, &f.Recovery); err != nil {
			return nil, err
		}
		if len(out[f.MuscleGroup]) < limit {
			out[f.MuscleGroup] = append(out[f.MuscleGroup], f)
		}
	}
	return out, rows.Err()
}

func (s *userStore) SetLandmarks(ctx context.Context, l training.Landmarks) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO volume_landmarks(user_id,muscle_group,mev,mrv,updated_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,muscle_group) DO UPDATE SET mev=excluded.mev, mrv=excluded.mrv, updated_at=CURRENT_TIMESTAMP`,
		s.userID, l.MuscleGroup, l.MEV, l.MRV)
	return err
}

func (s *userStore) AllLandmarks(ctx context.Context) (map[string]training.Landmarks, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT muscle_group,mev,mrv FROM volume_landmarks WHERE user_id=?`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]training.Landmarks{}
	for rows.Next() {
		var l training.Landmarks
		if err = rows.Scan(&l.MuscleGroup, &l.MEV, &l.MRV); err != nil {
			return nil, err
		}
		out[l.MuscleGroup] = l
	}
	return out, rows.Err()
}

// DeleteSession removes a session and, by ON DELETE CASCADE, all of its sets.
// It returns how many sets went with it so the caller can report the cost of an
// irreversible delete.
func (s *userStore) DeleteSession(ctx context.Context, id int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sets WHERE session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?)`, id, s.userID).Scan(&n)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=? AND user_id=?`, id, s.userID)
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

func (s *userStore) ExerciseHistory(ctx context.Context, exercise string, limit int) (training.ExerciseHistory, error) {
	out := training.ExerciseHistory{Exercise: exercise, Sets: []training.ExerciseSet{}}
	err := s.db.QueryRowContext(ctx, `SELECT muscle_group FROM exercise_groups WHERE user_id=? AND exercise=?`, s.userID, exercise).Scan(&out.MuscleGroup)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT st.id,st.session_id,se.date,st.weight_kg,st.reps,st.rpe,st.rpe_imputed,st.si,st.technique,st.superset
FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE se.user_id=? AND st.exercise=?
ORDER BY se.date DESC, st.position DESC LIMIT ?`, s.userID, exercise, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v training.ExerciseSet
		if err = rows.Scan(&v.SetID, &v.SessionID, &v.Date, &v.WeightKG, &v.Reps, &v.RPE, &v.RPEImputed, &v.SI, &v.Technique, &v.Superset); err != nil {
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
SELECT st.id,st.session_id,se.date,st.weight_kg,st.reps,st.rpe,st.rpe_imputed,st.si,st.technique,st.superset
FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE se.user_id=? AND st.exercise=? AND st.technique=''
ORDER BY st.weight_kg*(1+CAST(st.reps AS REAL)/30) DESC, se.date DESC LIMIT 1`, s.userID, exercise).
		Scan(&best.SetID, &best.SessionID, &best.Date, &best.WeightKG, &best.Reps, &best.RPE, &best.RPEImputed, &best.SI, &best.Technique, &best.Superset)
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
func (s *userStore) WeeklyVolume(ctx context.Context, f training.ListFilter) ([]training.WeeklyVolume, error) {
	q := `SELECT date(se.date, '-' || ((CAST(strftime('%w', se.date) AS INTEGER) + 6) % 7) || ' days') AS wk,
COALESCE(g.muscle_group,''),COALESCE(SUM(st.si),0),COUNT(st.id)
FROM sets st
JOIN sessions se ON se.id=st.session_id
LEFT JOIN exercise_groups g ON g.user_id=se.user_id AND g.exercise=st.exercise
WHERE se.user_id=?`
	args := []any{s.userID}
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

func (s *userStore) SetExerciseGroup(ctx context.Context, g training.ExerciseGroup) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO exercise_groups(user_id,exercise,muscle_group,updated_at) VALUES (?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,exercise) DO UPDATE SET muscle_group=excluded.muscle_group, updated_at=CURRENT_TIMESTAMP`,
		s.userID, g.Exercise, g.MuscleGroup)
	return err
}

func (s *userStore) ExerciseGroups(ctx context.Context) ([]training.ExerciseGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exercise,muscle_group FROM exercise_groups WHERE user_id=? ORDER BY muscle_group,exercise`, s.userID)
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
func (s *userStore) VolumeByGroup(ctx context.Context, f training.ListFilter) ([]training.GroupVolume, error) {
	q := `SELECT COALESCE(g.muscle_group,''),COALESCE(SUM(st.si),0),COUNT(st.id)
FROM sets st
JOIN sessions se ON se.id=st.session_id
LEFT JOIN exercise_groups g ON g.user_id=se.user_id AND g.exercise=st.exercise
WHERE se.user_id=?`
	args := []any{s.userID}
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

func (s *userStore) total(ctx context.Context, id int64) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(si),0) FROM sets WHERE session_id IN (SELECT id FROM sessions WHERE id=? AND user_id=?)`, id, s.userID).Scan(&total)
	return training.NormalizeSI(total), err
}

// LastSetAt is the most recently logged set of one of the user's sessions. The
// order is by created_at, which edits never touch, with the later id breaking a
// tie within the same second.
func (s *userStore) LastSetAt(ctx context.Context, sessionID int64) (int64, time.Time, error) {
	var id int64
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT st.id, st.created_at FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE st.session_id=? AND se.user_id=? ORDER BY st.created_at DESC, st.id DESC LIMIT 1`, sessionID, s.userID).Scan(&id, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, training.ErrNotFound
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	// CURRENT_TIMESTAMP is UTC in this exact layout.
	at, err := time.ParseInLocation("2006-01-02 15:04:05", created, time.UTC)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("set %d created_at %q: %w", id, created, err)
	}
	return id, at, nil
}

// RestTarget is the user's chosen rest between sets in seconds, 0 if never chosen.
func (s *userStore) RestTarget(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT rest_seconds FROM users WHERE id=?`, s.userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return int(v.Int64), err
}

func (s *userStore) SetRestTarget(ctx context.Context, seconds int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET rest_seconds=? WHERE id=?`, seconds, s.userID)
	return err
}

// CoachPreferences returns the person's coach model and thinking level; empty
// when never chosen.
func (s *Store) CoachPreferences(ctx context.Context, userID int64) (model, thinking string, err error) {
	var m, th sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT coach_model, coach_thinking FROM users WHERE id=?`, userID).Scan(&m, &th)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return m.String, th.String, err
}

// SetCoachPreferences keeps the person's coach model and thinking level.
func (s *Store) SetCoachPreferences(ctx context.Context, userID int64, model, thinking string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET coach_model=?, coach_thinking=? WHERE id=?`, model, thinking, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("coach preferences: no user %d", userID)
	}
	return nil
}
