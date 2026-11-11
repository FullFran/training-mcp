package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

func TestRestTargetIsUnsetUntilChosenAndPerUser(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	one, two := store.ForUser(1), store.ForUser(2)

	if got, err := one.RestTarget(ctx); err != nil || got != 0 {
		t.Fatalf("unset RestTarget() = %d, %v, want 0", got, err)
	}
	if err := one.SetRestTarget(ctx, 150); err != nil {
		t.Fatal(err)
	}
	if err := two.SetRestTarget(ctx, 60); err != nil {
		t.Fatal(err)
	}
	if got, err := one.RestTarget(ctx); err != nil || got != 150 {
		t.Fatalf("user 1 RestTarget() = %d, %v, want 150", got, err)
	}
	if got, err := two.RestTarget(ctx); err != nil || got != 60 {
		t.Fatalf("user 2 RestTarget() = %d, %v, want 60", got, err)
	}
}

func TestLastSetAtIsTheLatestLoggedSet(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	user := store.ForUser(1)
	session, err := user.Start(ctx, training.Session{Date: "2026-08-07"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = user.LastSetAt(ctx, session.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("empty session error = %v, want not found", err)
	}
	first, _, err := user.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := user.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "remo", WeightKG: 60, Reps: 10, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	// Same second for both: the later id wins.
	id, at, err := user.LastSetAt(ctx, session.ID)
	if err != nil || id != second.ID {
		t.Fatalf("LastSetAt() = %d, %v, want %d", id, err, second.ID)
	}
	if d := time.Since(at); d < -time.Minute || d > time.Minute || at.Location() != time.UTC {
		t.Fatalf("logged at %v, want about now in UTC", at)
	}

	// The time comes from created_at, not from position or later edits.
	if _, err = store.db.Exec(`UPDATE sets SET created_at='2026-08-07 09:00:00' WHERE id=?`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE sets SET created_at='2026-08-07 09:05:30' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	reps := 5
	if _, _, err = user.UpdateSet(ctx, second.ID, training.SetPatch{Reps: &reps}); err != nil {
		t.Fatal(err)
	}
	id, at, err = user.LastSetAt(ctx, session.ID)
	if want := time.Date(2026, 8, 7, 9, 5, 30, 0, time.UTC); err != nil || id != first.ID || !at.Equal(want) {
		t.Fatalf("LastSetAt() = %d, %v, %v, want %d at %v", id, at, err, first.ID, want)
	}
}
