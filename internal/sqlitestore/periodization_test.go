package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// Migration 030 only adds: every row stored before it reads back unchanged,
// and every existing session is unplaced (its three link columns NULL).
func TestMigration030KeepsEveryRowAndLeavesSessionsUnplaced(t *testing.T) {
	path := t.TempDir() + "/training.db"
	db := migrateTo(t, path, 29)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO plans(id,user_id,name,notes,created_at) VALUES (7,1,'Torso','',CURRENT_TIMESTAMP),(8,2,'Pierna','',CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO sessions(id,user_id,date,started_at,plan_id,plan_name) VALUES (1,1,'2026-11-10','2026-11-10 18:00:00',7,'Torso'),(2,2,'2026-11-11','2026-11-11 19:00:00',8,'Pierna'),(3,1,'2026-11-12','2026-11-12 18:30:00',NULL,'')`)
	exec(`INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at) VALUES (1,1,'press banca',80,5,8,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),(2,1,'sentadilla',100,5,8,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	snapshot := func(db *sql.DB) string {
		t.Helper()
		return dumpQuery(t, db, `SELECT id,user_id,date,started_at,plan_id,plan_name FROM sessions ORDER BY id`) +
			dumpQuery(t, db, `SELECT * FROM sets ORDER BY id`) +
			dumpQuery(t, db, `SELECT * FROM plans ORDER BY id`)
	}
	before := snapshot(db)
	db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if after := snapshot(store.db); after != before {
		t.Fatalf("rows changed:\nbefore %s\nafter  %s", before, after)
	}
	if got := dumpQuery(t, store.db, `SELECT id FROM sessions WHERE block_id IS NOT NULL OR block_microcycle_id IS NOT NULL OR block_off_plan IS NOT NULL`); got != "" {
		t.Fatalf("sessions placed by the migration: %s", got)
	}
	for _, table := range []string{"goals", "blocks", "block_microcycles", "block_rotations"} {
		if got := dumpQuery(t, store.db, `SELECT COUNT(*) FROM `+table); got != "[]interface {}{0}\n" {
			t.Fatalf("%s rows = %s", table, got)
		}
	}
}

// migrateExcept builds a database with every migration applied except skip,
// as a deployment that ran later migrations before this one landed.
func migrateExcept(t *testing.T, path string, skip int) *sql.DB {
	t.Helper()
	db := migrateTo(t, path, skip-1)
	names := mustMigrationNames()
	versions, err := migrationVersions(names)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		if versions[i] <= skip {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, versions[i]); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Migration 030 can land after 031 and 032 were applied: Open applies it to
// that database and every row stored before reads back unchanged.
func TestMigration030AppliesAfterLaterMigrationsKeepingEveryRow(t *testing.T) {
	path := t.TempDir() + "/training.db"
	db := migrateExcept(t, path, 30)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO plans(id,user_id,name,notes,created_at) VALUES (7,1,'Torso','',CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO sessions(id,user_id,date,started_at,plan_id,plan_name) VALUES (1,1,'2026-11-10','2026-11-10 18:00:00',7,'Torso')`)
	exec(`INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at) VALUES (1,1,'press banca',80,5,8,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO bodyweight(user_id,date,weight_kg,note,created_at,updated_at) VALUES (1,'2026-11-10',80.5,NULL,'2026-11-10','2026-11-10')`)
	snapshot := func(db *sql.DB) string {
		t.Helper()
		return dumpQuery(t, db, `SELECT id,user_id,date,started_at,plan_id,plan_name FROM sessions ORDER BY id`) +
			dumpQuery(t, db, `SELECT * FROM sets ORDER BY id`) +
			dumpQuery(t, db, `SELECT * FROM plans ORDER BY id`) +
			dumpQuery(t, db, `SELECT * FROM bodyweight ORDER BY rowid`)
	}
	before := snapshot(db)
	if got := dumpQuery(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version=30`); got != "[]interface {}{0}\n" {
		t.Fatalf("030 already applied: %s", got)
	}
	db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if after := snapshot(store.db); after != before {
		t.Fatalf("rows changed:\nbefore %s\nafter  %s", before, after)
	}
	if got := dumpQuery(t, store.db, `SELECT COUNT(*) FROM schema_migrations WHERE version=30`); got != "[]interface {}{1}\n" {
		t.Fatalf("030 not recorded: %s", got)
	}
	if got := dumpQuery(t, store.db, `SELECT id FROM sessions WHERE block_id IS NOT NULL OR block_microcycle_id IS NOT NULL OR block_off_plan IS NOT NULL`); got != "" {
		t.Fatalf("sessions placed by the migration: %s", got)
	}
	for _, table := range []string{"goals", "blocks", "block_microcycles", "block_rotations"} {
		if got := dumpQuery(t, store.db, `SELECT COUNT(*) FROM `+table); got != "[]interface {}{0}\n" {
			t.Fatalf("%s rows = %s", table, got)
		}
	}
}

// periodWorld is one account on a real store with a clock the test moves.
type periodWorld struct {
	t     *testing.T
	store *Store
	svc   *training.Service
	now   *time.Time
	plans map[string]int64
}

func newPeriodWorld(t *testing.T) *periodWorld {
	t.Helper()
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC)
	w := &periodWorld{t: t, store: store, now: &now, plans: map[string]int64{}}
	w.svc = training.NewService(store.ForUser(1), func() time.Time { return *w.now })
	for _, name := range []string{"torso", "pierna", "full"} {
		p, err := w.svc.CreatePlan(context.Background(), training.Plan{Name: name, Items: []training.PlanItem{{Exercise: "press banca", TargetSets: 3}}})
		if err != nil {
			t.Fatal(err)
		}
		w.plans[name] = p.ID
	}
	return w
}

func (w *periodWorld) day(date string) {
	w.t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		w.t.Fatal(err)
	}
	*w.now = d.Add(18 * time.Hour)
}

func (w *periodWorld) rotation(names ...string) *[]int64 {
	ids := make([]int64, len(names))
	for i, n := range names {
		ids[i] = w.plans[n]
	}
	return &ids
}

func (w *periodWorld) block(in training.BlockInput) training.BlockView {
	w.t.Helper()
	b, err := w.svc.SaveBlock(context.Background(), in)
	if err != nil {
		w.t.Fatalf("save block %+v: %v", in, err)
	}
	return b
}

func (w *periodWorld) advance(id int64, action string) training.BlockView {
	w.t.Helper()
	b, err := w.svc.AdvanceBlock(context.Background(), training.AdvanceInput{BlockID: id, Action: action})
	if err != nil {
		w.t.Fatalf("%s: %v", action, err)
	}
	return b
}

// start begins a session of a routine on a date ("" today) and returns where it
// was placed.
func (w *periodWorld) start(date, plan string) training.SessionLink {
	w.t.Helper()
	s, err := w.svc.StartPlannedSession(context.Background(), date, w.plans[plan])
	if err != nil {
		w.t.Fatal(err)
	}
	return w.link(s.ID)
}

func (w *periodWorld) link(session int64) training.SessionLink {
	w.t.Helper()
	l, err := w.store.ForUser(1).(training.PeriodizationStore).SessionLink(context.Background(), session)
	if err != nil {
		w.t.Fatal(err)
	}
	return l
}

func (w *periodWorld) get(id int64) training.BlockView {
	w.t.Helper()
	b, err := w.svc.GetBlock(context.Background(), id)
	if err != nil {
		w.t.Fatal(err)
	}
	return b
}

// failPlacement makes every write of a session's block link fail, as a broken
// periodization store would.
func failPlacement(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TRIGGER fail_placement BEFORE UPDATE OF block_id, block_microcycle_id, block_off_plan ON sessions
		BEGIN SELECT RAISE(ABORT, 'placement unavailable'); END`); err != nil {
		t.Fatal(err)
	}
}

func (w *periodWorld) sets(session int64) int {
	w.t.Helper()
	var n int
	if err := w.store.db.QueryRow(`SELECT COUNT(*) FROM sets WHERE session_id=?`, session).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// Placement is best effort: when it fails, every requested set is stored, the
// write reports success and the session stays unplaced. Periodization never
// fails or loses a logged set.
func TestAPlacementFailureNeverFailsOrLosesAWrite(t *testing.T) {
	ctx := context.Background()
	w := newPeriodWorld(t)
	b := w.block(training.BlockInput{Intent: strPtr("volumen"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso", "pierna")})
	w.day("2026-11-16")
	today, err := w.svc.StartPlannedSession(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	w.advance(b.ID, training.BlockActivate)
	failPlacement(t, w.store.db)
	before := states(w.get(b.ID))

	// log_set into today's unplaced session.
	n := w.sets(today.ID)
	if _, out, err := w.svc.LogSet(ctx, "press banca", 80, 5, 8, 3, "", "", ""); err != nil || len(out) != 3 || w.sets(today.ID)-n != 3 {
		t.Errorf("log_set into an unplaced session: %d sets returned, %d stored, err %v", len(out), w.sets(today.ID)-n, err)
	}
	// add_set.
	n = w.sets(today.ID)
	if _, _, err := w.svc.AddSet(ctx, training.AddSetInput{SessionID: today.ID, Exercise: "press banca", WeightKG: 80, Reps: 5, RPE: 8}); err != nil || w.sets(today.ID)-n != 1 {
		t.Errorf("add_set: %d stored, err %v", w.sets(today.ID)-n, err)
	}
	// log_sets.
	n = w.sets(today.ID)
	logged, err := w.svc.LogSets(ctx, training.LogSetsInput{Key: "k1", Exercise: "remo", Sets: []training.LoggedSet{{WeightKG: 60, Reps: 8, RPE: 8}, {WeightKG: 60, Reps: 8, RPE: 8}}})
	if err != nil || len(logged.Sets) != 2 || w.sets(today.ID)-n != 2 {
		t.Errorf("log_sets: %d stored, err %v", w.sets(today.ID)-n, err)
	}
	if l := w.link(today.ID); l.Placed {
		t.Errorf("today's session placed despite the failure: %+v", l)
	}
	// log_set on a new day creates the session and stores every set.
	w.day("2026-11-17")
	session, out, err := w.svc.LogSet(ctx, "sentadilla", 100, 5, 8, 3, "", "", "")
	if err != nil || len(out) != 3 || session.ID == 0 || w.sets(session.ID) != 3 {
		t.Errorf("log_set on a new day: %d sets returned, session %d, err %v", len(out), session.ID, err)
	}
	if session.ID != 0 && w.link(session.ID).Placed {
		t.Errorf("new session placed despite the failure: %+v", w.link(session.ID))
	}
	// start_session.
	started, err := w.svc.StartPlannedSession(ctx, "2026-11-18", w.plans["torso"])
	if err != nil || started.ID == 0 || w.link(started.ID).Placed {
		t.Errorf("start_session: %+v, %v", started, err)
	}
	if after := states(w.get(b.ID)); after != before {
		t.Errorf("block moved: %s, want %s", after, before)
	}
}

// states reads each microcycle as position:state:sessions.
func states(b training.BlockView) string {
	out := make([]string, len(b.Microcycles))
	for i, m := range b.Microcycles {
		out[i] = fmt.Sprintf("%d:%s:%d", m.Position, m.State, m.SessionsDone)
	}
	return strings.Join(out, " ")
}

func strPtr(v string) *string   { return &v }
func i64Ptr(v int64) *int64     { return &v }
func f64Ptr(v float64) *float64 { return &v }

// A sketch needs only an intent and a length; everything else has a default
// and nothing is placed until the block is active.
func TestSketchBlockNeedsOnlyIntentAndLength(t *testing.T) {
	w := newPeriodWorld(t)
	ctx := context.Background()
	for _, bad := range []training.BlockInput{
		{MicrocycleCount: intPtr(3)},
		{Intent: strPtr("  "), MicrocycleCount: intPtr(3)},
		{Intent: strPtr("fuerza")},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(0)},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(training.MaxMicrocycles + 1)},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(3), Advance: strPtr("weekly")},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(3), MicrocycleDays: intPtr(0)},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(3), Status: strPtr(training.BlockActive)},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(3), Microcycles: []training.MicrocycleInput{{Position: 4, Label: strPtr("x")}}},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(3), Microcycles: []training.MicrocycleInput{{Position: 1, SetsDelta: intPtr(training.MaxSetsDelta + 1)}}},
		{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(3), Microcycles: []training.MicrocycleInput{{Position: 1, Rotation: w.rotation("torso"), InheritRotation: true}}},
	} {
		if _, err := w.svc.SaveBlock(ctx, bad); !errors.Is(err, training.ErrValidation) {
			t.Errorf("SaveBlock(%+v) = %v, want validation", bad, err)
		}
	}
	if _, err := w.svc.SaveBlock(ctx, training.BlockInput{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(2), Rotation: &[]int64{missingID}}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("missing routine = %v", err)
	}
	if got := dumpQuery(t, w.store.db, `SELECT COUNT(*) FROM blocks`); got != "[]interface {}{0}\n" {
		t.Fatalf("a rejected block was stored: %s", got)
	}
	b := w.block(training.BlockInput{Intent: strPtr("  bloque de fuerza  "), MicrocycleCount: intPtr(3)})
	if b.Intent != "bloque de fuerza" || b.Advance != training.AdvanceSequence || b.MicrocycleDays != 7 || b.Status != training.BlockSketch || b.MicrocycleCount != 3 || len(b.Rotation) != 0 {
		t.Fatalf("sketch = %+v", b)
	}
	if states(b) != "1:upcoming:0 2:upcoming:0 3:upcoming:0" {
		t.Fatalf("states = %s", states(b))
	}
	if l := w.start("", "torso"); l.Placed || l.BlockID != 0 {
		t.Fatalf("a session was placed in a sketch: %+v", l)
	}
}

// Sequence: each microcycle takes one session per routine of its rotation and
// moves on only once they are all done or it is closed or skipped; a missed
// week breaks nothing, and a session that fits nowhere is kept off-plan.
func TestSequenceBlockAdvancesBySessionsDoneOrSkipped(t *testing.T) {
	w := newPeriodWorld(t)
	b := w.block(training.BlockInput{Intent: strPtr("hipertrofia"), MicrocycleCount: intPtr(3), Rotation: w.rotation("torso", "pierna")})
	b = w.advance(b.ID, training.BlockActivate)
	if b.Status != training.BlockActive || b.StartDate != "2026-11-16" {
		t.Fatalf("active = %+v", b)
	}
	if l := w.start("2026-11-10", "torso"); !l.OffPlan || l.BlockID != b.ID || l.MicrocycleID != 0 {
		t.Fatalf("a session before the start = %+v, want off-plan", l)
	}
	first := w.start("", "torso")
	if first.OffPlan || first.MicrocycleID != b.Microcycles[0].ID || first.BlockID != b.ID {
		t.Fatalf("first = %+v", first)
	}
	if got := w.get(b.ID); states(got) != "1:current:1 2:upcoming:0 3:upcoming:0" || len(got.Microcycles[0].Due) != 1 || got.Microcycles[0].Due[0].PlanName != "pierna" {
		t.Fatalf("after one session: %s due %+v", states(got), got.Microcycles[0].Due)
	}
	// Two weeks without training change nothing: the next session still
	// completes the first microcycle.
	w.day("2026-12-01")
	if l := w.start("", "pierna"); l.MicrocycleID != b.Microcycles[0].ID {
		t.Fatalf("second = %+v", l)
	}
	if l := w.start("", "torso"); l.MicrocycleID != b.Microcycles[1].ID {
		t.Fatalf("third = %+v, want the second microcycle", l)
	}
	got := w.get(b.ID)
	if states(got) != "1:done:2 2:current:1 3:upcoming:0" || got.Microcycles[0].ClosedOn != "2026-12-01" {
		t.Fatalf("after the first microcycle: %s %+v", states(got), got.Microcycles[0])
	}
	w.advance(b.ID, training.BlockSkipCurrent)
	if l := w.start("", "full"); l.MicrocycleID != b.Microcycles[2].ID {
		t.Fatalf("after a skip = %+v", l)
	}
	got = w.advance(b.ID, training.BlockCompleteCurrent)
	if states(got) != "1:done:2 2:skipped:1 3:done:1" {
		t.Fatalf("states = %s", states(got))
	}
	if _, err := w.svc.AdvanceBlock(context.Background(), training.AdvanceInput{BlockID: b.ID, Action: training.BlockCompleteCurrent}); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("completing with nothing left = %v", err)
	}
	// Every microcycle is closed, yet the block stays active until it is closed.
	extra := w.start("", "torso")
	if !extra.OffPlan || extra.BlockID != b.ID {
		t.Fatalf("a session past the last microcycle = %+v", extra)
	}
	if got = w.get(b.ID); got.Status != training.BlockActive || len(got.OffPlan) != 2 {
		t.Fatalf("block = %s, off-plan %d", got.Status, len(got.OffPlan))
	}
	got = w.advance(b.ID, training.BlockClose)
	if got.Status != training.BlockDone || got.EndedOn != "2026-12-01" {
		t.Fatalf("closed = %+v", got)
	}
	if l := w.start("", "torso"); l.Placed {
		t.Fatalf("a session after the block closed was placed: %+v", l)
	}
}

// Calendar: the microcycle is the one whose dates cover the session; outside
// them the session is off-plan; the block never closes by itself, and it does
// not take explicit microcycle advances.
func TestCalendarBlockPlacesSessionsByDate(t *testing.T) {
	w := newPeriodWorld(t)
	b := w.block(training.BlockInput{Intent: strPtr("fuerza"), Advance: strPtr(training.AdvanceCalendar), MicrocycleCount: intPtr(2), MicrocycleDays: intPtr(7), Rotation: w.rotation("full")})
	b, err := w.svc.AdvanceBlock(context.Background(), training.AdvanceInput{BlockID: b.ID, Action: training.BlockActivate, StartDate: "2026-11-16"})
	if err != nil {
		t.Fatal(err)
	}
	if b.EndDate != "2026-11-29" || b.Microcycles[1].StartDate != "2026-11-23" || b.Microcycles[1].EndDate != "2026-11-29" {
		t.Fatalf("dates = %+v", b)
	}
	cases := []struct {
		date       string
		microcycle int
	}{
		{"2026-11-15", 0}, {"2026-11-16", 1}, {"2026-11-16", 1}, {"2026-11-22", 1}, {"2026-11-23", 2}, {"2026-11-29", 2}, {"2026-11-30", 0},
	}
	for _, c := range cases {
		l := w.start(c.date, "full")
		if c.microcycle == 0 {
			if !l.OffPlan || l.BlockID != b.ID {
				t.Errorf("%s = %+v, want off-plan", c.date, l)
			}
			continue
		}
		if l.OffPlan || l.MicrocycleID != b.Microcycles[c.microcycle-1].ID {
			t.Errorf("%s = %+v, want microcycle %d", c.date, l, c.microcycle)
		}
	}
	for _, action := range []string{training.BlockCompleteCurrent, training.BlockSkipCurrent} {
		if _, err := w.svc.AdvanceBlock(context.Background(), training.AdvanceInput{BlockID: b.ID, Action: action}); !errors.Is(err, training.ErrValidation) {
			t.Fatalf("%s on a calendar block = %v", action, err)
		}
	}
	w.day("2027-01-10")
	got := w.get(b.ID)
	if got.Status != training.BlockActive || states(got) != "1:elapsed:3 2:elapsed:2" {
		t.Fatalf("long after its dates: %s %s", got.Status, states(got))
	}
}

// The mode can change mid-block: to calendar, the first pending microcycle
// starts today; back to sequence, the microcycles whose dates passed are done.
func TestAdvanceModeChangesMidBlock(t *testing.T) {
	w := newPeriodWorld(t)
	ctx := context.Background()
	b := w.block(training.BlockInput{Intent: strPtr("mixto"), MicrocycleCount: intPtr(5), Rotation: w.rotation("full")})
	w.advance(b.ID, training.BlockActivate)
	w.start("", "full")
	w.day("2026-11-20")
	b = w.block(training.BlockInput{ID: b.ID, Advance: strPtr(training.AdvanceCalendar)})
	if b.Advance != training.AdvanceCalendar || b.StartDate != "2026-11-13" || b.Microcycles[1].StartDate != "2026-11-20" {
		t.Fatalf("to calendar: %+v", b)
	}
	if states(b) != "1:done:1 2:current:0 3:upcoming:0 4:upcoming:0 5:upcoming:0" {
		t.Fatalf("states = %s", states(b))
	}
	if l := w.start("", "full"); l.MicrocycleID != b.Microcycles[1].ID {
		t.Fatalf("today = %+v", l)
	}
	w.day("2026-11-28")
	if l := w.start("", "full"); l.MicrocycleID != b.Microcycles[2].ID {
		t.Fatalf("a week later = %+v", l)
	}
	b = w.block(training.BlockInput{ID: b.ID, Advance: strPtr(training.AdvanceSequence)})
	if states(b) != "1:done:1 2:done:1 3:current:1 4:upcoming:0 5:upcoming:0" || b.Microcycles[1].ClosedOn != "2026-11-26" {
		t.Fatalf("back to sequence: %s %+v", states(b), b.Microcycles[1])
	}
	// The third microcycle already holds its one session: the next goes on.
	if l := w.start("", "full"); l.MicrocycleID != b.Microcycles[3].ID {
		t.Fatalf("sequence again = %+v", l)
	}
	// A calendar block keeps today's microcycle when its length changes.
	b = w.block(training.BlockInput{ID: b.ID, Advance: strPtr(training.AdvanceCalendar), MicrocycleDays: intPtr(4)})
	if b.Microcycles[4].StartDate != "2026-11-28" || b.Microcycles[4].EndDate != "2026-12-01" {
		t.Fatalf("calendar, 4 days: %+v", b.Microcycles[4])
	}
	b = w.block(training.BlockInput{ID: b.ID, MicrocycleDays: intPtr(10)})
	if b.Microcycles[4].StartDate != "2026-11-28" || b.Microcycles[4].EndDate != "2026-12-07" {
		t.Fatalf("calendar, 10 days: %+v", b.Microcycles[4])
	}
	if _, err := w.svc.SaveBlock(ctx, training.BlockInput{ID: b.ID, Status: strPtr(training.BlockPlanned)}); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("setting the status of an active block = %v", err)
	}
}

// Edits are forward only: done microcycles and their sessions keep what was
// done, a rotation change freezes the old one into them, and shrinking drops
// only remaining microcycles with nothing placed in them.
func TestBlockEditsTouchOnlyRemainingMicrocycles(t *testing.T) {
	w := newPeriodWorld(t)
	ctx := context.Background()
	b := w.block(training.BlockInput{Intent: strPtr("volumen"), MicrocycleCount: intPtr(4), Rotation: w.rotation("torso", "pierna")})
	w.advance(b.ID, training.BlockActivate)
	first := w.start("", "torso")
	w.start("", "pierna")
	second := w.start("", "torso")
	b = w.block(training.BlockInput{ID: b.ID, Rotation: w.rotation("pierna", "full"),
		Microcycles: []training.MicrocycleInput{{Position: 4, Label: strPtr("descarga"), SetsDelta: intPtr(-2), EffortDelta: f64Ptr(-1.5)}}})
	m := b.Microcycles
	if !m[0].OwnRotation || len(m[0].Rotation) != 2 || m[0].Rotation[0].PlanName != "torso" {
		t.Fatalf("the done microcycle lost its rotation: %+v", m[0])
	}
	if m[1].OwnRotation || len(m[1].Rotation) != 2 || m[3].Label != "descarga" || m[3].SetsDelta != -2 || m[3].EffortDelta != -1.5 {
		t.Fatalf("remaining microcycles = %+v", m)
	}
	if l := w.link(first.SessionID); l.MicrocycleID != m[0].ID {
		t.Fatalf("a done session moved: %+v", l)
	}
	if l := w.link(second.SessionID); l.MicrocycleID != m[1].ID {
		t.Fatalf("the current session moved: %+v", l)
	}
	for _, bad := range []training.BlockInput{
		{ID: b.ID, Microcycles: []training.MicrocycleInput{{Position: 1, Label: strPtr("reescrito")}}},
		{ID: b.ID, MicrocycleCount: intPtr(1)},
		{ID: b.ID, MicrocycleCount: intPtr(2), Microcycles: []training.MicrocycleInput{{Position: 3, Label: strPtr("x")}}},
	} {
		if _, err := w.svc.SaveBlock(ctx, bad); !errors.Is(err, training.ErrValidation) {
			t.Errorf("SaveBlock(%+v) = %v, want validation", bad, err)
		}
	}
	b = w.block(training.BlockInput{ID: b.ID, MicrocycleCount: intPtr(3), Microcycles: []training.MicrocycleInput{{Position: 3, Rotation: &[]int64{}}}})
	if b.MicrocycleCount != 3 || !b.Microcycles[2].OwnRotation || len(b.Microcycles[2].Rotation) != 0 {
		t.Fatalf("shrunk = %+v", b)
	}
	b = w.block(training.BlockInput{ID: b.ID, MicrocycleCount: intPtr(5), Microcycles: []training.MicrocycleInput{{Position: 3, InheritRotation: true}}})
	if b.MicrocycleCount != 5 || b.Microcycles[2].OwnRotation || len(b.Microcycles[4].Rotation) != 2 {
		t.Fatalf("grown = %+v", b)
	}
	w.advance(b.ID, training.BlockClose)
	if _, err := w.svc.SaveBlock(ctx, training.BlockInput{ID: b.ID, Intent: strPtr("otro")}); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("editing a done block = %v", err)
	}
}

// The link is editable, a placement elsewhere never rejects a session, and
// another account's session, block or microcycle reads as missing.
func TestAssignSessionBlockMovesTheLink(t *testing.T) {
	w := newPeriodWorld(t)
	ctx := context.Background()
	b := w.block(training.BlockInput{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso")})
	w.advance(b.ID, training.BlockActivate)
	l := w.start("2026-11-01", "torso")
	if !l.OffPlan {
		t.Fatalf("before start = %+v", l)
	}
	got, err := w.svc.AssignSessionBlock(ctx, training.AssignInput{SessionID: l.SessionID, MicrocycleID: b.Microcycles[1].ID})
	if err != nil || got.MicrocycleID != b.Microcycles[1].ID || got.OffPlan || got.BlockID != b.ID {
		t.Fatalf("assign = %+v, %v", got, err)
	}
	got, err = w.svc.AssignSessionBlock(ctx, training.AssignInput{SessionID: l.SessionID, OffPlan: true})
	if err != nil || !got.OffPlan || got.MicrocycleID != 0 || got.BlockID != b.ID {
		t.Fatalf("off plan = %+v, %v", got, err)
	}
	for _, bad := range []training.AssignInput{{SessionID: l.SessionID}, {SessionID: l.SessionID, MicrocycleID: b.Microcycles[0].ID, OffPlan: true}, {MicrocycleID: b.Microcycles[0].ID}} {
		if _, err := w.svc.AssignSessionBlock(ctx, bad); !errors.Is(err, training.ErrValidation) {
			t.Errorf("assign %+v = %v", bad, err)
		}
	}
	if _, err := w.store.db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	other := training.NewService(w.store.ForUser(2), func() time.Time { return *w.now })
	foreign, err := other.StartSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []training.AssignInput{{SessionID: foreign.ID, OffPlan: true}, {SessionID: missingID, OffPlan: true}, {SessionID: l.SessionID, MicrocycleID: missingID}} {
		if _, err := w.svc.AssignSessionBlock(ctx, bad); !errors.Is(err, training.ErrNotFound) {
			t.Errorf("assign %+v = %v, want not found", bad, err)
		}
	}
	if _, err := other.AssignSessionBlock(ctx, training.AssignInput{SessionID: foreign.ID, MicrocycleID: b.Microcycles[0].ID}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("assigning to another account's microcycle = %v", err)
	}
	if _, err := other.GetBlock(ctx, b.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("reading another account's block = %v", err)
	}
}

func TestGoalsAreFreeTextWithAnOptionalDate(t *testing.T) {
	w := newPeriodWorld(t)
	ctx := context.Background()
	for _, bad := range []training.GoalInput{
		{},
		{Text: strPtr(" ")},
		{Text: strPtr(strings.Repeat("a", 501))},
		{Text: strPtr("100 kg"), TargetDate: strPtr("mañana")},
		{Text: strPtr("100 kg"), Status: strPtr("maybe")},
	} {
		if _, err := w.svc.SetGoal(ctx, bad); !errors.Is(err, training.ErrValidation) {
			t.Errorf("SetGoal(%+v) = %v", bad, err)
		}
	}
	g, err := w.svc.SetGoal(ctx, training.GoalInput{Text: strPtr(" banca 100 kg "), TargetDate: strPtr("2027-06-01")})
	if err != nil || g.Text != "banca 100 kg" || g.Status != training.GoalActive || g.TargetDate != "2027-06-01" {
		t.Fatalf("goal = %+v, %v", g, err)
	}
	g, err = w.svc.SetGoal(ctx, training.GoalInput{ID: g.ID, TargetDate: strPtr(""), Status: strPtr(training.GoalAchieved)})
	if err != nil || g.Text != "banca 100 kg" || g.Status != training.GoalAchieved || g.TargetDate != "" {
		t.Fatalf("edited = %+v, %v", g, err)
	}
	if _, err = w.svc.SetGoal(ctx, training.GoalInput{ID: missingID, Text: strPtr("x")}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("missing goal = %v", err)
	}
	if _, err = w.svc.SaveBlock(ctx, training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(1), GoalID: i64Ptr(missingID)}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("block with a missing goal = %v", err)
	}
	b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(1), GoalID: &g.ID})
	if b.GoalID != g.ID {
		t.Fatalf("goal link = %+v", b)
	}
	list, err := w.svc.ListBlocks(ctx)
	if err != nil || len(list.Goals) != 1 || len(list.Blocks) != 1 || list.Blocks[0].GoalID != g.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

// training_status shows the active block and the microcycle the next session
// counts in, with nothing applied to the prescription.
func TestTrainingStatusShowsTheCurrentMicrocycle(t *testing.T) {
	w := newPeriodWorld(t)
	ctx := context.Background()
	st, err := w.svc.TrainingStatus(ctx)
	if err != nil || st.Today != "2026-11-16" || st.Block != nil || st.Current != nil {
		t.Fatalf("empty status = %+v, %v", st, err)
	}
	if _, err = w.svc.SetGoal(ctx, training.GoalInput{Text: strPtr("sentadilla 150")}); err != nil {
		t.Fatal(err)
	}
	b := w.block(training.BlockInput{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso", "pierna"),
		Microcycles: []training.MicrocycleInput{{Position: 1, Label: strPtr("introducción"), SetsDelta: intPtr(-1)}}})
	w.advance(b.ID, training.BlockActivate)
	w.start("", "pierna")
	st, err = w.svc.TrainingStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Goals) != 1 || st.Block == nil || st.Block.ID != b.ID || st.Block.CurrentPosition != 1 || st.Current == nil {
		t.Fatalf("status = %+v", st)
	}
	c := st.Current
	if c.Label != "introducción" || c.SetsDelta != -1 || c.SessionsDone != 1 || c.SessionsPlanned != 2 || len(c.Due) != 1 || c.Due[0].PlanName != "torso" {
		t.Fatalf("current = %+v", c)
	}
}

var periodTimestamps = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T[^"]*"`)

// Without a block, or with one that is not active, training works exactly as
// before periodization: the same results, the same rows, every session
// unplaced. The reference is the same service on a store whose periodization
// port is hidden, which is how the code ran before it existed.
func TestTrainingWithoutAnActiveBlockBehavesAsBefore(t *testing.T) {
	run := func(withPort bool) (string, string) {
		store, err := Open(t.TempDir() + "/training.db")
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		clock := func() time.Time { return time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC) }
		var user training.UserStore = store.ForUser(1)
		if !withPort {
			user = struct{ training.UserStore }{user}
		}
		svc := training.NewService(user, clock)
		ctx := context.Background()
		var results []any
		record := func(v any, err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, v)
		}
		plan, err := svc.CreatePlan(ctx, training.Plan{Name: "torso", Items: []training.PlanItem{{Exercise: "press banca", TargetSets: 3}}})
		record(plan, err)
		if withPort {
			// A block that is not active places nothing.
			_, err := svc.SaveBlock(ctx, training.BlockInput{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(2), Rotation: &[]int64{plan.ID}, Status: strPtr(training.BlockPlanned)})
			if err != nil {
				t.Fatal(err)
			}
		}
		session, err := svc.StartPlannedSession(ctx, "", plan.ID)
		record(session, err)
		set, si, err := svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "press banca", WeightKG: 80, Reps: 5, RPE: 8})
		record(set, err)
		record(si, nil)
		logged, err := svc.LogSets(ctx, training.LogSetsInput{Key: "k1", Exercise: "remo", Sets: []training.LoggedSet{{WeightKG: 60, Reps: 10, RPE: 8}}})
		record(logged, err)
		free, err := svc.StartSession(ctx, "2026-11-17")
		record(free, err)
		s2, sets, err := svc.LogSet(ctx, "curl", 12, 12, 9, 2, "", "", "")
		record(s2, err)
		record(sets, nil)
		got, err := svc.GetSession(ctx, session.ID)
		record(got, err)
		out, err := json.Marshal(results)
		if err != nil {
			t.Fatal(err)
		}
		rows := dumpQuery(t, store.db, `SELECT id,user_id,date,plan_id,plan_name,block_id,block_microcycle_id,block_off_plan FROM sessions ORDER BY id`) +
			dumpQuery(t, store.db, `SELECT id,session_id,position,exercise,weight_kg,reps,rpe,si FROM sets ORDER BY id`)
		if placed := dumpQuery(t, store.db, `SELECT id FROM sessions WHERE block_id IS NOT NULL OR block_microcycle_id IS NOT NULL OR block_off_plan IS NOT NULL`); placed != "" {
			t.Fatalf("sessions placed without an active block: %s", placed)
		}
		return periodTimestamps.ReplaceAllString(string(out), "T"), rows
	}
	beforeOut, beforeRows := run(false)
	afterOut, afterRows := run(true)
	if beforeOut != afterOut {
		t.Fatalf("results differ:\nbefore %s\nafter  %s", beforeOut, afterOut)
	}
	if beforeRows != afterRows {
		t.Fatalf("rows differ:\nbefore %s\nafter  %s", beforeRows, afterRows)
	}
}

// Every method of the periodization port, called by one account with the
// other account's ids, reads as missing and changes nothing of theirs.
func TestEveryPeriodizationMethodIsolatesAccounts(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	clock := func() time.Time { return time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC) }
	owner, other := training.NewService(store.ForUser(1), clock), training.NewService(store.ForUser(2), clock)
	plan, err := other.CreatePlan(ctx, training.Plan{Name: "torso", Items: []training.PlanItem{{Exercise: "remo", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	goal, err := other.SetGoal(ctx, training.GoalInput{Text: strPtr("dominadas")})
	if err != nil {
		t.Fatal(err)
	}
	block, err := other.SaveBlock(ctx, training.BlockInput{Intent: strPtr("fuerza"), MicrocycleCount: intPtr(2), GoalID: &goal.ID, Rotation: &[]int64{plan.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.AdvanceBlock(ctx, training.AdvanceInput{BlockID: block.ID, Action: training.BlockActivate}); err != nil {
		t.Fatal(err)
	}
	session, err := other.StartPlannedSession(ctx, "", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	own, err := owner.StartSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	microcycle := block.Microcycles[0].ID
	before := dumpUser(t, store, 2)
	port := store.ForUser(1).(training.PeriodizationStore)
	notFound := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, training.ErrNotFound) {
			t.Errorf("%s = %v, want not found", name, err)
		}
	}
	cases := map[string]func(){
		"SaveGoal": func() {
			_, err := port.SaveGoal(ctx, training.Goal{ID: goal.ID, Text: "robado", Status: training.GoalActive})
			notFound("SaveGoal", err)
		},
		"GetGoal": func() { _, err := port.GetGoal(ctx, goal.ID); notFound("GetGoal", err) },
		"Goals": func() {
			if g, err := port.Goals(ctx); err != nil || len(g) != 0 {
				t.Errorf("Goals = %+v, %v", g, err)
			}
		},
		"SaveBlock": func() {
			_, err := port.SaveBlock(ctx, training.Block{ID: block.ID, Intent: "robado", Advance: training.AdvanceSequence, MicrocycleDays: 7, Status: training.BlockSketch})
			notFound("SaveBlock", err)
			_, err = port.SaveBlock(ctx, training.Block{Intent: "x", Advance: training.AdvanceSequence, MicrocycleDays: 7, Status: training.BlockSketch, GoalID: goal.ID})
			notFound("SaveBlock with their goal", err)
			_, err = port.SaveBlock(ctx, training.Block{Intent: "x", Advance: training.AdvanceSequence, MicrocycleDays: 7, Status: training.BlockSketch, Rotation: []training.RotationEntry{{PlanID: plan.ID, PlanName: "torso"}}})
			notFound("SaveBlock with their routine", err)
		},
		"GetBlock": func() { _, err := port.GetBlock(ctx, block.ID); notFound("GetBlock", err) },
		"Blocks": func() {
			if b, err := port.Blocks(ctx); err != nil || len(b) != 0 {
				t.Errorf("Blocks = %+v, %v", b, err)
			}
		},
		"ActiveBlock": func() {
			if _, active, err := port.ActiveBlock(ctx); err != nil || active {
				t.Errorf("ActiveBlock = %v, %v", active, err)
			}
		},
		"BlockOfMicrocycle": func() { _, err := port.BlockOfMicrocycle(ctx, microcycle); notFound("BlockOfMicrocycle", err) },
		"SessionLink":       func() { _, err := port.SessionLink(ctx, session.ID); notFound("SessionLink", err) },
		"BlockSessions": func() {
			if l, err := port.BlockSessions(ctx, block.ID); err != nil || len(l) != 0 {
				t.Errorf("BlockSessions = %+v, %v", l, err)
			}
		},
		"LinkSession": func() {
			notFound("LinkSession of their session", port.LinkSession(ctx, training.SessionLink{SessionID: session.ID, OffPlan: true}, nil))
			notFound("LinkSession into their microcycle", port.LinkSession(ctx, training.SessionLink{SessionID: own.ID, BlockID: block.ID, MicrocycleID: microcycle}, nil))
			notFound("LinkSession into their block", port.LinkSession(ctx, training.SessionLink{SessionID: own.ID, BlockID: block.ID, OffPlan: true}, nil))
		},
	}
	iface := reflect.TypeFor[training.PeriodizationStore]()
	for i := range iface.NumMethod() {
		name := iface.Method(i).Name
		c, ok := cases[name]
		if !ok {
			t.Errorf("%s has no two-user case", name)
			continue
		}
		c()
	}
	if after := dumpUser(t, store, 2); after != before {
		t.Fatalf("the other account's rows changed:\nbefore %s\nafter  %s", before, after)
	}
	if l, err := port.SessionLink(ctx, own.ID); err != nil || l.Placed {
		t.Fatalf("own session = %+v, %v", l, err)
	}
}
