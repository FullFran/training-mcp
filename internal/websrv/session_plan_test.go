package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// A session opened from Progreso can become a routine there, through the same
// service call save_session_as_plan makes: adjustments and off-plan work
// included, skipped exercises left out.
func TestSavingASessionAsARoutineFromItsPage(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	plan, err := svc.CreatePlan(ctx, training.Plan{Name: "Torso", Items: []training.PlanItem{
		{Exercise: "press banca", TargetSets: 3, RepMin: 6, RepMax: 8, TargetRPE: 8},
		{Exercise: "fondos", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.StartPlannedSession(ctx, "2026-08-05", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	skipped := true
	if err = svc.AdjustSessionItem(ctx, session.ID, "fondos", training.SessionItemPatch{Skipped: &skipped}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "remo", WeightKG: 50, Reps: 10, RPE: 8}); err != nil {
			t.Fatal(err)
		}
	}
	id := strconv.FormatInt(session.ID, 10)

	page := get(t, h, base+"/s/"+id).Body.String()
	if !strings.Contains(page, `action="`+base+`/s/`+id+`/plan"`) || !strings.Contains(page, "Guardar como rutina") || !strings.Contains(page, `value="Torso"`) {
		t.Fatalf("session page offers no save as routine:\n%s", page)
	}

	// No name is refused on the same page, and nothing is created.
	bad := post(t, h, base+"/s/"+id+"/plan", url.Values{"name": {"  "}}, false)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "Ponle un nombre a la rutina.") {
		t.Fatalf("blank name: %d\n%s", bad.Code, bad.Body)
	}

	w := post(t, h, base+"/s/"+id+"/plan", url.Values{"name": {"Torso B"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/plans" {
		t.Fatalf("status %d, location %q", w.Code, w.Header().Get("Location"))
	}
	plans, err := svc.ListPlans(ctx)
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans = %+v, %v", plans, err)
	}
	var saved training.Plan
	for _, p := range plans {
		if p.Name == "Torso B" {
			if saved, err = svc.GetPlan(ctx, p.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := []string{}
	for _, it := range saved.Items {
		got = append(got, it.Exercise+" "+strconv.Itoa(it.TargetSets)+" "+strconv.Itoa(it.RepMin)+"-"+strconv.Itoa(it.RepMax))
	}
	if strings.Join(got, ", ") != "press banca 3 6-8, remo 2 0-0" {
		t.Fatalf("saved routine = %v", got)
	}

	if missing := post(t, h, base+"/s/999999/plan", url.Values{"name": {"x"}}, false); missing.Code != http.StatusNotFound {
		t.Fatalf("missing session status %d", missing.Code)
	}
}
