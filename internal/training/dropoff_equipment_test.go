package training

import "testing"

func dropOffReasons(a DropOffAnalysis) map[int64]string {
	out := map[int64]string{}
	for _, s := range a.Sets {
		out[s.SetID] = s.ExcludedReason
	}
	return out
}

// Two machines sharing an exercise name are not compared: a set on other
// equipment than the reference is excluded as different_equipment.
func TestDropOffExcludesSetsOnOtherEquipmentThanTheReference(t *testing.T) {
	a := AnalyzeDropOff("aperturas", []Set{
		{ID: 1, Position: 1, WeightKG: 100, Reps: 6, RPE: 8, Equipment: "machine a"},
		{ID: 2, Position: 2, WeightKG: 95, Reps: 6, RPE: 8, Equipment: "machine a"},
		{ID: 3, Position: 3, WeightKG: 86, Reps: 6, RPE: 8, Equipment: "machine b"},
		{ID: 4, Position: 4, WeightKG: 86, Reps: 6, RPE: 8},
	})
	got := dropOffReasons(a)
	want := map[int64]string{1: "", 2: "", 3: ExcludedDifferentEquipment, 4: ExcludedDifferentEquipment}
	for id, reason := range want {
		if got[id] != reason {
			t.Fatalf("set %d excluded_reason = %q, want %q (all: %v)", id, got[id], reason, got)
		}
	}
	if ExcludedDifferentEquipment != "different_equipment" {
		t.Fatalf("reason id = %q", ExcludedDifferentEquipment)
	}
	if a.ReferenceSetID != 1 || !a.Comparable || a.Sets[2].Equipment != "machine b" {
		t.Fatalf("analysis = %#v", a)
	}
}

// Sets with no equipment stated stay comparable with each other, as before.
func TestDropOffWithoutEquipmentIsUnchanged(t *testing.T) {
	a := AnalyzeDropOff("remo", []Set{
		{ID: 1, Position: 1, WeightKG: 60, Reps: 10, RPE: 8},
		{ID: 2, Position: 2, WeightKG: 60, Reps: 9, RPE: 9},
	})
	if got := dropOffReasons(a); got[1] != "" || got[2] != "" || !a.Comparable {
		t.Fatalf("reasons = %v", got)
	}
}

// The probable warm-up rule looks at the top load of the set's own equipment,
// so a light machine is not mistaken for a warm-up of a heavier one.
func TestProbableWarmupIsJudgedWithinTheSameEquipment(t *testing.T) {
	a := AnalyzeDropOff("aperturas", []Set{
		{ID: 1, Position: 1, WeightKG: 40, Reps: 10, RPE: 8, Equipment: "cable"},
		{ID: 2, Position: 2, WeightKG: 40, Reps: 10, RPE: 9, Equipment: "cable"},
		{ID: 3, Position: 3, WeightKG: 100, Reps: 6, RPE: 8, Equipment: "machine a"},
	})
	got := dropOffReasons(a)
	if got[1] != "" || got[2] != "" || got[3] != ExcludedDifferentEquipment || a.ReferenceSetID != 1 {
		t.Fatalf("reasons = %v, reference = %d", got, a.ReferenceSetID)
	}
}
