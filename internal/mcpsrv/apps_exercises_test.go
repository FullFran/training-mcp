package mcpsrv

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
)

// The picker lists today's plan with its progress first, skipped items left out,
// then the other exercises done today (the one just done first), each starting
// from its last set today.
func TestRestTimerExercisesListThePlanThenWhatWasDone(t *testing.T) {
	live := training.LiveWorkout{
		Session: &training.Session{Sets: []training.Set{
			{ID: 1, Exercise: "remo", WeightKG: 50, Reps: 12, Category: training.SetWorking},
			{ID: 2, Exercise: "remo", WeightKG: 55, Reps: 10, Category: training.SetWorking},
			{ID: 3, Exercise: "curl", WeightKG: 12, Reps: 12, Category: training.SetWorking},
			{ID: 4, Exercise: "curl", WeightKG: 8, Reps: 15, Category: training.SetWarmup},
			{ID: 5, Exercise: "banca", WeightKG: 80, Reps: 8, Category: training.SetWorking},
		}},
		Progress: []training.PlanProgress{
			{Exercise: "remo", TargetSets: 4, DoneSets: 2, RepMin: 8, RepMax: 12},
			{Exercise: "dominadas", TargetSets: 3, RepMin: 6, RepMax: 10},
			{Exercise: "press militar", TargetSets: 3, Skipped: true},
		},
		LastSet: &training.LastSet{Set: training.Set{ID: 5, Exercise: "banca", WeightKG: 80, Reps: 8}},
	}
	live.Next = &live.Progress[1]
	want := []RestTimerExercise{
		{Exercise: "remo", WeightKG: 55, Reps: 10, DoneSets: 2, TargetSets: 4, Planned: true},
		{Exercise: "dominadas", Reps: 10, TargetSets: 3, Planned: true},
		{Exercise: "banca", WeightKG: 80, Reps: 8, DoneSets: 1},
		{Exercise: "curl", WeightKG: 8, Reps: 15, DoneSets: 1},
	}
	if got := restTimerOut(live).Exercises; !reflect.DeepEqual(got, want) {
		t.Fatalf("exercises = %+v\nwant %+v", got, want)
	}

	// Without a plan the picker shows the exercises done today.
	live.Progress, live.Next = nil, nil
	want = []RestTimerExercise{
		{Exercise: "banca", WeightKG: 80, Reps: 8, DoneSets: 1},
		{Exercise: "remo", WeightKG: 55, Reps: 10, DoneSets: 2},
		{Exercise: "curl", WeightKG: 8, Reps: 15, DoneSets: 1},
	}
	if got := restTimerOut(live).Exercises; !reflect.DeepEqual(got, want) {
		t.Fatalf("no plan: exercises = %+v\nwant %+v", got, want)
	}

	// Nothing planned or done: nothing to pick.
	live.Session, live.LastSet = nil, nil
	if got := restTimerOut(live).Exercises; len(got) != 0 {
		t.Fatalf("nothing done or planned: exercises = %+v", got)
	}
}

// Through the tool: the progress is today's, and it moves after a set is logged
// with log_sets for the exercise the person picked.
func TestRenderRestTimerExercisesFollowLoggedSets(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := training.NewService(store.ForUser(1), time.Now)
	session := mustConnect(t, New(only(service)).Handler())
	plan, err := service.CreatePlan(ctx, training.Plan{Name: "empuje", Items: []training.PlanItem{
		{Exercise: "remo", TargetSets: 3},
		{Exercise: "press de pecho", TargetSets: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartPlannedSession(ctx, time.Now().Format("2006-01-02"), plan.ID); err != nil {
		t.Fatal(err)
	}

	var before RestTimerOut
	decode(t, call(t, session, "render_rest_timer", map[string]any{}), &before)
	if got := before.Exercises; len(got) != 2 || got[0].Exercise != "remo" || got[1].Exercise != "press de pecho" || got[1].TargetSets != 3 || got[1].DoneSets != 0 {
		t.Fatalf("before = %+v", got)
	}

	// The planned machine is busy: the chest press is logged instead of rows.
	call(t, session, "log_sets", map[string]any{"idempotency_key": "k1", "exercise": "press de pecho", "sets": []map[string]any{{"weight_kg": 40, "reps": 10, "rpe": 8}}})
	var after RestTimerOut
	decode(t, call(t, session, "render_rest_timer", map[string]any{}), &after)
	if got := after.Exercises; len(got) != 2 || got[0].Exercise != "remo" || got[0].DoneSets != 0 ||
		got[1] != (RestTimerExercise{Exercise: "press de pecho", WeightKG: 40, Reps: 10, DoneSets: 1, TargetSets: 3, Planned: true}) {
		t.Fatalf("after = %+v", got)
	}
	// The previous fields keep their behavior.
	if after.LastSet == nil || after.LastSet.Exercise != "press de pecho" || after.NextSet == nil || after.NextSet.Exercise != "press de pecho" || after.Rest.State != training.RestResting {
		t.Fatalf("timer = %+v", after)
	}
}
