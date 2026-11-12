package training

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func simulationService(t *testing.T) (*Service, *memoryStore) {
	t.Helper()
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC) })
	ctx := context.Background()
	for exercise, group := range map[string]string{
		"press banca": "pecho", "aperturas": "pecho", "remo": "espalda", "sentadilla": "cuadriceps",
	} {
		if err := svc.SetExerciseGroup(ctx, exercise, group); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []Landmarks{
		{MuscleGroup: "pecho", MEV: 8, MRV: 20},
		{MuscleGroup: "espalda", MEV: 10, MRV: 22},
		{MuscleGroup: "cuadriceps", MRV: 6},
	} {
		if err := svc.SetLandmarks(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	return svc, store
}

// Weekly sets per muscle group are the plan's working sets of every exercise
// mapped to that group, times the sessions per week; an exercise without a
// group still counts in the totals and is named as unmapped.
func TestSimulatePlanCountsWeeklySetsPerMuscleGroup(t *testing.T) {
	svc, _ := simulationService(t)
	got, err := svc.SimulatePlan(context.Background(), PlanSimulationInput{
		SessionsPerWeek: 2,
		Items: []PlanItem{
			{Exercise: " Press Banca ", TargetSets: 3, TargetRPE: 9},
			{Exercise: "aperturas", TargetSets: 2},
			{Exercise: "remo", TargetSets: 4, TargetRPE: 10},
			{Exercise: "curl araña", TargetSets: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionsPerWeek != 2 || got.WeeklySets != 22 {
		t.Fatalf("totals = %d sessions, %d sets; want 2 and 22", got.SessionsPerWeek, got.WeeklySets)
	}
	// SI per set reuses SetSI: RPE 9 is 1.2, unstated is the imputed 1.0, RPE 10 is 1.4.
	if want := NormalizeSI(2 * (3*1.2 + 2*1.0 + 4*1.4 + 2*1.0)); got.WeeklySI != want {
		t.Fatalf("WeeklySI = %v, want %v", got.WeeklySI, want)
	}
	groups := map[string]GroupSimulation{}
	var order []string
	for _, g := range got.Groups {
		groups[g.MuscleGroup] = g
		order = append(order, g.MuscleGroup)
	}
	if !reflect.DeepEqual(order, []string{"pecho", "espalda"}) {
		t.Fatalf("groups = %v, want only the trained groups in catalogue order", order)
	}
	if p := groups["pecho"]; p.WeeklySets != 10 || p.WeeklySI != 11.2 || !reflect.DeepEqual(p.Exercises, []string{"press banca", "aperturas"}) {
		t.Fatalf("pecho = %#v", p)
	}
	if e := groups["espalda"]; e.WeeklySets != 8 || e.WeeklySI != 11.2 {
		t.Fatalf("espalda = %#v", e)
	}
	if !reflect.DeepEqual(got.Unmapped, []string{"curl araña"}) {
		t.Fatalf("Unmapped = %v", got.Unmapped)
	}
}

// Each trained group is compared with that person's landmarks: under MEV,
// between MEV and MRV, over MRV, or no landmarks recorded.
func TestSimulatePlanComparesWithLandmarks(t *testing.T) {
	svc, _ := simulationService(t)
	ctx := context.Background()
	if err := svc.SetExerciseGroup(ctx, "curl", "biceps"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.SimulatePlan(ctx, PlanSimulationInput{Items: []PlanItem{
		{Exercise: "press banca", TargetSets: 4}, // pecho 4 < MEV 8
		{Exercise: "remo", TargetSets: 10},       // espalda 10 == MEV 10: inside
		{Exercise: "sentadilla", TargetSets: 7},  // cuadriceps 7 > MRV 6
		{Exercise: "curl", TargetSets: 3},        // biceps: nothing recorded
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]GroupSimulation{
		"pecho":      {MuscleGroup: "pecho", WeeklySets: 4, MEV: 8, MRV: 20, Status: VolumeBelowMEV},
		"espalda":    {MuscleGroup: "espalda", WeeklySets: 10, MEV: 10, MRV: 22, Status: VolumeWithin},
		"cuadriceps": {MuscleGroup: "cuadriceps", WeeklySets: 7, MRV: 6, Status: VolumeAboveMRV},
		"biceps":     {MuscleGroup: "biceps", WeeklySets: 3, Status: VolumeNoLandmarks},
	}
	for _, g := range got.Groups {
		w := want[g.MuscleGroup]
		if g.WeeklySets != w.WeeklySets || g.MEV != w.MEV || g.MRV != w.MRV || g.Status != w.Status {
			t.Errorf("%s = %#v, want %#v", g.MuscleGroup, g, w)
		}
		delete(want, g.MuscleGroup)
	}
	if len(want) != 0 {
		t.Fatalf("missing groups %v", want)
	}
	if got.SessionsPerWeek != 1 {
		t.Fatalf("SessionsPerWeek defaults to %d, want 1", got.SessionsPerWeek)
	}
}

// A saved plan is simulated by id, and an edit (setting or removing one
// exercise) is simulated on top of it without writing anything.
func TestSimulatePlanFromSavedPlanAndEdit(t *testing.T) {
	svc, store := simulationService(t)
	store.plans = []Plan{{ID: 7, Name: "Torso", Items: []PlanItem{
		{Exercise: "press banca", TargetSets: 3, Position: 1},
		{Exercise: "remo", TargetSets: 3, Position: 2},
	}}}
	ctx := context.Background()
	got, err := svc.SimulatePlan(ctx, PlanSimulationInput{PlanID: 7})
	if err != nil || got.PlanID != 7 || got.WeeklySets != 6 {
		t.Fatalf("saved plan = %#v, %v", got, err)
	}
	got, err = svc.SimulatePlan(ctx, PlanSimulationInput{PlanID: 7, Edit: &PlanEdit{Set: &PlanItem{Exercise: "Press Banca", TargetSets: 6}}})
	if err != nil || got.WeeklySets != 9 || got.Groups[0].WeeklySets != 6 {
		t.Fatalf("replace = %#v, %v", got, err)
	}
	got, err = svc.SimulatePlan(ctx, PlanSimulationInput{PlanID: 7, Edit: &PlanEdit{Set: &PlanItem{Exercise: "sentadilla", TargetSets: 5}}})
	if err != nil || got.WeeklySets != 11 {
		t.Fatalf("add = %#v, %v", got, err)
	}
	got, err = svc.SimulatePlan(ctx, PlanSimulationInput{PlanID: 7, Edit: &PlanEdit{Remove: " REMO "}})
	if err != nil || got.WeeklySets != 3 || len(got.Groups) != 1 {
		t.Fatalf("remove = %#v, %v", got, err)
	}
	if len(store.plans[0].Items) != 2 || store.plans[0].Items[0].TargetSets != 3 {
		t.Fatalf("simulation changed the stored plan: %#v", store.plans[0])
	}
	if store.planCreated.Name != "" {
		t.Fatalf("simulation created a plan: %#v", store.planCreated)
	}
}

func TestSimulatePlanRejectsInvalidInput(t *testing.T) {
	svc, store := simulationService(t)
	store.plans = []Plan{{ID: 7, Name: "Torso", Items: []PlanItem{{Exercise: "remo", TargetSets: 3}}}}
	ctx := context.Background()
	for name, in := range map[string]PlanSimulationInput{
		"nothing":             {},
		"plan and items":      {PlanID: 7, Items: []PlanItem{{Exercise: "remo", TargetSets: 1}}},
		"edit without plan":   {Items: []PlanItem{{Exercise: "remo", TargetSets: 1}}, Edit: &PlanEdit{Remove: "remo"}},
		"zero sets":           {Items: []PlanItem{{Exercise: "remo"}}},
		"eight sessions":      {Items: []PlanItem{{Exercise: "remo", TargetSets: 1}}, SessionsPerWeek: 8},
		"negative sessions":   {Items: []PlanItem{{Exercise: "remo", TargetSets: 1}}, SessionsPerWeek: -1},
		"remove unknown item": {PlanID: 7, Edit: &PlanEdit{Remove: "curl"}},
	} {
		if _, err := svc.SimulatePlan(ctx, in); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	if _, err := svc.SimulatePlan(ctx, PlanSimulationInput{PlanID: 99}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing plan: err = %v, want ErrNotFound", err)
	}
}
