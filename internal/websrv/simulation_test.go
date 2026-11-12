package websrv_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// mapWeek gives the person exercise groups and landmarks the simulation reads.
func mapWeek(t *testing.T, svc *training.Service) {
	t.Helper()
	ctx := context.Background()
	for exercise, group := range map[string]string{"press banca": "pecho", "remo": "espalda", "sentadilla": "cuadriceps"} {
		if err := svc.SetExerciseGroup(ctx, exercise, group); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []training.Landmarks{{MuscleGroup: "pecho", MEV: 8, MRV: 20}, {MuscleGroup: "espalda", MEV: 4, MRV: 12}, {MuscleGroup: "cuadriceps", MRV: 2}} {
		if err := svc.SetLandmarks(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
}

// A routine draft in Planificar shows its simulated week before it is saved:
// sets per muscle group against the person's MEV and MRV, and the totals.
func TestPlanificarDraftShowsTheSimulatedWeek(t *testing.T) {
	w := newPlanWorld(t)
	mapWeek(t, w.service)
	p := w.propose(t, "create_plan", "plan.create", map[string]any{"name": "Torso", "items": []map[string]any{
		{"exercise": "press banca", "target_sets": 5, "target_rpe": 9},
		{"exercise": "remo", "target_sets": 4},
		{"exercise": "sentadilla", "target_sets": 3},
		{"exercise": "curl araña", "target_sets": 2},
	}})
	body := get(t, w.handler, "/?modo=planificar").Body.String()
	card := section(t, body, `class="chat-card proposal-card draft-card" data-proposal="`+strconv.FormatInt(p.ID, 10)+`"`, "</article>")
	sim := section(t, card, `class="simulation"`, "</section>")
	for _, want := range []string{
		`Volumen semanal`, `1 vez por semana · 14 series · SI 15`,
		`sim-low`, `pecho`, `5 series`, `Por debajo de tu MEV`, `MEV 8 · MRV 20`,
		`sim-ok`, `espalda`, `4 series`, `Dentro de tu rango`, `MEV 4 · MRV 12`,
		`sim-high`, `cuadriceps`, `3 series`, `Por encima de tu MRV`, `MRV 2`,
		`Sin grupo muscular: curl araña`,
	} {
		if !strings.Contains(sim, want) {
			t.Fatalf("simulation lacks %q:\n%s", want, sim)
		}
	}
	if strings.Index(card, `class="simulation"`) > strings.Index(card, `Revisar y guardar`) {
		t.Fatalf("the simulated week must come before the save button:\n%s", card)
	}
}

// The review page shows the simulated week on a pending plan proposal: a new
// routine, and an edit to a saved one simulated on top of what is stored.
func TestReviewShowsTheSimulatedWeekOfPlanProposals(t *testing.T) {
	f := newReviewFixture(t)
	mapWeek(t, f.service)
	ctx := context.Background()
	plan, err := f.service.CreatePlan(ctx, training.Plan{Name: "Torso", Items: []training.PlanItem{
		{Exercise: "press banca", TargetSets: 3}, {Exercise: "remo", TargetSets: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.propose(t, "pierna") // create_plan with 3 sets of sentadilla
	edit := proposeTool(t, f, "set_plan_exercise", "plan.exercise.set", map[string]any{"plan_id": plan.ID, "exercise": "press banca", "target_sets": 10})
	remove := proposeTool(t, f, "remove_plan_exercise", "plan.exercise.remove", map[string]any{"plan_id": plan.ID, "exercise": "remo"})
	memory := proposeTool(t, f, "memory_save", "memory.save", map[string]any{"topic_key": "a", "title": "a", "body": "a"})

	body := get(t, f.handler, "/review").Body.String()
	pending := section(t, body, "<h2>Pendientes</h2>", "</main>")
	cards := strings.Split(pending, `<article class="card proposal"`)[1:]
	byID := map[int64]string{}
	for _, c := range cards {
		for _, p := range []training.Proposal{edit, remove, memory} {
			if strings.Contains(c, `/review/`+strconv.FormatInt(p.ID, 10)+`/accept"`) {
				byID[p.ID] = c
			}
		}
		if strings.Contains(c, "Crear rutina") {
			if !strings.Contains(c, `class="simulation"`) || !strings.Contains(c, "3 series") || !strings.Contains(c, "Por encima de tu MRV") {
				t.Fatalf("new routine card lacks its simulated week:\n%s", c)
			}
		}
	}
	for _, want := range []string{"13 series", "pecho", "10 series", "Dentro de tu rango", "espalda", "3 series"} {
		if !strings.Contains(byID[edit.ID], want) {
			t.Fatalf("edit card lacks %q:\n%s", want, byID[edit.ID])
		}
	}
	removed := byID[remove.ID]
	if !strings.Contains(removed, `class="simulation"`) || strings.Contains(removed, "espalda") || !strings.Contains(removed, "Por debajo de tu MEV") {
		t.Fatalf("remove card should simulate the plan without remo:\n%s", removed)
	}
	if strings.Contains(byID[memory.ID], `class="simulation"`) {
		t.Fatalf("a memory proposal shows a simulation:\n%s", byID[memory.ID])
	}
	if got, _ := f.service.GetPlan(ctx, plan.ID); len(got.Items) != 2 || got.Items[0].TargetSets != 3 {
		t.Fatalf("rendering a simulation changed the plan: %#v", got)
	}
}

func proposeTool(t *testing.T, f reviewFixture, tool, action string, payload map[string]any) training.Proposal {
	t.Helper()
	raw, _ := json.Marshal(payload)
	p, err := f.service.Propose(training.WithAgentSession(context.Background(), coachSession), training.ProposalInput{Tool: tool, ActionType: action, Payload: raw},
		func(ctx context.Context) (json.RawMessage, error) { return f.mcp.Apply(ctx, f.service, tool, raw) })
	if err != nil {
		t.Fatalf("propose %s: %v", tool, err)
	}
	return p
}
