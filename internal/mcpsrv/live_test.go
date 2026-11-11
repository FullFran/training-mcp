package mcpsrv

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

func liveClock() time.Time { return time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC) }

func TestLiveWorkoutShowsTheSessionTheLastSetAndTheRest(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/training.db"
	store, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := training.NewService(store.ForUser(1), liveClock)
	session := mustConnect(t, New(only(service)).Handler())

	// Nothing logged today: the default rest and no session.
	var idle LiveWorkoutOut
	decode(t, call(t, session, "live_workout", map[string]any{}), &idle)
	if idle.Live.Session != nil || idle.Live.LastSet != nil || idle.Live.Date != "2026-08-07" ||
		idle.Live.Rest != (training.Rest{TargetSeconds: 120, State: training.RestNone}) {
		t.Fatalf("idle live workout = %+v", idle.Live)
	}

	plan, err := service.CreatePlan(ctx, training.Plan{Name: "empuje", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 2, RepMin: 8, RepMax: 10, TargetRPE: 8},
		{Exercise: "remo", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.StartPlannedSession(ctx, "2026-08-07", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	set, _, err := service.AddSet(ctx, training.AddSetInput{SessionID: started.ID, Exercise: "banca", WeightKG: 80, Reps: 9, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE sets SET created_at='2026-08-07 09:59:20' WHERE id=?`, set.ID); err != nil {
		t.Fatal(err)
	}

	var rest RestTargetOut
	decode(t, call(t, session, "set_rest_target", map[string]any{"rest_seconds": 90}), &rest)
	if rest.RestSeconds != 90 {
		t.Fatalf("set_rest_target = %+v", rest)
	}
	var live LiveWorkoutOut
	decode(t, call(t, session, "live_workout", map[string]any{}), &live)
	got := live.Live
	if got.Session == nil || got.Session.ID != started.ID || got.Session.PlanName != "empuje" || len(got.Progress) != 2 {
		t.Fatalf("session = %+v progress = %+v", got.Session, got.Progress)
	}
	if got.LastSet == nil || got.LastSet.Set.ID != set.ID || got.LastSet.LoggedAt != "2026-08-07T09:59:20Z" || got.LastSet.SecondsAgo != 40 {
		t.Fatalf("last set = %+v", got.LastSet)
	}
	if got.Next == nil || got.Next.Exercise != "banca" || got.Next.DoneSets != 1 {
		t.Fatalf("next = %+v", got.Next)
	}
	want := training.Rest{TargetSeconds: 90, State: training.RestResting, Since: "2026-08-07T09:59:20Z", ElapsedSeconds: 40, RemainingSeconds: 50}
	if got.Rest != want {
		t.Fatalf("rest = %+v, want %+v", got.Rest, want)
	}

	for _, bad := range []any{29, 901, 0, "90"} {
		if result := call(t, session, "set_rest_target", map[string]any{"rest_seconds": bad}); !result.IsError {
			t.Fatalf("set_rest_target accepted %v", bad)
		}
	}
	if v, err := service.RestTarget(ctx); err != nil || v != 90 {
		t.Fatalf("a rejected call changed the target: %d, %v", v, err)
	}
}
