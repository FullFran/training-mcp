package websrv_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// switchForm returns the form on Entrenar that chooses plan id, or "".
func switchForm(body string, id int64) string {
	at := strings.Index(body, `<input type="hidden" name="plan_id" value="`+itoa(id)+`">`)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(body[:at], "<form")
	if start < 0 || !strings.Contains(body[start:at], `action="`+base+`/plan"`) {
		return ""
	}
	return body[start:at]
}

// Once a routine is started, Entrenar still offers the others: tapping Empezar
// on the routine shown first must not leave the person stuck with it.
func TestAStartedRoutineCanBeSwappedForAnotherWithNothingLogged(t *testing.T) {
	h, service := newServer(t)
	push, err := service.CreatePlan(t.Context(), training.Plan{Name: "Empuje A", Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	pull, err := service.CreatePlan(t.Context(), training.Plan{Name: "Tirón A", Items: []training.PlanItem{{Exercise: "remo", TargetSets: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if w := post(t, h, base+"/plan", url.Values{"plan_id": {itoa(push.ID)}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("Empezar: status %d", w.Code)
	}

	body := get(t, h, base+"/").Body.String()
	if switchForm(body, pull.ID) == "" {
		t.Fatalf("with Empuje A started, Entrenar offers no way to choose Tirón A:\n%s", body)
	}
	if switchForm(body, push.ID) != "" {
		t.Fatal("the routine already running is offered again")
	}

	if w := post(t, h, base+"/plan", url.Values{"plan_id": {itoa(pull.ID)}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("choosing Tirón A: status %d", w.Code)
	}
	live, err := service.LiveWorkout(t.Context())
	if err != nil || live.Session == nil || live.Session.PlanName != "Tirón A" || len(live.Progress) != 1 || live.Progress[0].Exercise != "remo" {
		t.Fatalf("Tirón A did not replace the empty Empuje A: %+v %+v, %v", live.Session, live.Progress, err)
	}
	sessions, err := service.ListSessions(t.Context(), training.ListFilter{From: "2026-08-07", To: "2026-08-07"})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("today's sessions = %+v, %v", sessions, err)
	}
}

// With sets logged, choosing another routine keeps them and brings its
// exercises in, and the page says so before the tap.
func TestChoosingAnotherRoutineWithSetsLoggedKeepsThem(t *testing.T) {
	h, service := newServer(t)
	push, err := service.CreatePlan(t.Context(), training.Plan{Name: "Empuje A", Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	pull, err := service.CreatePlan(t.Context(), training.Plan{Name: "Tirón A", Items: []training.PlanItem{{Exercise: "remo", TargetSets: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(push.ID)}}, false)
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)

	body := get(t, h, base+"/").Body.String()
	if switchForm(body, pull.ID) == "" || !strings.Contains(body, "Tus series de hoy se quedan") {
		t.Fatalf("with sets logged Entrenar must still offer Tirón A and say the sets stay:\n%s", body)
	}
	if w := post(t, h, base+"/plan", url.Values{"plan_id": {itoa(pull.ID)}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("choosing Tirón A: status %d", w.Code)
	}
	live, err := service.LiveWorkout(t.Context())
	if err != nil || live.Session == nil || len(live.Session.Sets) != 1 {
		t.Fatalf("today's set was not kept: %+v, %v", live.Session, err)
	}
	var remo bool
	for _, p := range live.Progress {
		remo = remo || (p.Exercise == "remo" && p.TargetSets == 4)
	}
	if !remo {
		t.Fatalf("Tirón A's exercises were not brought in: %+v", live.Progress)
	}
}
