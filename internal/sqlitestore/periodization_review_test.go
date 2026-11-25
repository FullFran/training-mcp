package sqlitestore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const benchPress = "barbell bench press - medium grip"

// reviewWorld is a block of two microcycles over the routines empuje (bench
// press 3 sets at RPE 8, curl 2 sets with no RPE) and tiron (remo 3 sets at
// RPE 7). The first microcycle adds one set and half an RPE point. Empuje is
// done in it with an imputed RPE, a warm-up and extra rows, an off-plan
// session is logged the same day, and the microcycle is closed by hand with
// tiron undone; tiron is then done in the second microcycle.
type reviewWorld struct {
	*periodWorld
	blk                   training.BlockView
	offPlan, first, other int64
}

func newReviewWorld(t *testing.T) *reviewWorld {
	ctx := context.Background()
	w := &reviewWorld{periodWorld: newPeriodWorld(t)}
	svc := w.svc
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []training.LibraryEntryInput{
		{Name: "remo", Primary: []string{"espalda"}, Secondary: []string{"biceps"}},
		{Name: "curl", Primary: []string{"biceps"}},
	} {
		_, err := svc.SaveExerciseEntry(ctx, e)
		must(err)
	}
	for ex, g := range map[string]string{benchPress: "pecho", "remo": "espalda", "curl": "biceps"} {
		must(svc.SetExerciseGroup(ctx, ex, g))
	}
	for name, items := range map[string][]training.PlanItem{
		"empuje": {{Exercise: benchPress, TargetSets: 3, TargetRPE: 8}, {Exercise: "curl", TargetSets: 2}},
		"tiron":  {{Exercise: "remo", TargetSets: 3, TargetRPE: 7}},
	} {
		p, err := svc.CreatePlan(ctx, training.Plan{Name: name, Items: items})
		must(err)
		w.plans[name] = p.ID
	}
	w.day("2026-11-10")
	_, err := svc.LogBodyweight(ctx, training.BodyweightInput{Date: "2026-11-10", Weight: 81, Unit: "kg"})
	must(err)
	b := w.block(training.BlockInput{Intent: strPtr("acumular"), MicrocycleCount: intPtr(2), Rotation: w.rotation("empuje", "tiron"),
		Microcycles: []training.MicrocycleInput{{Position: 1, Label: strPtr("carga"), SetsDelta: intPtr(1), EffortDelta: f64Ptr(0.5)}}})
	if _, err = svc.AdvanceBlock(ctx, training.AdvanceInput{BlockID: b.ID, Action: training.BlockActivate, StartDate: "2026-11-10"}); err != nil {
		t.Fatal(err)
	}

	w.day("2026-11-16")
	free, err := svc.StartSession(ctx, "")
	must(err)
	w.offPlan = free.ID
	for range 2 {
		_, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: free.ID, Exercise: "sentadilla", WeightKG: 100, Reps: 5, RPE: 8})
		must(err)
	}
	_, err = svc.AssignSessionBlock(ctx, training.AssignInput{SessionID: free.ID, OffPlan: true})
	must(err)

	w.first = w.start("", "empuje").SessionID
	for _, in := range []training.AddSetInput{
		{Exercise: benchPress, WeightKG: 40, Reps: 10, RPE: 5, Category: training.SetWarmup},
		{Exercise: benchPress, WeightKG: 80, Reps: 8, RPE: 9},
		{Exercise: benchPress, WeightKG: 80, Reps: 8, RPE: 9.5},
		{Exercise: benchPress, WeightKG: 80, Reps: 7, RPE: training.ImputedRPE, RPEImputed: true},
		{Exercise: "curl", WeightKG: 20, Reps: 10, RPE: 9},
		{Exercise: "curl", WeightKG: 20, Reps: 10, RPE: 9},
		{Exercise: "remo", WeightKG: 60, Reps: 10, RPE: 8},
		{Exercise: "remo", WeightKG: 60, Reps: 10, RPE: 8},
	} {
		in.SessionID = w.first
		_, _, err = svc.AddSet(ctx, in)
		must(err)
	}
	must(svc.RecordFeedback(ctx, w.first, training.Feedback{MuscleGroup: "pecho", Fatigue: 2, Pump: 3, Recovery: 1}))
	_, err = svc.LogBodyweight(ctx, training.BodyweightInput{Date: "2026-11-16", Weight: 80.2, Unit: "kg"})
	must(err)
	w.advance(b.ID, training.BlockCompleteCurrent)

	w.day("2026-11-17")
	w.other = w.start("", "tiron").SessionID
	for range 3 {
		_, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: w.other, Exercise: "remo", WeightKG: 60, Reps: 10, RPE: 7})
		must(err)
	}
	_, err = svc.LogBodyweight(ctx, training.BodyweightInput{Date: "2026-11-17", Weight: 79.9, Unit: "kg"})
	must(err)
	w.blk = w.get(b.ID)
	return w
}

func (w *reviewWorld) review(in training.ReviewInput) training.MicrocycleReview {
	w.t.Helper()
	r, err := w.svc.ReviewMicrocycle(context.Background(), in)
	if err != nil {
		w.t.Fatalf("review %+v: %v", in, err)
	}
	return r
}

func muscles(rows []training.ReviewMuscle) [][3]any {
	out := [][3]any{}
	for _, r := range rows {
		out = append(out, [3]any{r.MuscleGroup, r.PlannedSets, r.DoneSets})
	}
	return out
}

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// The review of a closed microcycle matches the numbers worked out by hand:
// rotation, planned sets from the session's copy and from the skipped routine
// with the modifiers, working sets done, extra work, both muscle counts,
// effort with the imputed RPE left out, off-plan sessions, feedback and
// bodyweight inside the microcycle's window.
func TestReviewMicrocycleMatchesHandComputedNumbers(t *testing.T) {
	w := newReviewWorld(t)
	r := w.review(training.ReviewInput{})

	if r.SelectedBy != training.ReviewLastClosed || r.Microcycle.ID != w.blk.Microcycles[0].ID || r.Previous != nil {
		t.Fatalf("selected %s microcycle %d previous %+v", r.SelectedBy, r.Microcycle.ID, r.Previous)
	}
	wantTotals := training.ReviewTotals{RotationPlanned: 2, RotationDone: 1, RotationSkipped: 1, Sessions: 1,
		OffPlanSessions: 1, PlannedSets: 11, WorkingSetsDone: 5, ExtraSets: 2}
	if r.Totals != wantTotals {
		t.Fatalf("totals = %+v\nwant     %+v", r.Totals, wantTotals)
	}
	wantRotation := []training.ReviewRoutine{
		{PlanID: w.plans["empuje"], PlanName: "empuje", Status: training.RoutineDone, SessionID: w.first},
		{PlanID: w.plans["tiron"], PlanName: "tiron", Status: training.RoutineSkipped},
	}
	if !reflect.DeepEqual(r.Rotation, wantRotation) {
		t.Fatalf("rotation = %+v", r.Rotation)
	}
	wantSessions := []training.ReviewSession{{SessionID: w.first, Date: "2026-11-16", PlanID: w.plans["empuje"], PlanName: "empuje",
		InRotation: true, PlannedSets: 7, WorkingSets: 7, ExtraSets: 2, SetsDelta: 1, EffortDelta: 0.5}}
	if !reflect.DeepEqual(r.Sessions, wantSessions) {
		t.Fatalf("sessions = %+v", r.Sessions)
	}
	wantExercises := []training.ReviewExercise{
		{Exercise: benchPress, MuscleGroup: "pecho", PlannedSets: 4, WorkingSetsDone: 3},
		{Exercise: "remo", MuscleGroup: "espalda", PlannedSets: 4},
		{Exercise: "curl", MuscleGroup: "biceps", PlannedSets: 3, WorkingSetsDone: 2},
	}
	if !reflect.DeepEqual(r.Exercises, wantExercises) || r.ExercisesTotal != 3 {
		t.Fatalf("exercises = %+v (%d)", r.Exercises, r.ExercisesTotal)
	}
	if want := []training.ReviewExtra{{Exercise: "remo", MuscleGroup: "espalda", WorkingSets: 2, Sessions: 1}}; !reflect.DeepEqual(r.ExtraWork, want) {
		t.Fatalf("extra work = %+v", r.ExtraWork)
	}

	// Single group: every set to its exercise's group.
	if got, want := muscles(r.Muscles.SingleGroup), [][3]any{{"pecho", 4.0, 3.0}, {"espalda", 4.0, 2.0}, {"biceps", 3.0, 2.0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("single group = %v", got)
	}
	// Fractional: bench press 1 to pecho, 0.5 to triceps and 0.5 to an
	// uncertain shoulder; remo 1 to espalda and 0.5 to biceps; curl 1 to biceps.
	f := r.Muscles.Fractional
	if f == nil || f.Method != training.FractionalMethod || f.Source != training.FractionalSource {
		t.Fatalf("fractional = %+v", f)
	}
	if got, want := muscles(f.Groups), [][3]any{{"biceps", 5.0, 3.0}, {"pecho", 4.0, 3.0}, {"espalda", 4.0, 2.0}, {"triceps", 2.0, 1.5}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fractional groups = %v", got)
	}
	if len(f.Uncertain) != 1 || f.UncertainTotal != 1 {
		t.Fatalf("uncertain = %+v", f.Uncertain)
	}
	if u := f.Uncertain[0]; u.Exercise != benchPress || u.Role != training.RoleSecondary || u.PlannedSets != 2 || u.DoneSets != 1.5 || len(u.Candidates) == 0 {
		t.Fatalf("uncertain = %+v", u)
	}

	// Effort: two bench sets compared against 8.5; the imputed one is counted
	// apart, and sets with no target (curl, the extra remo) are not compared.
	e := r.Effort
	if e.ComparedSets != 2 || deref(e.MeanTargetRPE) != 8.5 || deref(e.MeanLoggedRPE) != 9.25 || deref(e.MeanDifference) != 0.75 ||
		e.RPEImputedSets != 1 || e.RPEMissingSets != 0 || e.SetsWithoutTarget != 4 {
		t.Fatalf("effort = %+v target %v logged %v diff %v", e, deref(e.MeanTargetRPE), deref(e.MeanLoggedRPE), deref(e.MeanDifference))
	}
	if want := []training.ReviewExerciseEffort{{Exercise: benchPress, ComparedSets: 2, MeanTargetRPE: 8.5, MeanLoggedRPE: 9.25, MeanDifference: 0.75}}; !reflect.DeepEqual(e.Exercises, want) {
		t.Fatalf("effort per exercise = %+v", e.Exercises)
	}

	// Drop-off reuses the per-session read, with its exclusions.
	var bench *training.ReviewDropOff
	for i := range r.DropOff {
		if r.DropOff[i].Exercise == benchPress {
			bench = &r.DropOff[i]
		}
	}
	if bench == nil || bench.SessionID != w.first || r.DropOffTotal != len(r.DropOff) {
		t.Fatalf("drop-off = %+v", r.DropOff)
	}
	warmups := 0
	for _, x := range bench.Excluded {
		if x.Reason == "warmup" {
			warmups = x.Sets
		}
	}
	if warmups != 1 {
		t.Fatalf("bench drop-off exclusions = %+v", bench.Excluded)
	}

	if want := []training.ReviewOffPlan{{SessionID: w.offPlan, Date: "2026-11-16", WorkingSets: 2}}; !reflect.DeepEqual(r.OffPlan, want) || r.OffPlanTotal != 1 {
		t.Fatalf("off-plan = %+v", r.OffPlan)
	}
	if want := []training.ReviewFeedback{{MuscleGroup: "pecho", Sessions: 1, Fatigue: 2, Pump: 3, Recovery: 1}}; !reflect.DeepEqual(r.Feedback, want) {
		t.Fatalf("feedback = %+v", r.Feedback)
	}
	if r.Window.From != "2026-11-10" || r.Window.To != "2026-11-16" {
		t.Fatalf("window = %+v", r.Window)
	}
	bw := r.Bodyweight
	if bw.Count != 2 || bw.FirstDate != "2026-11-10" || bw.LastDate != "2026-11-16" || deref(bw.FirstKG) != 81.0 || deref(bw.LastKG) != 80.2 || deref(bw.ChangeKG) != -0.8 {
		t.Fatalf("bodyweight = %+v first %v last %v change %v", bw, deref(bw.FirstKG), deref(bw.LastKG), deref(bw.ChangeKG))
	}
	if len(r.Notes) == 0 {
		t.Fatal("no provenance notes")
	}
}

// The current microcycle reads its own session's copy, counts the routine not
// yet done as pending at its routine values, and carries the previous
// microcycle's numbers for comparison.
func TestReviewMicrocycleComparesWithThePreviousMicrocycle(t *testing.T) {
	w := newReviewWorld(t)
	second := w.blk.Microcycles[1].ID
	r := w.review(training.ReviewInput{MicrocycleID: second})
	if r.SelectedBy != training.ReviewRequested || r.Block.ID != w.blk.ID || r.Microcycle.ID != second {
		t.Fatalf("selected %s block %d microcycle %d", r.SelectedBy, r.Block.ID, r.Microcycle.ID)
	}
	want := training.ReviewTotals{RotationPlanned: 2, RotationDone: 1, RotationPending: 1, Sessions: 1, PlannedSets: 8, WorkingSetsDone: 3}
	if r.Totals != want {
		t.Fatalf("totals = %+v\nwant     %+v", r.Totals, want)
	}
	if e := r.Effort; e.ComparedSets != 3 || deref(e.MeanDifference) != 0.0 {
		t.Fatalf("effort = %+v", e)
	}
	// The off-plan session belongs to the first microcycle's days only.
	if r.Window.From != "2026-11-17" || r.Window.To != "2026-11-17" || r.OffPlanTotal != 0 {
		t.Fatalf("window %+v off-plan %d", r.Window, r.OffPlanTotal)
	}
	if bw := r.Bodyweight; bw.Count != 1 || deref(bw.ChangeKG) != 0.0 {
		t.Fatalf("bodyweight = %+v", bw)
	}
	p := r.Previous
	if p == nil || p.MicrocycleID != w.blk.Microcycles[0].ID || p.Position != 1 || p.Label != "carga" || p.SetsDelta != 1 {
		t.Fatalf("previous = %+v", p)
	}
	if p.Totals.PlannedSets != 11 || p.Totals.WorkingSetsDone != 5 || p.ComparedSets != 2 || deref(p.MeanDifference) != 0.75 || p.RPEImputedSets != 1 {
		t.Fatalf("previous numbers = %+v", p)
	}
	if got := muscles(p.SingleGroup); !reflect.DeepEqual(got, [][3]any{{"pecho", 4.0, 3.0}, {"espalda", 4.0, 2.0}, {"biceps", 3.0, 2.0}}) {
		t.Fatalf("previous single group = %v", got)
	}
	if len(p.Fractional) != 4 {
		t.Fatalf("previous fractional = %+v", p.Fractional)
	}

	// Asking by block alone gives the same default as asking for nothing.
	if byBlock := w.review(training.ReviewInput{BlockID: w.blk.ID}); byBlock.Microcycle.ID != w.blk.Microcycles[0].ID {
		t.Fatalf("by block = microcycle %d", byBlock.Microcycle.ID)
	}
}

// Bad or foreign ids are refused, and another account sees nothing of this one.
func TestReviewMicrocycleRefusesBadAndForeignIDs(t *testing.T) {
	ctx := context.Background()
	w := newReviewWorld(t)
	mc := w.blk.Microcycles[0].ID

	other, err := w.svc.SaveBlock(ctx, training.BlockInput{Intent: strPtr("otro"), MicrocycleCount: intPtr(1)})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		in   training.ReviewInput
		want error
	}{
		"negative block":              {training.ReviewInput{BlockID: -1}, training.ErrValidation},
		"negative microcycle":         {training.ReviewInput{MicrocycleID: -1}, training.ErrValidation},
		"unknown block":               {training.ReviewInput{BlockID: 9999}, training.ErrNotFound},
		"unknown microcycle":          {training.ReviewInput{MicrocycleID: 9999}, training.ErrNotFound},
		"microcycle of another block": {training.ReviewInput{BlockID: other.ID, MicrocycleID: mc}, training.ErrNotFound},
	} {
		if _, err := w.svc.ReviewMicrocycle(ctx, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}

	stranger := training.NewService(w.store.ForUser(2), func() time.Time { return *w.now })
	for name, in := range map[string]training.ReviewInput{
		"default":            {},
		"owner's block":      {BlockID: w.blk.ID},
		"owner's microcycle": {MicrocycleID: mc},
	} {
		if r, err := stranger.ReviewMicrocycle(ctx, in); !errors.Is(err, training.ErrNotFound) {
			t.Errorf("stranger %s: err = %v, review %+v", name, err, r.Totals)
		}
	}
}
