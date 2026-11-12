package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// A session's workout summary is claimed once: the first claim wins, later ones
// report it was already proposed, and a session that is not the user's is
// missing, the same as one that does not exist.
func TestClaimWorkoutSummaryIsOncePerSessionAndPerUser(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	owner, stranger := store.ForUser(1), store.ForUser(2)
	session, err := owner.Start(ctx, training.Session{Date: sharedDay})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if ok, err := stranger.ClaimWorkoutSummary(ctx, session.ID, at); ok || !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("stranger claim = %v, %v; want false, not found", ok, err)
	}
	if ok, err := owner.ClaimWorkoutSummary(ctx, session.ID, at); !ok || err != nil {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if ok, err := owner.ClaimWorkoutSummary(ctx, session.ID, at.Add(time.Hour)); ok || err != nil {
		t.Fatalf("second claim = %v, %v; want false, nil", ok, err)
	}
	var stored string
	if err := store.db.QueryRow(`SELECT summary_proposed_at FROM sessions WHERE id=?`, session.ID).Scan(&stored); err != nil || stored != at.Format(time.RFC3339) {
		t.Fatalf("summary_proposed_at = %q, %v", stored, err)
	}
	if ok, err := owner.ClaimWorkoutSummary(ctx, missingID, at); ok || !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("missing claim = %v, %v", ok, err)
	}
}
