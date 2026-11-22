package mcpsrv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Every set-writing tool takes an optional equipment and returns it normalized;
// exercise_history carries it on each set.
func TestSetToolsCarryEquipment(t *testing.T) {
	session := logSetsSession(t)

	var logged LogOut
	decode(t, call(t, session, "log_set", map[string]any{"exercise": "row", "weight_kg": 60, "reps": 10, "rpe": 8, "count": 2, "equipment": " Hammer  Row "}), &logged)
	if len(logged.Sets) != 2 || logged.Sets[0].Equipment != "hammer row" || logged.Sets[1].Equipment != "hammer row" {
		t.Fatalf("log_set sets = %#v", logged.Sets)
	}

	var batch LogSetsOut
	decode(t, call(t, session, "log_sets", map[string]any{"idempotency_key": "eq-1", "exercise": "aperturas", "sets": []map[string]any{
		{"weight_kg": 100, "reps": 6, "rpe": 8, "equipment": "Machine A"},
		{"weight_kg": 86, "reps": 6, "rpe": 8},
	}}), &batch)
	if len(batch.Sets) != 2 || batch.Sets[0].Equipment != "machine a" || batch.Sets[1].Equipment != "" {
		t.Fatalf("log_sets sets = %#v", batch.Sets)
	}

	var added SetOut
	decode(t, call(t, session, "add_set", map[string]any{"session_id": logged.Session.ID, "exercise": "row", "weight_kg": 62, "reps": 8, "rpe": 9, "equipment": "Cable Row"}), &added)
	if added.Set.Equipment != "cable row" {
		t.Fatalf("add_set set = %#v", added.Set)
	}

	var updated SetOut
	decode(t, call(t, session, "update_set", map[string]any{"set_id": batch.Sets[1].ID, "equipment": "Machine B"}), &updated)
	if updated.Set.Equipment != "machine b" {
		t.Fatalf("update_set set = %#v", updated.Set)
	}
	var cleared SetOut
	decode(t, call(t, session, "update_set", map[string]any{"set_id": batch.Sets[1].ID, "equipment": ""}), &cleared)
	if cleared.Set.ID != batch.Sets[1].ID || cleared.Set.Equipment != "" {
		t.Fatalf("cleared set = %#v", cleared.Set)
	}

	var history HistoryOut
	decode(t, call(t, session, "exercise_history", map[string]any{"exercise": "aperturas"}), &history)
	found := false
	for _, s := range history.History.Sets {
		if s.SetID == batch.Sets[0].ID {
			found = s.Equipment == "machine a"
		}
	}
	if !found {
		t.Fatalf("exercise_history does not carry equipment: %#v", history.History.Sets)
	}
}

func TestEquipmentIsInTheSetToolSchemas(t *testing.T) {
	session := logSetsSession(t)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"log_set": true, "log_sets": true, "add_set": true, "update_set": true}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			continue
		}
		delete(want, tool.Name)
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(schema), `"equipment"`) {
			t.Fatalf("%s schema lacks equipment: %s", tool.Name, schema)
		}
	}
	if len(want) != 0 {
		t.Fatalf("tools not listed: %v", want)
	}
}

// Through the real tools and store: sets logged on two machines under one
// exercise name are not compared by performance_dropoff.
func TestPerformanceDropoffSeparatesEquipment(t *testing.T) {
	session := logSetsSession(t)
	var batch LogSetsOut
	decode(t, call(t, session, "log_sets", map[string]any{"idempotency_key": "eq-2", "exercise": "aperturas", "sets": []map[string]any{
		{"weight_kg": 100, "reps": 6, "rpe": 8, "equipment": "machine a"},
		{"weight_kg": 95, "reps": 6, "rpe": 8, "equipment": "machine a"},
		{"weight_kg": 86, "reps": 6, "rpe": 8, "equipment": "machine b"},
	}}), &batch)
	var out PerformanceOut
	decode(t, call(t, session, "performance_dropoff", map[string]any{"session_id": batch.Session.ID}), &out)
	if len(out.Analyses) != 1 || len(out.Analyses[0].Sets) != 3 {
		t.Fatalf("analyses = %#v", out.Analyses)
	}
	sets := out.Analyses[0].Sets
	if sets[0].ExcludedReason != "" || sets[1].ExcludedReason != "" || sets[2].ExcludedReason != "different_equipment" || sets[2].Equipment != "machine b" {
		t.Fatalf("sets = %#v", sets)
	}
}
