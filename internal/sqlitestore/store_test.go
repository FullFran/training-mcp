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
