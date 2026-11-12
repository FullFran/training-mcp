package websrv_test

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// A routine is a template: every change made from the workout layer edits
// today's session only, as add/adjust/swap/remove_session_exercise do over MCP.
func TestEveryInSessionChangeEditsTodayOnlyAndNeverTheRoutine(t *testing.T) {
	h, service := newServer(t)
	plan := startPlan(t, h, service,
		training.PlanItem{Exercise: "banca", TargetSets: 4, RepMin: 8, RepMax: 10, TargetRPE: 9},
		training.PlanItem{Exercise: "aperturas", TargetSets: 3, RepMin: 12, RepMax: 15, TargetRPE: 8},
		training.PlanItem{Exercise: "fondos", TargetSets: 2},
		training.PlanItem{Exercise: "triceps polea", TargetSets: 3})
	template, err := service.GetPlan(t.Context(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Off-plan work, for adopt.
	post(t, h, base+"/sets", setForm("curl", "12", "10", "8"), true)

	item := func(exercise string) (training.PlanProgress, bool) {
		t.Helper()
		live, err := service.LiveWorkout(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range live.Progress {
			if p.Exercise == exercise {
				return p, true
			}
		}
		return training.PlanProgress{}, false
	}
	changes := []struct {
		name  string
		form  url.Values
		check func() bool
	}{
		{"sets up", url.Values{"action": {"sets"}, "delta": {"1"}, "current": {"4"}, "exercise": {"banca"}},
			func() bool { p, _ := item("banca"); return p.TargetSets == 5 }},
		{"sets down", url.Values{"action": {"sets"}, "delta": {"-1"}, "current": {"3"}, "exercise": {"aperturas"}},
			func() bool { p, _ := item("aperturas"); return p.TargetSets == 2 }},
		{"rx", url.Values{"action": {"rx"}, "exercise": {"banca"}, "rep_min": {"6"}, "rep_max": {"8"}, "target_rpe": {"8.5"}},
			func() bool { p, _ := item("banca"); return p.RepMin == 6 && p.RepMax == 8 && p.TargetRPE == 8.5 }},
		{"skip", url.Values{"action": {"skip"}, "exercise": {"fondos"}},
			func() bool { p, _ := item("fondos"); return p.Skipped }},
		{"swap", url.Values{"action": {"swap"}, "exercise": {"aperturas"}, "replacement": {"cruce poleas"}},
			func() bool {
				_, old := item("aperturas")
				p, ok := item("cruce poleas")
				return !old && ok && p.TargetSets == 2
			}},
		{"remove", url.Values{"action": {"remove"}, "exercise": {"triceps polea"}},
			func() bool { _, ok := item("triceps polea"); return !ok }},
		{"add", url.Values{"action": {"add"}, "exercise": {"press militar"}, "target_sets": {"3"}, "rep_min": {"8"}, "rep_max": {"12"}, "target_rpe": {"8"}},
			func() bool { p, ok := item("press militar"); return ok && p.TargetSets == 3 && p.RepMax == 12 }},
		{"adopt", url.Values{"action": {"adopt"}, "exercise": {"curl"}, "done": {"1"}},
			func() bool { p, ok := item("curl"); return ok && p.TargetSets == 1 }},
	}
	for _, c := range changes {
		post(t, h, base+"/plan/item", c.form, true)
		if !c.check() {
			t.Fatalf("%s did not change today's session", c.name)
		}
		saved, err := service.GetPlan(t.Context(), plan.ID)
		if err != nil || !reflect.DeepEqual(saved, template) {
			t.Fatalf("%s changed the routine:\nbefore %#v\nafter  %#v (%v)", c.name, template, saved, err)
		}
	}
}
