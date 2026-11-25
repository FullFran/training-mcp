package mcpsrv

import (
	"context"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// activateWithModifiers gives the seeded block's first microcycle modifiers
// and activates it.
func activateWithModifiers(t *testing.T, w isolationWorld) {
	t.Helper()
	saved := structured(t, call(t, w.owner, "save_block", map[string]any{"block_id": w.ids.block,
		"microcycles": []map[string]any{{"position": 1, "label": "carga", "sets_delta": 1, "effort_delta": -1}}}))
	if saved["status"] != "accepted" {
		t.Fatalf("save = %v", saved)
	}
	if out := structured(t, call(t, w.owner, "advance_block", map[string]any{"block_id": w.ids.block, "action": "activate"})); out["status"] != "accepted" {
		t.Fatalf("activate = %v", out)
	}
}

// start_session in a microcycle with modifiers says what it applied, base and
// result, the session's copy holds the result, session_progress shows the
// same, and undoing the start leaves every row as it was.
func TestStartSessionShowsAndAppliesTheMicrocycleModifiers(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	activateWithModifiers(t, w)
	before := fullDump(t, w)

	var out StartOut
	result := call(t, w.owner, "start_session", map[string]any{"date": isolationDay, "plan_id": w.ids.plan})
	decode(t, result, &out)
	p := out.Prescription
	if p == nil || p.BlockID != w.ids.block || p.MicrocycleID != w.ids.microcycle || p.MicrocycleLabel != "carga" || p.SetsDelta != 1 || p.EffortDelta != -1 || p.Rule == "" {
		t.Fatalf("block_modifiers = %+v", p)
	}
	want := []training.PrescribedItem{
		{Exercise: isolationExercise, BaseSets: 3, Sets: 4, BaseTargetRPE: 8, TargetRPE: 7},
		{Exercise: "remo", BaseSets: 2, Sets: 3},
	}
	if len(p.Items) != len(want) || p.Items[0] != want[0] || p.Items[1] != want[1] {
		t.Fatalf("items = %+v", p.Items)
	}
	if got := w.scalar(t, `SELECT target_sets FROM session_items WHERE session_id=? AND exercise=?`, out.Session.ID, isolationExercise); got != int64(4) {
		t.Fatalf("copy target_sets = %v", got)
	}
	if got := w.scalar(t, `SELECT target_sets FROM plan_items WHERE plan_id=? AND exercise=?`, w.ids.plan, isolationExercise); got != int64(3) {
		t.Fatalf("routine target_sets = %v", got)
	}
	var progress ProgressOut
	decode(t, call(t, w.owner, "session_progress", map[string]any{"session_id": out.Session.ID}), &progress)
	if progress.BlockModifiers == nil || progress.BlockModifiers.Items[0] != want[0] || progress.Progress[0].TargetSets != 4 {
		t.Fatalf("session_progress = %+v", progress)
	}

	if _, err := w.ownerService.UndoProposal(ctx, out.ProposalID); err != nil {
		t.Fatal(err)
	}
	if got := fullDump(t, w); got != before {
		t.Fatalf("undo did not restore:\ngot\n%s\nwant\n%s", got, before)
	}
}

// When applying modifiers fails, start_session is still accepted and
// undoable, the copy keeps the routine's values and nothing claims otherwise.
func TestAModifierFailureLeavesStartSessionAcceptedAndUnmodified(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	activateWithModifiers(t, w)
	if _, err := w.db.Exec(`CREATE TRIGGER fail_modifiers BEFORE UPDATE OF target_sets, target_rpe ON session_items
		BEGIN SELECT RAISE(ABORT, 'modifiers unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	result := call(t, w.owner, "start_session", map[string]any{"date": isolationDay, "plan_id": w.ids.plan})
	if result.IsError {
		t.Fatalf("start_session failed: %s", stringContent(result))
	}
	var out StartOut
	decode(t, result, &out)
	if out.Status != "accepted" || out.Prescription != nil {
		t.Fatalf("start = %+v", out)
	}
	if p, err := w.ownerService.GetProposal(ctx, out.ProposalID); err != nil || p.Status != training.ProposalAccepted || !p.Undoable {
		t.Fatalf("proposal = %+v, %v", p, err)
	}
	if got := w.scalar(t, `SELECT target_sets FROM session_items WHERE session_id=? AND exercise=?`, out.Session.ID, isolationExercise); got != int64(3) {
		t.Fatalf("copy target_sets = %v", got)
	}
	if got := w.scalar(t, `SELECT block_microcycle_id FROM sessions WHERE id=?`, out.Session.ID); got != w.ids.microcycle {
		t.Fatalf("session microcycle = %v", got)
	}
}
