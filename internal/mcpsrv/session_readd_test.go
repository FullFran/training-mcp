package mcpsrv

import (
	"context"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// add_session_exercise on an exercise already in today's session keeps the
// superset and notes the call does not send. Issue #54.
func TestAddSessionExerciseReAddKeepsSupersetAndNotes(t *testing.T) {
	w := newIsolationWorld(t)
	ctx := context.Background()
	mine := w.services.ForUser(2)
	session, err := mine.StartSession(ctx, isolationDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := mine.SetSessionItem(ctx, session.ID, training.PlanItem{
		Exercise: "curl", TargetSets: 3, RepMin: 10, RepMax: 12, Superset: "A", Notes: "control",
	}); err != nil {
		t.Fatal(err)
	}
	res := call(t, w.second, "add_session_exercise", map[string]any{"session_id": session.ID, "exercise": "curl", "target_sets": 4})
	if res.IsError {
		t.Fatalf("add_session_exercise failed: %#v", res.Content)
	}
	progress, err := mine.SessionProgress(ctx, session.ID)
	if err != nil || len(progress) != 1 {
		t.Fatalf("progress = %#v, %v", progress, err)
	}
	if got := progress[0]; got.TargetSets != 4 || got.Superset != "A" || got.Notes != "control" || got.RepMin != 10 || got.RepMax != 12 {
		t.Fatalf("re-add over MCP clobbered unsent fields: %#v", got)
	}
}
