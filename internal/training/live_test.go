package training

import (
	"context"
	"errors"
	"testing"
	"time"
)

var liveNow = time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)

func TestRestStateFollowsTheLastSet(t *testing.T) {
	last := liveNow.Add(-50 * time.Second)
	cases := []struct {
		name     string
		lastAt   time.Time
		superset bool
		want     Rest
	}{
		{"no set yet", time.Time{}, false, Rest{TargetSeconds: 120, State: RestNone}},
		{"resting", last, false, Rest{TargetSeconds: 120, State: RestResting, Since: last.Format(time.RFC3339), ElapsedSeconds: 50, RemainingSeconds: 70}},
		{"target reached", liveNow.Add(-130 * time.Second), false, Rest{TargetSeconds: 120, State: RestReady, Since: liveNow.Add(-130 * time.Second).Format(time.RFC3339), ElapsedSeconds: 130, OverBySeconds: 10}},
		{"exactly on target", liveNow.Add(-120 * time.Second), false, Rest{TargetSeconds: 120, State: RestReady, Since: liveNow.Add(-120 * time.Second).Format(time.RFC3339), ElapsedSeconds: 120}},
		{"superset goes straight on", last, true, Rest{TargetSeconds: 120, State: RestSuperset, Since: last.Format(time.RFC3339), ElapsedSeconds: 50}},
		{"long gap is not a rest", liveNow.Add(-RestIdleAfter - time.Second), false, Rest{TargetSeconds: 120, State: RestIdle, Since: liveNow.Add(-RestIdleAfter - time.Second).Format(time.RFC3339), ElapsedSeconds: int((RestIdleAfter + time.Second).Seconds())}},
		{"clock skew never goes negative", liveNow.Add(5 * time.Second), false, Rest{TargetSeconds: 120, State: RestResting, Since: liveNow.Add(5 * time.Second).Format(time.RFC3339), RemainingSeconds: 120}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RestState(tc.lastAt, liveNow, 120, tc.superset); got != tc.want {
				t.Fatalf("RestState() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNextPlannedExercise(t *testing.T) {
	plan := []PlanProgress{
		{Exercise: "sentadilla", TargetSets: 3, DoneSets: 3},
		{Exercise: "banca", TargetSets: 3, DoneSets: 1},
		{Exercise: "remo", TargetSets: 3, Superset: "A"},
		{Exercise: "fondos", TargetSets: 3, Superset: "A"},
		{Exercise: "curl", TargetSets: 2, Skipped: true},
		{Exercise: "face pull", TargetSets: 2},
		{Exercise: "gemelo", DoneSets: 2},
	}
	cases := []struct {
		name, last string
		progress   []PlanProgress
		want       string
		superset   bool
	}{
		{"keeps going with the unfinished exercise", "banca", plan, "banca", false},
		{"moves to the first unfinished one", "sentadilla", plan, "banca", false},
		{"next of the round without rest", "remo", plan, "fondos", true},
		{"rests after the round, then starts the next one", "fondos", plan, "remo", false},
		{"straight sets when the rest of the round is done", "remo", []PlanProgress{{Exercise: "remo", TargetSets: 3, Superset: "A"}, {Exercise: "fondos", TargetSets: 2, DoneSets: 2, Superset: "A"}}, "remo", false},
		{"off-plan last set points at the plan", "gemelo", plan, "banca", false},
		{"no set yet starts the plan", "", plan, "banca", false},
		{"nothing left", "banca", []PlanProgress{{Exercise: "banca", TargetSets: 1, DoneSets: 1}, {Exercise: "curl", TargetSets: 1, Skipped: true}}, "", false},
		{"no plan", "banca", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, superset := NextPlanned(tc.progress, tc.last)
			name := ""
			if got != nil {
				name = got.Exercise
			}
			if name != tc.want || superset != tc.superset {
				t.Fatalf("NextPlanned(%q) = %q superset=%v, want %q superset=%v", tc.last, name, superset, tc.want, tc.superset)
			}
		})
	}
}

func TestRestTargetDefaultsAndValidation(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return liveNow })

	if got, err := svc.RestTarget(ctx); err != nil || got != DefaultRestSeconds {
		t.Fatalf("unset RestTarget() = %d, %v, want %d", got, err, DefaultRestSeconds)
	}
	for _, bad := range []int{0, MinRestSeconds - 1, MaxRestSeconds + 1, -30} {
		if err := svc.SetRestTarget(ctx, bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("SetRestTarget(%d) error = %v, want validation", bad, err)
		}
	}
	if err := svc.SetRestTarget(ctx, 90); err != nil {
		t.Fatalf("SetRestTarget(90) error = %v", err)
	}
	if got, err := svc.RestTarget(ctx); err != nil || got != 90 {
		t.Fatalf("RestTarget() = %d, %v, want 90", got, err)
	}
}

func TestLiveWorkoutWithoutSessionToday(t *testing.T) {
	store := newMemoryStore()
	got, err := NewService(store, func() time.Time { return liveNow }).LiveWorkout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != nil || got.LastSet != nil || got.Next != nil || got.Date != "2026-08-07" ||
		got.Rest != (Rest{TargetSeconds: DefaultRestSeconds, State: RestNone}) || got.Now != liveNow.Format(time.RFC3339) {
		t.Fatalf("LiveWorkout() = %+v", got)
	}
	if store.listFilter != (ListFilter{From: "2026-08-07", To: "2026-08-07", Limit: 1}) {
		t.Fatalf("looked up sessions with %+v, want today only", store.listFilter)
	}
}

func TestLiveWorkoutReportsTheLastSetAndRest(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return liveNow })
	session, _ := store.Start(ctx, Session{Date: "2026-08-07"})
	store.listResults = []SessionSummary{{ID: session.ID, Date: session.Date}}
	first, _, _ := store.AddSet(ctx, AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 8})
	second, _, _ := store.AddSet(ctx, AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 7, RPE: 9})
	store.setTimes = map[int64]time.Time{first.ID: liveNow.Add(-5 * time.Minute), second.ID: liveNow.Add(-45 * time.Second)}
	if err := svc.SetRestTarget(ctx, 150); err != nil {
		t.Fatal(err)
	}

	got, err := svc.LiveWorkout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Session == nil || got.Session.ID != session.ID || len(got.Session.Sets) != 2 {
		t.Fatalf("session = %+v", got.Session)
	}
	if got.LastSet == nil || got.LastSet.Set.ID != second.ID || got.LastSet.Set.Reps != 7 ||
		got.LastSet.LoggedAt != liveNow.Add(-45*time.Second).Format(time.RFC3339) {
		t.Fatalf("last set = %+v", got.LastSet)
	}
	want := Rest{TargetSeconds: 150, State: RestResting, Since: got.LastSet.LoggedAt, ElapsedSeconds: 45, RemainingSeconds: 105}
	if got.Rest != want {
		t.Fatalf("rest = %+v, want %+v", got.Rest, want)
	}
	// No plan items: there is nothing planned next and no progress to show.
	if got.Next != nil || got.Progress != nil {
		t.Fatalf("next = %+v progress = %+v, want none without a plan", got.Next, got.Progress)
	}
}

func TestLiveWorkoutPointsAtThePlan(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return liveNow })
	session, _ := store.Start(ctx, Session{Date: "2026-08-07"})
	store.listResults = []SessionSummary{{ID: session.ID, Date: session.Date}}
	set, _, _ := store.AddSet(ctx, AddSetInput{SessionID: session.ID, Exercise: "remo", WeightKG: 60, Reps: 10, RPE: 8})
	store.setTimes = map[int64]time.Time{set.ID: liveNow.Add(-20 * time.Second)}
	store.progress = []PlanProgress{
		{Exercise: "remo", TargetSets: 3, DoneSets: 1, Superset: "A"},
		{Exercise: "fondos", TargetSets: 3, Superset: "A"},
	}

	got, err := svc.LiveWorkout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Next == nil || got.Next.Exercise != "fondos" || len(got.Progress) != 2 {
		t.Fatalf("next = %+v progress = %+v", got.Next, got.Progress)
	}
	if got.Rest.State != RestSuperset {
		t.Fatalf("rest state = %q, want %q", got.Rest.State, RestSuperset)
	}
}

func TestLiveWorkoutRestsAfterASupersetRound(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return liveNow })
	session, _ := store.Start(ctx, Session{Date: "2026-08-07"})
	store.listResults = []SessionSummary{{ID: session.ID, Date: session.Date}}
	set, _, _ := store.AddSet(ctx, AddSetInput{SessionID: session.ID, Exercise: "fondos", WeightKG: 0, Reps: 12, RPE: 8})
	store.setTimes = map[int64]time.Time{set.ID: liveNow.Add(-20 * time.Second)}
	store.progress = []PlanProgress{
		{Exercise: "remo", TargetSets: 3, DoneSets: 1, Superset: "A"},
		{Exercise: "fondos", TargetSets: 3, DoneSets: 1, Superset: "A"},
	}

	got, err := svc.LiveWorkout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Next == nil || got.Next.Exercise != "remo" {
		t.Fatalf("next = %+v, want remo", got.Next)
	}
	if got.Rest.State == RestSuperset {
		t.Fatalf("rest state = %q after the last exercise of the round, want a rest", got.Rest.State)
	}
}
