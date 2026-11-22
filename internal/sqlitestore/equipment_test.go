package sqlitestore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

func newEquipmentService(t *testing.T) (*Store, *training.Service) {
	t.Helper()
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	clock := func() time.Time { return time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC) }
	return store, training.NewService(store.ForUser(1), clock)
}

// equipmentIsNull reads the raw column, so "not stated" is checked as NULL in
// the database and not only as an empty string in Go.
func equipmentIsNull(t *testing.T, store *Store, setID int64) bool {
	t.Helper()
	var isNull bool
	if err := store.db.QueryRow(`SELECT equipment IS NULL FROM sets WHERE id=?`, setID).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	return isNull
}

func TestAddSetStoresNormalizedEquipmentAndEveryReadReturnsIt(t *testing.T) {
	ctx := context.Background()
	_, svc := newEquipmentService(t)
	session, err := svc.StartSession(ctx, "2026-11-22")
	if err != nil {
		t.Fatal(err)
	}
	set, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "aperturas", WeightKG: 90, Reps: 10, RPE: 8, Equipment: "  Technogym   Pec Fly "})
	if err != nil {
		t.Fatal(err)
	}
	if set.Equipment != "technogym pec fly" {
		t.Fatalf("AddSet equipment = %q, want normalized", set.Equipment)
	}
	got, err := svc.GetSession(ctx, session.ID)
	if err != nil || len(got.Sets) != 1 || got.Sets[0].Equipment != "technogym pec fly" {
		t.Fatalf("GetSession sets = %#v, err=%v", got.Sets, err)
	}
	history, err := svc.ExerciseHistory(ctx, "aperturas", 10)
	if err != nil || len(history.Sets) != 1 || history.Sets[0].Equipment != "technogym pec fly" {
		t.Fatalf("history sets = %#v, err=%v", history.Sets, err)
	}
	if history.Best == nil || history.Best.Equipment != "technogym pec fly" {
		t.Fatalf("history best = %#v", history.Best)
	}
}

func TestSetWithoutEquipmentIsStoredAsNull(t *testing.T) {
	ctx := context.Background()
	store, svc := newEquipmentService(t)
	_, sets, err := svc.LogSet(ctx, "row", 60, 10, 8, 1, "", "", "   ")
	if err != nil || len(sets) != 1 {
		t.Fatalf("LogSet sets=%v err=%v", sets, err)
	}
	if sets[0].Equipment != "" || !equipmentIsNull(t, store, sets[0].ID) {
		t.Fatalf("blank equipment must be stored as NULL, got %q", sets[0].Equipment)
	}
}

func TestLogSetRecordsEquipmentOnEveryCopy(t *testing.T) {
	ctx := context.Background()
	_, svc := newEquipmentService(t)
	_, sets, err := svc.LogSet(ctx, "row", 60, 10, 8, 3, "", "", "Hammer Row")
	if err != nil || len(sets) != 3 {
		t.Fatalf("LogSet sets=%v err=%v", sets, err)
	}
	for _, s := range sets {
		if s.Equipment != "hammer row" {
			t.Fatalf("set %d equipment = %q", s.ID, s.Equipment)
		}
	}
}

func TestLogSetsKeepsEquipmentPerSetAndInTheIdempotencyFingerprint(t *testing.T) {
	ctx := context.Background()
	store, svc := newEquipmentService(t)
	in := training.LogSetsInput{Key: "k1", Exercise: "aperturas", Sets: []training.LoggedSet{
		{WeightKG: 100, Reps: 6, RPE: 8, Equipment: "Machine A"},
		{WeightKG: 86, Reps: 6, RPE: 8},
	}}
	res, err := svc.LogSets(ctx, in)
	if err != nil || len(res.Sets) != 2 {
		t.Fatalf("LogSets = %#v, err=%v", res, err)
	}
	if res.Sets[0].Equipment != "machine a" || res.Sets[1].Equipment != "" || !equipmentIsNull(t, store, res.Sets[1].ID) {
		t.Fatalf("sets = %#v", res.Sets)
	}
	replay, err := svc.LogSets(ctx, in)
	if err != nil || !replay.Replayed || replay.Sets[0].Equipment != "machine a" {
		t.Fatalf("replay = %#v, err=%v", replay, err)
	}
	in.Sets[0].Equipment = "Machine B"
	if _, err := svc.LogSets(ctx, in); !errors.Is(err, training.ErrIdempotencyConflict) {
		t.Fatalf("same key with other equipment: err=%v, want ErrIdempotencyConflict", err)
	}
}

func TestUpdateSetChangesAndClearsEquipment(t *testing.T) {
	ctx := context.Background()
	store, svc := newEquipmentService(t)
	_, sets, err := svc.LogSet(ctx, "row", 60, 10, 8, 1, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	id := sets[0].ID
	v := "Cable  Row"
	set, _, err := svc.UpdateSet(ctx, id, training.SetPatch{Equipment: &v})
	if err != nil || set.Equipment != "cable row" {
		t.Fatalf("set=%#v err=%v", set, err)
	}
	// A patch that does not mention equipment leaves it alone.
	reps := 9
	if set, _, err = svc.UpdateSet(ctx, id, training.SetPatch{Reps: &reps}); err != nil || set.Equipment != "cable row" {
		t.Fatalf("set=%#v err=%v", set, err)
	}
	empty := ""
	if set, _, err = svc.UpdateSet(ctx, id, training.SetPatch{Equipment: &empty}); err != nil || set.Equipment != "" {
		t.Fatalf("set=%#v err=%v", set, err)
	}
	if !equipmentIsNull(t, store, id) {
		t.Fatal("cleared equipment must be stored as NULL")
	}
}

func TestEquipmentLongerThanTheLimitIsRejected(t *testing.T) {
	ctx := context.Background()
	_, svc := newEquipmentService(t)
	long := strings.Repeat("x", 81)
	if _, _, err := svc.LogSet(ctx, "row", 60, 10, 8, 1, "", "", long); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("LogSet err=%v, want ErrValidation", err)
	}
	if _, err := svc.LogSets(ctx, training.LogSetsInput{Key: "k", Exercise: "row", Sets: []training.LoggedSet{{WeightKG: 60, Reps: 10, RPE: 8, Equipment: long}}}); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("LogSets err=%v, want ErrValidation", err)
	}
	_, sets, err := svc.LogSet(ctx, "row", 60, 10, 8, 1, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.UpdateSet(ctx, sets[0].ID, training.SetPatch{Equipment: &long}); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("UpdateSet err=%v, want ErrValidation", err)
	}
	if _, err := svc.GetSession(ctx, sets[0].SessionID); err != nil {
		t.Fatal(err)
	}
}
