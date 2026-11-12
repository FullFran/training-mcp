package mcpsrv

import (
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// simulate_plan reads the caller's own exercise groups and landmarks: the same
// unsaved items give the owner a compared group and give another person, who
// mapped nothing, only an unmapped exercise. Neither call writes a row or a
// proposal.
func TestSimulatePlanUsesOnlyTheCallersGroupsAndLandmarks(t *testing.T) {
	world := newIsolationWorld(t)
	args := map[string]any{"sessions_per_week": 2, "items": []map[string]any{
		{"exercise": isolationExercise, "target_sets": 1, "target_rpe": 9},
	}}
	before := dumpTables(t, world.db, nil)

	var owner, second SimulatePlanOut
	decode(t, call(t, world.owner, "simulate_plan", args), &owner)
	decode(t, call(t, world.second, "simulate_plan", args), &second)

	sim := owner.Simulation
	if sim.WeeklySets != 2 || sim.WeeklySI != 2.4 || len(sim.Groups) != 1 || len(sim.Unmapped) != 0 {
		t.Fatalf("owner = %#v", sim)
	}
	if g := sim.Groups[0]; g.MuscleGroup != "pecho" || g.WeeklySets != 2 || g.MEV != 4 || g.MRV != 12 || g.Status != training.VolumeBelowMEV {
		t.Fatalf("owner pecho = %#v", g)
	}
	other := second.Simulation
	if other.WeeklySets != 2 || len(other.Groups) != 0 || len(other.Unmapped) != 1 || other.Unmapped[0] != isolationExercise {
		t.Fatalf("second user saw the owner's groups or landmarks: %#v", other)
	}
	if after := dumpTables(t, world.db, nil); after != before {
		t.Fatalf("a simulation changed rows:\nbefore\n%s\nafter\n%s", before, after)
	}
	var proposals int
	if err := world.db.QueryRow(`SELECT COUNT(*) FROM proposals`).Scan(&proposals); err != nil {
		t.Fatal(err)
	}
	if proposals != 0 {
		t.Fatalf("a simulation recorded %d proposals", proposals)
	}
}

func TestSimulatePlanRejectsPlanAndItemsTogether(t *testing.T) {
	world := newIsolationWorld(t)
	result := call(t, world.owner, "simulate_plan", map[string]any{"plan_id": world.ids.plan, "items": []map[string]any{{"exercise": "remo", "target_sets": 1}}})
	if !result.IsError {
		t.Fatalf("plan_id and items together were accepted: %s", answer(t, result))
	}
}
