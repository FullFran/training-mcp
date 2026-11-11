package mcpsrv

import (
	"context"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every set-writing tool takes the category, every set it returns reports it,
// and a warm-up neither adds SI nor counts as a set.
func TestSetToolsAcceptAndReportTheCategory(t *testing.T) {
	session := logSetsSession(t)
	var batch LogSetsOut
	decode(t, call(t, session, "log_sets", map[string]any{"idempotency_key": "w1", "exercise": "squat", "sets": []map[string]any{
		{"weight_kg": 60, "reps": 5, "category": "warmup"},
		{"weight_kg": 100, "reps": 5, "rpe": 8},
	}}), &batch)
	if len(batch.Sets) != 2 || batch.Sets[0].Category != training.SetWarmup || batch.Sets[0].SI != 0 || batch.Sets[1].Category != training.SetWorking || batch.TotalSI != 1 {
		t.Fatalf("log_sets = %+v", batch)
	}
	var one LogOut
	decode(t, call(t, session, "log_set", map[string]any{"exercise": "squat", "weight_kg": 80, "reps": 3, "rpe": 6, "category": "warmup"}), &one)
	if len(one.Sets) != 1 || one.Sets[0].Category != training.SetWarmup || one.TotalSI != 1 {
		t.Fatalf("log_set = %+v", one)
	}
	var added SetOut
	decode(t, call(t, session, "add_set", map[string]any{"session_id": batch.Session.ID, "exercise": "squat", "weight_kg": 90, "reps": 2, "rpe": 6, "category": "warmup"}), &added)
	if added.Set.Category != training.SetWarmup || added.TotalSI != 1 {
		t.Fatalf("add_set = %+v", added)
	}
	if got := listedSets(t, session); got != 1 {
		t.Fatalf("listed set count = %d, want only the working set", got)
	}
	var updated SetOut
	decode(t, call(t, session, "update_set", map[string]any{"set_id": batch.Sets[0].ID, "category": "working"}), &updated)
	if updated.Set.Category != training.SetWorking || updated.Set.SI != 1 || updated.TotalSI != 2 {
		t.Fatalf("update_set = %+v", updated)
	}
	if got := listedSets(t, session); got != 2 {
		t.Fatalf("listed set count after toggle = %d", got)
	}
}

func TestSetToolsRefuseAnUnknownCategory(t *testing.T) {
	session := logSetsSession(t)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "log_set", Arguments: map[string]any{"exercise": "squat", "weight_kg": 80, "reps": 3, "rpe": 6, "category": "top set"}})
	if err == nil && !res.IsError {
		t.Fatalf("an unknown category was accepted: %+v", res)
	}
	if got := listedSets(t, session); got != 0 {
		t.Fatalf("sets recorded = %d", got)
	}
}
