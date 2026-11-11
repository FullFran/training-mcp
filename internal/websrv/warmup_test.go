package websrv_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// The log form's warm-up toggle records a warm-up, and the panel marks it.
func TestAddSetFromTheFormRecordsAndMarksAWarmup(t *testing.T) {
	h, service := newServer(t)
	form := setForm("squat", "60", "5", "6")
	form.Set("category", "warmup")
	w := post(t, h, base+"/sets", form, true)
	post(t, h, base+"/sets", setForm("squat", "100", "5", "8"), true)
	sessions, err := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	if err != nil || len(sessions) != 1 || sessions[0].SetCount != 1 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	session, err := service.GetSession(t.Context(), sessions[0].ID)
	if err != nil || session.Sets[0].Category != training.SetWarmup || session.Sets[1].Category != training.SetWorking {
		t.Fatalf("session = %+v, %v", session, err)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-category="warmup"`) || !strings.Contains(body, `class="warmup-tag"`) {
		t.Fatalf("panel does not mark the warm-up: %s", body)
	}
}

// The edit form sends a hidden "working" before the toggle, so an unticked box
// means working and a ticked one means warm-up; a form without the field
// leaves the category alone.
func TestUpdateSetFromTheFormTogglesTheCategory(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("squat", "100", "5", "8"), true)
	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	session, _ := service.GetSession(t.Context(), sessions[0].ID)
	id := strconv.FormatInt(session.Sets[0].ID, 10)
	category := func() string {
		got, err := service.GetSession(t.Context(), session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Sets[0].Category
	}
	post(t, h, base+"/sets/"+id+"/update", url.Values{"category": {"working", "warmup"}}, true)
	if got := category(); got != training.SetWarmup {
		t.Fatalf("ticked = %q", got)
	}
	post(t, h, base+"/sets/"+id+"/update", url.Values{"reps": {"6"}}, true)
	if got := category(); got != training.SetWarmup {
		t.Fatalf("a form without the field changed it to %q", got)
	}
	post(t, h, base+"/sets/"+id+"/update", url.Values{"category": {"working"}}, true)
	if got := category(); got != training.SetWorking {
		t.Fatalf("unticked = %q", got)
	}
}

// A weight stored with more precision than 2 decimals is shown and offered
// rounded, never as a long float.
func TestWeightsAreShownRounded(t *testing.T) {
	h, _ := newServer(t)
	w := post(t, h, base+"/sets", setForm("squat", "65.77089365", "5", "8"), true)
	body := w.Body.String()
	if strings.Contains(body, "65.7708") || !strings.Contains(body, `data-weight="65.77"`) {
		t.Fatalf("weight not rounded: %s", body)
	}
}

// A session holding only warm-ups is not empty: choosing a plan keeps them.
func TestChoosingAPlanKeepsASessionWithOnlyWarmups(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "legs", Items: []training.PlanItem{{Exercise: "squat", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	form := setForm("squat", "60", "5", "6")
	form.Set("category", "warmup")
	post(t, h, base+"/sets", form, true)
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	sessions, err := service.ListSessions(t.Context(), training.ListFilter{Limit: 5})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	session, err := service.GetSession(t.Context(), sessions[0].ID)
	if err != nil || len(session.Sets) != 1 || session.Sets[0].Category != training.SetWarmup {
		t.Fatalf("the warm-up was lost: %+v, %v", session, err)
	}
	// The one-tap weight is today's last working set; a warm-up is not one.
	if page := get(t, h, base+"/").Body.String(); strings.Contains(page, `data-today-weight="60"`) {
		t.Fatalf("one-tap weight offers the warm-up: %s", page)
	}
}

// The panel counts working sets only, and "repeat last" and the one-tap
// weight offer the last working set, never a warm-up.
func TestPanelCountsAndRepeatsWorkingSetsOnly(t *testing.T) {
	h, _ := newServer(t)
	post(t, h, base+"/sets", setForm("squat", "100", "5", "8"), true)
	form := setForm("squat", "60", "5", "6")
	form.Set("category", "warmup")
	body := post(t, h, base+"/sets", form, true).Body.String()
	if !strings.Contains(body, "1 serie ·") {
		t.Fatalf("panel count includes the warm-up: %s", body)
	}
	if !strings.Contains(body, `data-last-weight="100"`) {
		t.Fatalf("repeat last offers the warm-up: %s", body)
	}
}
