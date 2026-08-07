package sqlitestore

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

func TestStorePersistsAndResequencesAcrossRestart(t *testing.T) {
	path := t.TempDir() + "/training.db"
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Start(context.Background(), training.Session{Date: "2026-08-06"})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []training.AddSetInput{{SessionID: session.ID, Exercise: "squat", WeightKG: 100, Reps: 5, RPE: 7}, {SessionID: session.ID, Exercise: "press", WeightKG: 50, Reps: 5, RPE: 8}, {SessionID: session.ID, Exercise: "row", WeightKG: 60, Reps: 5, RPE: 9}} {
		if _, _, err := store.AddSet(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := store.DeleteSet(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(context.Background(), session.ID)
	if err != nil || len(got.Sets) != 2 || got.Sets[0].Position != 1 || got.Sets[1].Position != 2 || got.Sets[1].Exercise != "row" {
		t.Fatalf("session = %#v, err=%v", got, err)
	}
}

func TestStoreListsWithFiltersAndLimit(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026-08-01", "2026-08-03", "2026-08-02"} {
		if _, err := store.Start(context.Background(), training.Session{Date: date}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListSessions(context.Background(), training.ListFilter{From: "2026-08-02", To: "2026-08-03", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].Date != "2026-08-03" {
		t.Fatalf("rows = %#v, err=%v", rows, err)
	}
	rows, err = store.ListSessions(context.Background(), training.ListFilter{From: "2026-08-02", To: "2026-08-02", Limit: 20})
	if err != nil || len(rows) != 1 || rows[0].Date != "2026-08-02" {
		t.Fatalf("equal-bound rows = %#v, err=%v", rows, err)
	}
}

func TestStoreRecentExercisesReturnsLastSetPerExerciseNewestFirst(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Start(context.Background(), training.Session{Date: "2026-08-07"})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []training.AddSetInput{
		{SessionID: session.ID, Exercise: "squat", WeightKG: 100, Reps: 5, RPE: 8},
		{SessionID: session.ID, Exercise: "bench", WeightKG: 80, Reps: 5, RPE: 8},
		{SessionID: session.ID, Exercise: "squat", WeightKG: 110, Reps: 3, RPE: 9},
	} {
		if _, _, err := store.AddSet(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.RecentExercises(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	// One entry per exercise, most recently used first, carrying the latest values.
	want := []training.ExerciseMemory{
		{Exercise: "squat", WeightKG: 110, Reps: 3, RPE: 9},
		{Exercise: "bench", WeightKG: 80, Reps: 5, RPE: 8},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RecentExercises() = %#v, want %#v", got, want)
	}
	limited, err := store.RecentExercises(context.Background(), 1)
	if err != nil || len(limited) != 1 || limited[0].Exercise != "squat" {
		t.Fatalf("limited = %#v, err=%v", limited, err)
	}
}

func TestStoreAddSetReturnsCurrentSessionTotal(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.Start(context.Background(), training.Session{Date: "2026-08-06"})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{1.0, 2.4, 2.4} {
		_, total, err := store.AddSet(context.Background(), training.AddSetInput{
			SessionID: session.ID, Exercise: "press", WeightKG: 50, Reps: 5, RPE: []float64{8, 10, 2}[i],
		})
		if err != nil || total != want {
			t.Fatalf("add %d total=%v err=%v want=%v", i+1, total, err, want)
		}
	}
}

func TestStoreNormalizesMultiSetTotal(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.Start(context.Background(), training.Session{Date: "2026-08-06"})
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, rpe := range []float64{9, 9, 10} {
		_, total, err = store.AddSet(context.Background(), training.AddSetInput{
			SessionID: session.ID, Exercise: "press", WeightKG: 50, Reps: 5, RPE: rpe,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 3.8 {
		t.Fatalf("total = %v, want 3.8", total)
	}
}

func TestStoreDeleteRollbackRestoresRowsAfterInjectedFailure(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.Start(context.Background(), training.Session{Date: "2026-08-06"})
	if err != nil {
		t.Fatal(err)
	}
	for _, exercise := range []string{"squat", "press", "row"} {
		if _, _, err := store.AddSet(context.Background(), training.AddSetInput{SessionID: session.ID, Exercise: exercise, WeightKG: 50, Reps: 5, RPE: 8}); err != nil {
			t.Fatal(err)
		}
	}
	store.failpoint = func(stage string) error {
		if stage == "delete-resequence" {
			return errors.New("injected failure")
		}
		return nil
	}
	if _, _, _, err := store.DeleteSet(context.Background(), 2); err == nil {
		t.Fatal("injected delete failure should be returned")
	}
	store.failpoint = nil
	got, err := store.GetSession(context.Background(), session.ID)
	if err != nil || len(got.Sets) != 3 || got.Sets[1].Exercise != "press" || got.Sets[1].Position != 2 {
		t.Fatalf("rollback session=%#v err=%v", got, err)
	}
}

func TestStoreVolumeByGroupPartitionsSessionSIAndSurfacesUnmapped(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s1, _ := store.Start(ctx, training.Session{Date: "2026-08-01"})
	s2, _ := store.Start(ctx, training.Session{Date: "2026-08-05"})
	for _, in := range []training.AddSetInput{
		{SessionID: s1.ID, Exercise: "banca", WeightKG: 80, Reps: 5, RPE: 8},  // SI 1.0
		{SessionID: s1.ID, Exercise: "remo", WeightKG: 60, Reps: 8, RPE: 9},   // SI 1.2
		{SessionID: s2.ID, Exercise: "banca", WeightKG: 85, Reps: 3, RPE: 10}, // SI 1.4
		{SessionID: s2.ID, Exercise: "sin mapear", WeightKG: 20, Reps: 10, RPE: 8},
	} {
		if _, _, err := store.AddSet(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []training.ExerciseGroup{
		{Exercise: "banca", MuscleGroup: "pecho"},
		{Exercise: "remo", MuscleGroup: "espalda"},
	} {
		if err := store.SetExerciseGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.VolumeByGroup(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]training.GroupVolume{
		"pecho":   {MuscleGroup: "pecho", TotalSI: 2.4, Sets: 2},
		"espalda": {MuscleGroup: "espalda", TotalSI: 1.2, Sets: 1},
		// An exercise with no mapping must stay visible, not vanish from totals.
		"": {MuscleGroup: "", TotalSI: 1.0, Sets: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("groups = %#v, want %d", got, len(want))
	}
	for _, v := range got {
		if w, seen := want[v.MuscleGroup]; !seen || !reflect.DeepEqual(v, w) {
			t.Fatalf("group %q = %#v, want %#v", v.MuscleGroup, v, w)
		}
	}

	// A date range restricts the aggregation.
	ranged, err := store.VolumeByGroup(ctx, training.ListFilter{From: "2026-08-01", To: "2026-08-01"})
	if err != nil || len(ranged) != 2 {
		t.Fatalf("ranged = %#v, err=%v", ranged, err)
	}

	// Re-assigning an exercise replaces its group instead of duplicating it.
	if err := store.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "banca", MuscleGroup: "triceps"}); err != nil {
		t.Fatal(err)
	}
	groups, err := store.ExerciseGroups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups = %#v, err=%v", groups, err)
	}
	for _, g := range groups {
		if g.Exercise == "banca" && g.MuscleGroup != "triceps" {
			t.Fatalf("re-assignment did not replace the group: %#v", g)
		}
	}
}
