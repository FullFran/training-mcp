package sqlitestore

import (
	"context"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// A session started here reports when it started, in UTC, and only to its
// owner; an imported workout's start is in the source app's unknown clock, so it
// is not reported as an instant.
func TestGetSessionReportsWhenTheSessionStarted(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	owner := store.ForUser(1)
	started, err := owner.Start(ctx, training.Session{Date: "2026-08-07"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE sessions SET started_at='2026-08-07 09:35:00' WHERE id=?`, started.ID); err != nil {
		t.Fatal(err)
	}
	got, err := owner.GetSession(ctx, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StartedAt != "2026-08-07T09:35:00Z" {
		t.Fatalf("StartedAt = %q, want 2026-08-07T09:35:00Z", got.StartedAt)
	}
	if _, err = store.ForUser(2).GetSession(ctx, started.ID); err == nil {
		t.Fatal("the second user read the owner's session")
	}

	if _, err = store.ImportsFor(1).ApplyImport(ctx, importRecord(workout("2025-01-27", importedSet("k1", 100)))); err != nil {
		t.Fatal(err)
	}
	var importedID int64
	if err = store.db.QueryRow(`SELECT id FROM sessions WHERE user_id=1 AND date='2025-01-27'`).Scan(&importedID); err != nil {
		t.Fatal(err)
	}
	imported, err := owner.GetSession(ctx, importedID)
	if err != nil {
		t.Fatal(err)
	}
	if imported.StartedAt != "" {
		t.Fatalf("imported StartedAt = %q, want empty", imported.StartedAt)
	}
}
