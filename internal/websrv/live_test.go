package websrv_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

func TestRestTargetIsStoredAndRendered(t *testing.T) {
	h, service := newServer(t)
	if body := get(t, h, base+"/").Body.String(); !strings.Contains(body, `data-rest-target="120"`) {
		t.Fatalf("page should carry the default rest target: %q", body)
	}

	w := post(t, h, base+"/rest", url.Values{"seconds": {"90"}}, true)
	var got struct {
		RestSeconds int `json:"rest_seconds"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.RestSeconds != 90 {
		t.Fatalf("POST /rest = %d %q", w.Code, w.Body)
	}
	if body := get(t, h, base+"/").Body.String(); !strings.Contains(body, `data-rest-target="90"`) {
		t.Fatalf("page should carry the stored rest target: %q", body)
	}

	for _, bad := range []string{"10", "901", "x", ""} {
		if w := post(t, h, base+"/rest", url.Values{"seconds": {bad}}, true); w.Code != http.StatusBadRequest {
			t.Fatalf("POST /rest %q = %d, want 400", bad, w.Code)
		}
	}
	if v, err := service.RestTarget(t.Context()); err != nil || v != 90 {
		t.Fatalf("a rejected value changed the target: %d, %v", v, err)
	}
}

func TestPanelCarriesTheLastSetAndWhatComesNext(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service,
		training.PlanItem{Exercise: "banca", TargetSets: 1},
		training.PlanItem{Exercise: "remo", TargetSets: 2, RepMin: 10, RepMax: 12})

	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, `data-rest-state="none"`) || !strings.Contains(body, `data-next="banca"`) {
		t.Fatalf("before any set the plan's first exercise is next and nothing rests: %q", body)
	}

	panel := post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true).Body.String()
	live, err := service.LiveWorkout(t.Context())
	if err != nil || live.LastSet == nil {
		t.Fatalf("LiveWorkout() = %+v, %v", live, err)
	}
	for _, want := range []string{
		`data-last-set-id="` + itoa(live.LastSet.Set.ID) + `"`,
		`data-rest-state="` + live.Rest.State + `"`,
		`data-next="remo"`,
		`data-next-rx="10-12 reps"`,
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel lacks %s: %q", want, panel)
		}
	}
}

func TestExerciseCanBeAddedToTodaysPlanFromThePhone(t *testing.T) {
	h, _ := newServer(t)
	// No plan and no session yet: adding the first exercise creates both.
	w := post(t, h, base+"/plan/item", url.Values{
		"action": {"add"}, "exercise": {" Face Pull "}, "target_sets": {"3"},
		"rep_min": {"12"}, "rep_max": {"15"}, "target_rpe": {"8"},
	}, true)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `data-exercise="face pull"`) ||
		!strings.Contains(body, `<span class="count">0/3`) || !strings.Contains(body, "12-15 reps @8") {
		t.Fatalf("added exercise should be in today's plan: %d %q", w.Code, body)
	}

	bad := post(t, h, base+"/plan/item", url.Values{"action": {"add"}, "exercise": {"remo"}, "target_sets": {"0"}}, true)
	if !strings.Contains(bad.Body.String(), `class="alert"`) || strings.Contains(bad.Body.String(), `data-exercise="remo"`) {
		t.Fatalf("an invalid prescription should be refused inline: %q", bad.Body)
	}
}

func TestPrescriptionCanBeAdjustedFromThePhone(t *testing.T) {
	h, service := newServer(t)
	plan := startPlan(t, h, service, training.PlanItem{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 8})

	body := post(t, h, base+"/plan/item", url.Values{
		"action": {"rx"}, "exercise": {"banca"}, "rep_min": {"6"}, "rep_max": {"8"}, "target_rpe": {"9"},
	}, true).Body.String()
	if !strings.Contains(body, "6-8 reps @9") || !strings.Contains(body, `<span class="count">0/3`) {
		t.Fatalf("rep range and RPE should change, the set count stay: %q", body)
	}
	if saved, err := service.GetPlan(t.Context(), plan.ID); err != nil || saved.Items[0].RepMin != 8 {
		t.Fatalf("the template changed: %+v, %v", saved, err)
	}

	bad := post(t, h, base+"/plan/item", url.Values{
		"action": {"rx"}, "exercise": {"banca"}, "rep_min": {"12"}, "rep_max": {"8"},
	}, true).Body.String()
	if !strings.Contains(bad, `class="alert"`) || !strings.Contains(bad, "6-8 reps @9") {
		t.Fatalf("an inverted range should be refused and leave the prescription: %q", bad)
	}
}

func TestSetExerciseCanBeCorrectedFromThePhone(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("banca", "80", "5", "8"), true)
	live, _ := service.LiveWorkout(t.Context())
	id := itoa(live.LastSet.Set.ID)

	body := post(t, h, base+"/sets/"+id+"/update", url.Values{"exercise": {"Press Inclinado"}}, true).Body.String()
	if !strings.Contains(body, `<span class="ex">press inclinado</span>`) || !strings.Contains(body, "80 kg × 5 @8") {
		t.Fatalf("the set should move to the corrected exercise keeping its numbers: %q", body)
	}
}

func TestFreeSessionShowsNoPlanCard(t *testing.T) {
	h, _ := newServer(t)
	body := post(t, h, base+"/sets", setForm("banca", "80", "5", "8"), true).Body.String()
	if strings.Contains(body, `class="plan card"`) || strings.Contains(body, "data-next=\"banca\"") {
		t.Fatalf("a session without planned exercises has no plan card and nothing next: %q", body)
	}
}

// Adding an exercise that is already in today's plan from the phone keeps its
// superset and notes, which the add form never sends. Issue #54.
func TestReAddingAnExerciseFromThePhoneKeepsItsSupersetAndNotes(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service, training.PlanItem{Exercise: "curl", TargetSets: 3, Superset: "A", Notes: "control"})

	w := post(t, h, base+"/plan/item", url.Values{"action": {"add"}, "exercise": {"curl"}, "target_sets": {"4"}}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /plan/item = %d %q", w.Code, w.Body)
	}
	live, err := service.LiveWorkout(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	progress, err := service.SessionProgress(t.Context(), live.Session.ID)
	if err != nil || len(progress) != 1 {
		t.Fatalf("progress = %#v, %v", progress, err)
	}
	if got := progress[0]; got.TargetSets != 4 || got.Superset != "A" || got.Notes != "control" {
		t.Fatalf("re-add from the phone clobbered superset or notes: %#v", got)
	}
}
