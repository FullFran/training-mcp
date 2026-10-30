package sqlitestore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

func logSetsRecord(key string, reps ...int) training.LogSetsRecord {
	r := training.LogSetsRecord{Key: key, Fingerprint: "fp-" + key, Date: "2026-10-30", Exercise: "remo"}
	for _, n := range reps {
		r.Sets = append(r.Sets, training.LoggedSet{WeightKG: 70, Reps: n, RPE: 8})
	}
	return r
}

func storedSets(t *testing.T, store *Store) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sets`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLogSetsRollsBackEntirelyOnFailure(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	boom := errors.New("boom")
	store.failpoint = func(stage string) error {
		if stage == "log-sets-inserted" {
			return boom
		}
		return nil
	}
	if _, err := store.LogSets(context.Background(), logSetsRecord("k", 12, 10)); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	store.failpoint = nil
	var sessions, keys int
	if err := store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sessions),(SELECT COUNT(*) FROM idempotency_keys)`).Scan(&sessions, &keys); err != nil {
		t.Fatal(err)
	}
	if got := storedSets(t, store); got != 0 || sessions != 0 || keys != 0 {
		t.Fatalf("left sets=%d sessions=%d keys=%d behind", got, sessions, keys)
	}
	// A retry after the failure must actually record, not replay an empty result.
	out, err := store.LogSets(context.Background(), logSetsRecord("k", 12, 10))
	if err != nil || out.Replayed || len(out.Sets) != 2 {
		t.Fatalf("retry = %#v, %v", out, err)
	}
}

func TestLogSetsSameKeyConcurrentlyRecordsOnce(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var wg sync.WaitGroup
	results := make([]training.LogSetsResult, 8)
	errs := make([]error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = store.LogSets(context.Background(), logSetsRecord("same", 12, 10))
		}()
	}
	wg.Wait()
	fresh := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !results[i].Replayed {
			fresh++
		}
		if results[i].Sets[0].ID != results[0].Sets[0].ID {
			t.Fatalf("call %d returned different sets", i)
		}
	}
	if fresh != 1 {
		t.Fatalf("%d calls recorded, want 1", fresh)
	}
	if got := storedSets(t, store); got != 2 {
		t.Fatalf("stored sets = %d, want 2", got)
	}
}

func TestLogSetsDifferentKeysConcurrentlyShareOneSession(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	keys := []string{"a", "b", "c", "d", "e", "f"}
	var wg sync.WaitGroup
	errs := make([]error, len(keys))
	for i, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = store.LogSets(context.Background(), logSetsRecord(key, 12, 10))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("key %s: %v", keys[i], err)
		}
	}
	var sessions, distinctPositions int
	if err := store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sessions),(SELECT COUNT(DISTINCT position) FROM sets)`).Scan(&sessions, &distinctPositions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || distinctPositions != 12 {
		t.Fatalf("sessions=%d positions=%d", sessions, distinctPositions)
	}
}

func TestLogSetsReplaySurvivesARestart(t *testing.T) {
	path := t.TempDir() + "/training.db"
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.LogSets(context.Background(), logSetsRecord("k", 12))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	again, err := store.LogSets(context.Background(), logSetsRecord("k", 12))
	if err != nil || !again.Replayed || again.Sets[0].ID != first.Sets[0].ID {
		t.Fatalf("replay = %#v, %v", again, err)
	}
	if got := storedSets(t, store); got != 1 {
		t.Fatalf("stored sets = %d", got)
	}
}

func TestLogSetsStampsTheSupersetFromTheSessionPlan(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	plan, err := store.CreatePlan(ctx, training.Plan{Name: "A", Items: []training.PlanItem{{Exercise: "remo", TargetSets: 3, Superset: "A"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Start(ctx, training.Session{Date: "2026-10-30", PlanID: plan.ID}); err != nil {
		t.Fatal(err)
	}
	out, err := store.LogSets(ctx, logSetsRecord("k", 12))
	if err != nil {
		t.Fatal(err)
	}
	if out.Sets[0].Superset != "A" {
		t.Fatalf("superset = %q", out.Sets[0].Superset)
	}
}
