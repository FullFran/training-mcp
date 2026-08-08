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

func TestStoreVolumeByGroupPartitionsSessionSIAndSurfacesUnmapped(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s1, _ := store.Start(ctx, training.Session{Date: "2026-08-01"})
	s2, _ := store.Start(ctx, training.Session{Date: "2026-08-05"})
	for _, in := range []training.AddSetInput{
		{SessionID: s1.ID, Exercise: "banca", WeightKG: 80, Reps: 5, RPE: 8},  // SI 1.0
		{SessionID: s1.ID, Exercise: "remo", WeightKG: 60, Reps: 8, RPE: 9},   // SI 1.2
		{SessionID: s2.ID, Exercise: "banca", WeightKG: 85, Reps: 3, RPE: 10}, // SI 1.4
		{SessionID: s2.ID, Exercise: "sin mapear", WeightKG: 20, Reps: 10, RPE: 8},
	} {
		if _, _, err := store.AddSet(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []training.ExerciseGroup{
		{Exercise: "banca", MuscleGroup: "pecho"},
		{Exercise: "remo", MuscleGroup: "espalda"},
	} {
		if err := store.SetExerciseGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.VolumeByGroup(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]training.GroupVolume{
		"pecho":   {MuscleGroup: "pecho", TotalSI: 2.4, Sets: 2},
		"espalda": {MuscleGroup: "espalda", TotalSI: 1.2, Sets: 1},
		// An exercise with no mapping must stay visible, not vanish from totals.
		"": {MuscleGroup: "", TotalSI: 1.0, Sets: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("groups = %#v, want %d", got, len(want))
	}
	for _, v := range got {
		if w, seen := want[v.MuscleGroup]; !seen || !reflect.DeepEqual(v, w) {
			t.Fatalf("group %q = %#v, want %#v", v.MuscleGroup, v, w)
		}
	}

	// A date range restricts the aggregation.
	ranged, err := store.VolumeByGroup(ctx, training.ListFilter{From: "2026-08-01", To: "2026-08-01"})
	if err != nil || len(ranged) != 2 {
		t.Fatalf("ranged = %#v, err=%v", ranged, err)
	}

	// Re-assigning an exercise replaces its group instead of duplicating it.
	if err := store.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "banca", MuscleGroup: "triceps"}); err != nil {
		t.Fatal(err)
	}
	groups, err := store.ExerciseGroups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups = %#v, err=%v", groups, err)
	}
	for _, g := range groups {
		if g.Exercise == "banca" && g.MuscleGroup != "triceps" {
			t.Fatalf("re-assignment did not replace the group: %#v", g)
		}
	}
}

func TestStoreDeleteSessionRemovesItsSets(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, _ := store.Start(ctx, training.Session{Date: "2026-08-01"})
	for range 3 {
		if _, _, err := store.AddSet(ctx, training.AddSetInput{SessionID: s.ID, Exercise: "banca", WeightKG: 80, Reps: 5, RPE: 8}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.DeleteSession(ctx, s.ID)
	if err != nil || n != 3 {
		t.Fatalf("DeleteSession() = %d, %v, want 3", n, err)
	}
	if _, err := store.GetSession(ctx, s.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("session still present: %v", err)
	}
	if _, err := store.DeleteSession(ctx, s.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("second delete error = %v, want not found", err)
	}
}

func TestStoreExerciseHistoryOrdersNewestFirstAndFindsTheRecord(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	old, _ := store.Start(ctx, training.Session{Date: "2026-07-01"})
	recent, _ := store.Start(ctx, training.Session{Date: "2026-08-01"})
	if err := store.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "banca", MuscleGroup: "pecho"}); err != nil {
		t.Fatal(err)
	}
	for _, in := range []training.AddSetInput{
		{SessionID: old.ID, Exercise: "banca", WeightKG: 100, Reps: 5, RPE: 9},   // e1RM 116.7 <- record
		{SessionID: recent.ID, Exercise: "banca", WeightKG: 90, Reps: 5, RPE: 8}, // e1RM 105
		{SessionID: recent.ID, Exercise: "remo", WeightKG: 60, Reps: 8, RPE: 8},
	} {
		if _, _, err := store.AddSet(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ExerciseHistory(ctx, "banca", 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.MuscleGroup != "pecho" || len(got.Sets) != 2 {
		t.Fatalf("history = %#v", got)
	}
	if got.Sets[0].Date != "2026-08-01" {
		t.Fatalf("sets are not newest first: %#v", got.Sets)
	}
	if got.Sets[0].Est1RM != 105 {
		t.Fatalf("est 1RM = %v, want 105", got.Sets[0].Est1RM)
	}
	// The record comes from the whole history, not just the newest page.
	if got.Best == nil || got.Best.Date != "2026-07-01" || got.Best.Est1RM != 116.7 {
		t.Fatalf("best = %#v, want the 100x5 set", got.Best)
	}
	// A limit truncates the returned sets but must not change the record.
	limited, err := store.ExerciseHistory(ctx, "banca", 1)
	if err != nil || len(limited.Sets) != 1 || limited.Best == nil || limited.Best.Est1RM != 116.7 {
		t.Fatalf("limited = %#v, err=%v", limited, err)
	}
	empty, err := store.ExerciseHistory(ctx, "nunca hecho", 10)
	if err != nil || len(empty.Sets) != 0 || empty.Best != nil {
		t.Fatalf("unknown exercise = %#v, err=%v", empty, err)
	}
}

func TestStoreWeeklyVolumeBucketsByMondayOfTheTrainingWeek(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "banca", MuscleGroup: "pecho"}); err != nil {
		t.Fatal(err)
	}
	// 2026-08-05 is a Wednesday, 2026-08-09 the Sunday of the same week,
	// 2026-08-10 the Monday of the next one.
	for _, d := range []string{"2026-08-05", "2026-08-09", "2026-08-10"} {
		s, _ := store.Start(ctx, training.Session{Date: d})
		if _, _, err := store.AddSet(ctx, training.AddSetInput{SessionID: s.ID, Exercise: "banca", WeightKG: 80, Reps: 5, RPE: 8}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.WeeklyVolume(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []training.WeeklyVolume{
		{WeekStart: "2026-08-10", MuscleGroup: "pecho", TotalSI: 1, Sets: 1},
		{WeekStart: "2026-08-03", MuscleGroup: "pecho", TotalSI: 2, Sets: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WeeklyVolume() = %#v, want %#v", got, want)
	}
}

func TestStorePlanRoundTripAndSessionProgress(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := store.CreatePlan(ctx, training.Plan{
		Name:  "Empuje A",
		Notes: "semana 1",
		Items: []training.PlanItem{
			{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9},
			{Exercise: "laterales polea", TargetSets: 4, RepMin: 12, RepMax: 15, TargetRPE: 9},
		},
	})
	if err != nil || plan.ID == 0 || plan.TotalSets != 7 {
		t.Fatalf("CreatePlan() = %#v, %v", plan, err)
	}
	// Positions are assigned from the given order.
	if plan.Items[0].Position != 1 || plan.Items[1].Position != 2 {
		t.Fatalf("positions not assigned: %#v", plan.Items)
	}

	got, err := store.GetPlan(ctx, plan.ID)
	if err != nil || got.Name != "Empuje A" || len(got.Items) != 2 || got.TotalSets != 7 {
		t.Fatalf("GetPlan() = %#v, %v", got, err)
	}
	if got.Items[0].RepMin != 8 || got.Items[0].RepMax != 10 || got.Items[0].TargetRPE != 9 {
		t.Fatalf("prescription lost: %#v", got.Items[0])
	}

	listed, err := store.ListPlans(ctx)
	if err != nil || len(listed) != 1 || listed[0].TotalSets != 7 {
		t.Fatalf("ListPlans() = %#v, %v", listed, err)
	}

	// A session started from the plan snapshots its name.
	session, err := store.Start(ctx, training.Session{Date: "2026-08-08", PlanID: plan.ID})
	if err != nil || session.PlanName != "Empuje A" {
		t.Fatalf("Start() = %#v, %v", session, err)
	}
	for range 2 {
		if _, _, err := store.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 9, RPE: 9}); err != nil {
			t.Fatal(err)
		}
	}
	// One exercise done that the plan never prescribed.
	if _, _, err := store.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "curl", WeightKG: 10, Reps: 12, RPE: 8}); err != nil {
		t.Fatal(err)
	}

	progress, err := store.SessionProgress(ctx, session.ID)
	if err != nil || len(progress) != 3 {
		t.Fatalf("SessionProgress() = %#v, %v", progress, err)
	}
	if progress[0].Exercise != "banca" || progress[0].DoneSets != 2 || progress[0].TargetSets != 3 || progress[0].Done() {
		t.Fatalf("banca progress = %#v, want 2/3 incomplete", progress[0])
	}
	if progress[1].Exercise != "laterales polea" || progress[1].DoneSets != 0 {
		t.Fatalf("untouched plan item = %#v", progress[1])
	}
	// Off-plan work is visible, not hidden.
	if progress[2].Exercise != "curl" || progress[2].TargetSets != 0 || progress[2].DoneSets != 1 {
		t.Fatalf("off-plan exercise = %#v", progress[2])
	}

	// Deleting the plan must not rewrite the session's recorded plan name.
	if err := store.DeletePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSession(ctx, session.ID)
	if err != nil || after.PlanName != "Empuje A" {
		t.Fatalf("session lost its plan name after the plan was deleted: %#v", after)
	}
	if _, err := store.GetPlan(ctx, plan.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("GetPlan after delete = %v, want not found", err)
	}
}

func TestStoreMigrationsAreAppliedOnceAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/training.db"
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlan(context.Background(), training.Plan{
		Name:  "p",
		Items: []training.PlanItem{{Exercise: "banca", TargetSets: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	// Reopening must not fail: migration 003 uses ALTER TABLE, which would
	// error if migrations were replayed instead of version-tracked.
	store, err = Open(path)
	if err != nil {
		t.Fatalf("reopen failed, migrations are not idempotent: %v", err)
	}
	defer store.Close()
	plans, err := store.ListPlans(context.Background())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans after reopen = %#v, %v", plans, err)
	}
}

func TestStoreSessionItemsAreASnapshotOfThePlan(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := store.CreatePlan(ctx, training.Plan{
		Name: "Empuje A",
		Items: []training.PlanItem{
			{Exercise: "banca", TargetSets: 4, RepMin: 8, RepMax: 10, TargetRPE: 9},
			{Exercise: "aperturas", TargetSets: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Start(ctx, training.Session{Date: "2026-08-08", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Fewer sets today.
	if err := store.PatchSessionItem(ctx, session.ID, "banca", training.SessionItemPatch{TargetSets: intPtr(3)}); err != nil {
		t.Fatal(err)
	}
	// Machine taken: substitute, keeping the prescription.
	if err := store.SwapSessionItem(ctx, session.ID, "aperturas", "contractora pecho"); err != nil {
		t.Fatal(err)
	}
	progress, err := store.SessionProgress(ctx, session.ID)
	if err != nil || len(progress) != 2 {
		t.Fatalf("progress = %#v, %v", progress, err)
	}
	if progress[0].TargetSets != 3 || progress[0].RepMin != 8 || progress[0].TargetRPE != 9 {
		t.Fatalf("adjusted item lost its prescription: %#v", progress[0])
	}
	if progress[1].Exercise != "contractora pecho" || progress[1].TargetSets != 3 {
		t.Fatalf("swap did not keep position and target: %#v", progress[1])
	}

	// The template is untouched by any of it.
	saved, err := store.GetPlan(ctx, plan.ID)
	if err != nil || saved.Items[0].TargetSets != 4 || saved.Items[1].Exercise != "aperturas" {
		t.Fatalf("template was mutated by session edits: %#v, %v", saved.Items, err)
	}

	// Skipping keeps the exercise listed, so the decision is recorded.
	if err := store.PatchSessionItem(ctx, session.ID, "banca", training.SessionItemPatch{Skipped: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	progress, _ = store.SessionProgress(ctx, session.ID)
	if !progress[0].Skipped {
		t.Fatalf("skip not recorded: %#v", progress[0])
	}

	// Removing an item leaves its logged sets, which resurface as off-plan.
	if _, _, err := store.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveSessionItem(ctx, session.ID, "banca"); err != nil {
		t.Fatal(err)
	}
	progress, _ = store.SessionProgress(ctx, session.ID)
	var offPlan *training.PlanProgress
	for i := range progress {
		if progress[i].Exercise == "banca" {
			offPlan = &progress[i]
		}
	}
	if offPlan == nil || offPlan.TargetSets != 0 || offPlan.DoneSets != 1 {
		t.Fatalf("removed exercise should resurface as off-plan work: %#v", progress)
	}
	if err := store.RemoveSessionItem(ctx, session.ID, "no existe"); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("removing an absent item = %v, want not found", err)
	}
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func TestStoreFeedbackUpsertsAndReportsTheLatestPerGroup(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	older, _ := store.Start(ctx, training.Session{Date: "2026-08-01"})
	newer, _ := store.Start(ctx, training.Session{Date: "2026-08-08"})

	if err := store.SetFeedback(ctx, older.ID, training.Feedback{MuscleGroup: "pecho", Fatigue: 1, Pump: 1, Recovery: 1}); err != nil {
		t.Fatal(err)
	}
	// Rating the same group twice in one session replaces, never duplicates.
	if err := store.SetFeedback(ctx, older.ID, training.Feedback{MuscleGroup: "pecho", Fatigue: 2, Pump: 2, Recovery: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := store.SessionFeedback(ctx, older.ID)
	if err != nil || len(got) != 1 || got[0].Magnitude() != 6 {
		t.Fatalf("SessionFeedback() = %#v, %v", got, err)
	}

	if err := store.SetFeedback(ctx, newer.ID, training.Feedback{MuscleGroup: "pecho", Fatigue: 3, Pump: 3, Recovery: 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeedback(ctx, newer.ID, training.Feedback{MuscleGroup: "espalda", Fatigue: 0, Pump: 0, Recovery: 0}); err != nil {
		t.Fatal(err)
	}
	latest, err := store.LatestFeedback(ctx)
	if err != nil || len(latest) != 2 {
		t.Fatalf("LatestFeedback() = %#v, %v", latest, err)
	}
	// The newest rating wins, not the first one recorded.
	if latest["pecho"].Magnitude() != 9 || latest["espalda"].Magnitude() != 0 {
		t.Fatalf("latest = %#v", latest)
	}
	if err := store.SetFeedback(ctx, 9999, training.Feedback{MuscleGroup: "pecho"}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("feedback for a missing session = %v, want not found", err)
	}
}

func TestStoreUpdateSetRecalculatesAndCarriesTechnique(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, _ := store.Start(ctx, training.Session{Date: "2026-08-08"})
	set, _, err := store.AddSet(ctx, training.AddSetInput{
		SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpe, tech := 10.0, "drop set"
	updated, total, err := store.UpdateSet(ctx, set.ID, training.SetPatch{RPE: &rpe, Technique: &tech})
	if err != nil || updated.SI != 1.4 || total != 1.4 || updated.Technique != "drop set" {
		t.Fatalf("UpdateSet() = %#v, %v, %v", updated, total, err)
	}
	// Untouched fields survive a partial patch.
	if updated.WeightKG != 80 || updated.Reps != 8 || updated.Exercise != "banca" {
		t.Fatalf("partial patch clobbered other fields: %#v", updated)
	}
	if _, _, err := store.UpdateSet(ctx, 9999, training.SetPatch{RPE: &rpe}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("updating a missing set = %v, want not found", err)
	}
}

func TestStoreSessionItemsAddedAdHocAndSupersetLookup(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, _ := store.Start(ctx, training.Session{Date: "2026-08-08"})

	// An exercise added to a session that follows no plan at all.
	if err := store.SetSessionItem(ctx, session.ID, training.PlanItem{
		Exercise: "curl", TargetSets: 3, RepMin: 10, RepMax: 12, Superset: "A", Notes: "control",
	}); err != nil {
		t.Fatal(err)
	}
	// Re-adding replaces the prescription rather than duplicating the row.
	if err := store.SetSessionItem(ctx, session.ID, training.PlanItem{
		Exercise: "curl", TargetSets: 5, Superset: "B",
	}); err != nil {
		t.Fatal(err)
	}
	progress, err := store.SessionProgress(ctx, session.ID)
	if err != nil || len(progress) != 1 || progress[0].TargetSets != 5 || progress[0].Superset != "B" {
		t.Fatalf("progress = %#v, %v", progress, err)
	}

	label, err := store.SupersetFor(ctx, session.ID, "curl")
	if err != nil || label != "B" {
		t.Fatalf("SupersetFor() = %q, %v, want B", label, err)
	}
	// An exercise not in the session's plan carries no round, and that is not
	// an error — most exercises are standalone.
	label, err = store.SupersetFor(ctx, session.ID, "desconocido")
	if err != nil || label != "" {
		t.Fatalf("SupersetFor(unknown) = %q, %v", label, err)
	}
	if err := store.SetSessionItem(ctx, 9999, training.PlanItem{Exercise: "x", TargetSets: 1}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("adding to a missing session = %v, want not found", err)
	}
}

func TestStoreExerciseNotesRoundTripAndDeleteOnEmpty(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.SetExerciseNote(ctx, training.ExerciseNote{Exercise: "prensa", Note: "asiento 4"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetExerciseNote(ctx, training.ExerciseNote{Exercise: "prensa", Note: "asiento 5"}); err != nil {
		t.Fatal(err)
	}
	notes, err := store.ExerciseNotes(ctx)
	if err != nil || len(notes) != 1 || notes[0].Note != "asiento 5" {
		t.Fatalf("notes = %#v, %v", notes, err)
	}
	// An empty note removes the row rather than storing emptiness.
	if err := store.SetExerciseNote(ctx, training.ExerciseNote{Exercise: "prensa", Note: "  "}); err != nil {
		t.Fatal(err)
	}
	if notes, _ := store.ExerciseNotes(ctx); len(notes) != 0 {
		t.Fatalf("empty note should delete: %#v", notes)
	}
}

func TestStoreUpdatePlanLeavesExercisesAlone(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := store.CreatePlan(ctx, training.Plan{
		Name: "Empuje", Notes: "v1",
		Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	name := "Empuje A"
	if err := store.UpdatePlan(ctx, plan.ID, training.PlanPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPlan(ctx, plan.ID)
	if err != nil || got.Name != "Empuje A" || got.Notes != "v1" || len(got.Items) != 1 {
		t.Fatalf("GetPlan() = %#v, %v", got, err)
	}
	notes := "v2"
	if err := store.UpdatePlan(ctx, plan.ID, training.PlanPatch{Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetPlan(ctx, plan.ID); got.Notes != "v2" || got.Name != "Empuje A" {
		t.Fatalf("patching notes changed the name: %#v", got)
	}
	if err := store.UpdatePlan(ctx, 9999, training.PlanPatch{Notes: &notes}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("updating a missing plan = %v, want not found", err)
	}
}

func TestStoreSnapshotProducesAReadableCopy(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, _ := store.Start(ctx, training.Session{Date: "2026-08-08"})
	if _, _, err := store.AddSet(ctx, training.AddSetInput{
		SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 9,
	}); err != nil {
		t.Fatal(err)
	}

	copyPath := dir + "/backup.db"
	if err := store.Snapshot(ctx, copyPath); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	// A snapshot that cannot be opened and read is not a backup.
	restored, err := Open(copyPath)
	if err != nil {
		t.Fatalf("snapshot does not open: %v", err)
	}
	defer restored.Close()
	got, err := restored.GetSession(ctx, session.ID)
	if err != nil || len(got.Sets) != 1 || got.Sets[0].Exercise != "banca" {
		t.Fatalf("restored session = %#v, %v", got, err)
	}
	// A path that could break out of the quoted SQL literal is refused.
	if err := store.Snapshot(ctx, dir+"/ev'il.db"); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("quoted path = %v, want validation", err)
	}
}

func TestStorePlanItemsCanBeEditedReorderedAndRemoved(t *testing.T) {
	store, err := Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := store.CreatePlan(ctx, training.Plan{
		Name:  "Empuje",
		Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range []string{"aperturas", "laterales"} {
		if err := store.SetPlanItem(ctx, plan.ID, training.PlanItem{Exercise: ex, TargetSets: 3}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.GetPlan(ctx, plan.ID)
	if len(got.Items) != 3 || got.Items[2].Exercise != "laterales" {
		t.Fatalf("new items should go last: %#v", got.Items)
	}

	// Re-adding replaces the prescription rather than duplicating the row.
	if err := store.SetPlanItem(ctx, plan.ID, training.PlanItem{Exercise: "banca", TargetSets: 5, Superset: "A"}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetPlan(ctx, plan.ID)
	if len(got.Items) != 3 || got.Items[0].TargetSets != 5 || got.Items[0].Superset != "A" {
		t.Fatalf("re-add should replace in place: %#v", got.Items)
	}

	if err := store.MovePlanItem(ctx, plan.ID, "laterales", -1); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetPlan(ctx, plan.ID)
	if got.Items[1].Exercise != "laterales" || got.Items[2].Exercise != "aperturas" {
		t.Fatalf("move up did not swap: %#v", got.Items)
	}
	// Moving past the end is a no-op, not an error.
	if err := store.MovePlanItem(ctx, plan.ID, "banca", -1); err != nil {
		t.Fatalf("moving the first item up = %v, want no-op", err)
	}

	// Removing closes the gap so positions stay dense.
	if err := store.RemovePlanItem(ctx, plan.ID, "laterales"); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetPlan(ctx, plan.ID)
	if len(got.Items) != 2 || got.Items[0].Position != 1 || got.Items[1].Position != 2 {
		t.Fatalf("positions should be resequenced: %#v", got.Items)
	}

	for _, tt := range []struct {
		name string
		err  error
	}{
		{"remove missing", store.RemovePlanItem(ctx, plan.ID, "no existe")},
		{"move missing", store.MovePlanItem(ctx, plan.ID, "no existe", 1)},
		{"add to missing plan", store.SetPlanItem(ctx, 9999, training.PlanItem{Exercise: "x", TargetSets: 1})},
	} {
		if !errors.Is(tt.err, training.ErrNotFound) {
			t.Fatalf("%s = %v, want not found", tt.name, tt.err)
		}
	}
}
