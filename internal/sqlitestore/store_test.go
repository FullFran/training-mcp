package sqlitestore

import (
	"context"
	"errors"
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
