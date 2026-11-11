package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// warmupFixture is a planned session with two working squat sets and one
// warm-up set logged last and heavier than both, so any computation that
// counted it would read a different number: more sets, more SI, a heavier best
// set, a heavier last load.
type warmupFixture struct {
	svc     *training.Service
	session training.Session
	working []training.Set
	warmup  training.Set
}

func newWarmupFixture(t *testing.T, store *Store, userID int64) warmupFixture {
	t.Helper()
	ctx := context.Background()
	clock := func() time.Time { return time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC) }
	svc := training.NewService(store.ForUser(userID), clock)
	if err := svc.SetExerciseGroup(ctx, "squat", "cuadriceps"); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.CreatePlan(ctx, training.Plan{Name: "legs", Items: []training.PlanItem{
		{Exercise: "squat", TargetSets: 3, RepMin: 5, RepMax: 8, TargetRPE: 8},
		{Exercise: "leg press", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.StartPlannedSession(ctx, "2026-11-12", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := warmupFixture{svc: svc, session: session}
	for _, rpe := range []float64{8, 9} {
		set, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "squat", WeightKG: 100, Reps: 5, RPE: rpe})
		if err != nil {
			t.Fatal(err)
		}
		f.working = append(f.working, set)
	}
	f.warmup, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "squat", WeightKG: 140, Reps: 3, RPE: 9, Category: training.SetWarmup})
	if err != nil {
		t.Fatal(err)
	}
	if f.warmup.Category != training.SetWarmup || f.working[0].Category != training.SetWorking {
		t.Fatalf("categories = %q, %q", f.warmup.Category, f.working[0].Category)
	}
	if err = svc.RecordFeedback(ctx, session.ID, training.Feedback{MuscleGroup: "cuadriceps", Fatigue: 1, Pump: 1, Recovery: 1}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f warmupFixture) workingSI() float64 {
	return training.NormalizeSI(f.working[0].SI + f.working[1].SI)
}

// One subtest per computation the owner listed: a warm-up never counts in it.
func TestWarmupSetsAreExcludedFromEveryComputation(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := newWarmupFixture(t, store, 1)
	ctx := context.Background()
	want := f.workingSI()
	if want != 2.2 {
		t.Fatalf("fixture working SI = %v, want 2.2", want)
	}

	t.Run("set SI and session total SI", func(t *testing.T) {
		if f.warmup.SI != 0 {
			t.Fatalf("warm-up SI = %v, want 0", f.warmup.SI)
		}
		got, err := f.svc.GetSession(ctx, f.session.ID)
		if err != nil || got.TotalSI != want || len(got.Sets) != 3 {
			t.Fatalf("GetSession = %+v, %v", got, err)
		}
	})
	t.Run("session set count", func(t *testing.T) {
		got, err := f.svc.ListSessions(ctx, training.ListFilter{})
		if err != nil || len(got) != 1 || got[0].SetCount != 2 || got[0].TotalSI != want {
			t.Fatalf("ListSessions = %+v, %v", got, err)
		}
	})
	t.Run("weekly volume", func(t *testing.T) {
		got, err := f.svc.WeeklyVolume(ctx, training.ListFilter{})
		if err != nil || len(got) != 1 || got[0].Sets != 2 || got[0].TotalSI != want {
			t.Fatalf("WeeklyVolume = %+v, %v", got, err)
		}
	})
	t.Run("volume by muscle", func(t *testing.T) {
		got, err := f.svc.VolumeByGroup(ctx, training.ListFilter{})
		if err != nil || len(got) != 1 || got[0].Sets != 2 || got[0].TotalSI != want {
			t.Fatalf("VolumeByGroup = %+v, %v", got, err)
		}
	})
	t.Run("session progress", func(t *testing.T) {
		got, err := f.svc.SessionProgress(ctx, f.session.ID)
		if err != nil || len(got) != 2 || got[0].Exercise != "squat" || got[0].DoneSets != 2 {
			t.Fatalf("SessionProgress = %+v, %v", got, err)
		}
	})
	t.Run("live workout next and done", func(t *testing.T) {
		got, err := f.svc.LiveWorkout(ctx)
		if err != nil || got.Next == nil || got.Next.Exercise != "squat" || got.Next.DoneSets != 2 {
			t.Fatalf("LiveWorkout next = %+v, %v", got.Next, err)
		}
	})
	t.Run("best set", func(t *testing.T) {
		got, err := f.svc.ExerciseHistory(ctx, "squat", 0)
		if err != nil || got.Best == nil || got.Best.WeightKG != 100 {
			t.Fatalf("Best = %+v, %v", got.Best, err)
		}
	})
	t.Run("e1RM history", func(t *testing.T) {
		got, err := f.svc.ExerciseHistory(ctx, "squat", 0)
		if err != nil || len(got.Sets) != 3 {
			t.Fatalf("ExerciseHistory = %+v, %v", got, err)
		}
		for _, s := range got.Sets {
			warm := s.SetID == f.warmup.ID
			if warm != (s.Category == training.SetWarmup) || warm != (s.Est1RM == 0) {
				t.Fatalf("history set = %+v", s)
			}
		}
	})
	t.Run("suggest load", func(t *testing.T) {
		got, err := f.svc.SuggestLoad(ctx, "squat")
		if err != nil || got.LastWeightKG != 100 {
			t.Fatalf("SuggestLoad = %+v, %v", got, err)
		}
	})
	t.Run("volume recommendation", func(t *testing.T) {
		got, err := f.svc.VolumeRecommendation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range got {
			if c.MuscleGroup == "cuadriceps" && c.LastWeekSets != 2 {
				t.Fatalf("recommendation = %+v", c)
			}
		}
	})
	t.Run("performance drop-off", func(t *testing.T) {
		got, err := f.svc.PerformanceDropOff(ctx, f.session.ID, "squat")
		if err != nil || len(got) != 1 {
			t.Fatalf("PerformanceDropOff = %+v, %v", got, err)
		}
		for _, s := range got[0].Sets {
			if s.SetID == f.warmup.ID && (s.Comparable || s.ExcludedReason != training.ExcludedWarmup) {
				t.Fatalf("warm-up in drop-off = %+v", s)
			}
			if s.SetID == f.working[0].ID && !s.Reference {
				t.Fatalf("reference = %+v", s)
			}
		}
	})
	t.Run("recent exercises prefill", func(t *testing.T) {
		got, err := f.svc.RecentExercises(ctx, 5)
		if err != nil || len(got) != 1 || got[0].WeightKG != 100 {
			t.Fatalf("RecentExercises = %+v, %v", got, err)
		}
	})
}

// Toggling a set's category recomputes its SI both ways, and leaves the other
// fields alone.
func TestStoreUpdateSetTogglesCategory(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := newWarmupFixture(t, store, 1)
	ctx := context.Background()
	warm := training.SetWarmup
	set, total, err := f.svc.UpdateSet(ctx, f.working[0].ID, training.SetPatch{Category: &warm})
	if err != nil || set.Category != training.SetWarmup || set.SI != 0 || set.WeightKG != 100 || total != f.working[1].SI {
		t.Fatalf("to warm-up = %+v, %v, %v", set, total, err)
	}
	working := training.SetWorking
	set, total, err = f.svc.UpdateSet(ctx, f.warmup.ID, training.SetPatch{Category: &working})
	if err != nil || set.Category != training.SetWorking || set.SI != training.CalculateSI(9) || total != training.NormalizeSI(f.working[1].SI+set.SI) {
		t.Fatalf("to working = %+v, %v, %v", set, total, err)
	}
}

// Another user can neither toggle a set nor see its category in their numbers.
func TestWarmupCategoryIsIsolatedBetweenUsers(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	f := newWarmupFixture(t, store, 1)
	before := dumpAll(t, store)
	other := training.NewService(store.ForUser(2), time.Now)
	working := training.SetWorking
	if _, _, err := other.UpdateSet(ctx, f.warmup.ID, training.SetPatch{Category: &working}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("user 2 toggled user 1's set: %v", err)
	}
	if _, _, err := other.AddSet(ctx, training.AddSetInput{SessionID: f.session.ID, Exercise: "squat", WeightKG: 60, Reps: 5, RPE: 8, Category: training.SetWarmup}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("user 2 added a warm-up to user 1's session: %v", err)
	}
	if after := dumpAll(t, store); after != before {
		t.Fatalf("user 2 changed user 1's data:\nbefore %s\nafter  %s", before, after)
	}
	g := newWarmupFixture(t, store, 2)
	for _, svc := range []*training.Service{f.svc, g.svc} {
		got, err := svc.WeeklyVolume(ctx, training.ListFilter{})
		if err != nil || len(got) != 1 || got[0].Sets != 2 {
			t.Fatalf("WeeklyVolume = %+v, %v", got, err)
		}
	}
}

// migrateTo applies the embedded migrations up to and including version max on
// a fresh database, the state a server running that release left behind.
func migrateTo(t *testing.T, path string, max int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	names := mustMigrationNames()
	versions, err := migrationVersions(names)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		if versions[i] > max {
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

// Migration 023 turns the sets the coach marked with a warm-up word in the
// technique field into warm-up sets, and rounds stored weights to 2 decimals.
func TestMigration023ConvertsWarmupTechniqueAndRoundsWeights(t *testing.T) {
	path := t.TempDir() + "/training.db"
	db := migrateTo(t, path, 22)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO sessions(id,user_id,date,started_at) VALUES (1,1,'2026-11-12',CURRENT_TIMESTAMP),(2,2,'2026-11-12',CURRENT_TIMESTAMP)`)
	rows := []struct {
		session   int
		technique string
		weight    float64
	}{
		{1, "warmup", 60},
		{1, "Warm-Up", 70},
		{2, "CALENTAMIENTO", 40},
		{1, "drop set", 80},
		{1, "", 65.77089365},
		{2, "", 102.5},
	}
	for i, r := range rows {
		exec(`INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,technique,created_at,updated_at) VALUES (?,?,?,?,5,8,1,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.session, i+1, "squat", r.weight, r.technique)
	}
	db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	want := []struct {
		category, technique string
		si, weight          float64
	}{
		{training.SetWarmup, "", 0, 60},
		{training.SetWarmup, "", 0, 70},
		{training.SetWarmup, "", 0, 40},
		{training.SetWorking, "drop set", 1, 80},
		{training.SetWorking, "", 1, 65.77},
		{training.SetWorking, "", 1, 102.5},
	}
	got, err := store.db.Query(`SELECT category,technique,si,weight_kg FROM sets ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	i := 0
	for ; got.Next(); i++ {
		var category, technique string
		var si, weight float64
		if err = got.Scan(&category, &technique, &si, &weight); err != nil {
			t.Fatal(err)
		}
		w := want[i]
		if category != w.category || technique != w.technique || si != w.si || weight != w.weight {
			t.Fatalf("row %d = %q %q %v %v, want %+v", i, category, technique, si, weight, w)
		}
	}
	if i != len(want) {
		t.Fatalf("rows = %d", i)
	}
	if _, err = store.db.Exec(`UPDATE sets SET category='other' WHERE position=1`); err == nil {
		t.Fatal("an unknown category was stored")
	}
}
