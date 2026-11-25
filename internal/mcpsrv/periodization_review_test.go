package mcpsrv

import (
	"reflect"
	"sort"
	"testing"
)

// review_microcycle answers with the review under one key, totals first in
// its shape, and changes no row.
func TestReviewMicrocycleToolReadsTheLastClosedMicrocycle(t *testing.T) {
	w := newIsolationWorld(t)
	if out := structured(t, call(t, w.owner, "advance_block", map[string]any{"block_id": w.ids.block, "action": "activate"})); out["status"] != "accepted" {
		t.Fatalf("activate = %v", out)
	}
	if out := structured(t, call(t, w.owner, "start_session", map[string]any{"date": isolationDay, "plan_id": w.ids.plan})); out["status"] != "accepted" {
		t.Fatalf("start = %v", out)
	}
	before := fullDump(t, w)

	var out ReviewMicrocycleOut
	decode(t, call(t, w.owner, "review_microcycle", map[string]any{}), &out)
	r := out.Review
	if r.SelectedBy != "last_closed" || r.Block.ID != w.ids.block || r.Microcycle.ID != w.ids.microcycle {
		t.Fatalf("selected %s block %d microcycle %d", r.SelectedBy, r.Block.ID, r.Microcycle.ID)
	}
	if r.Totals.RotationDone != 1 || r.Totals.Sessions != 1 || r.Totals.PlannedSets == 0 {
		t.Fatalf("totals = %+v", r.Totals)
	}

	raw := structured(t, call(t, w.owner, "review_microcycle", map[string]any{"microcycle_id": w.ids.microcycle}))
	review, _ := raw["review"].(map[string]any)
	keys := make([]string, 0, len(review))
	for k := range review {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"block", "bodyweight", "dropoff", "dropoff_total", "effort", "exercises", "exercises_total", "extra_work", "extra_work_total",
		"feedback", "microcycle", "muscles", "notes", "off_plan_sessions", "off_plan_total", "rotation", "selected_by", "sessions", "sessions_total",
		"totals", "window"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("review keys = %v", keys)
	}
	if got := fullDump(t, w); got != before {
		t.Fatal("review_microcycle changed rows")
	}
}
