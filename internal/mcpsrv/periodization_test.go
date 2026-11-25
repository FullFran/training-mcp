package mcpsrv

import (
	"context"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

type periodIDs struct{ goal, block, microcycle int64 }

// seedPeriodization gives the owner an active goal and a planned sequence
// block of two microcycles that rotate one routine, not yet active, so the
// owner's other sessions stay unplaced.
func seedPeriodization(t *testing.T, owner *training.Service, planID int64) periodIDs {
	t.Helper()
	ctx := context.Background()
	text := "press banca 100 kg"
	goal, err := owner.SetGoal(ctx, training.GoalInput{Text: &text})
	if err != nil {
		t.Fatal(err)
	}
	intent, count, status, rotation := "acumular volumen", 2, training.BlockPlanned, []int64{planID}
	block, err := owner.SaveBlock(ctx, training.BlockInput{GoalID: &goal.ID, Intent: &intent, MicrocycleCount: &count, Status: &status, Rotation: &rotation})
	if err != nil {
		t.Fatal(err)
	}
	return periodIDs{goal: goal.ID, block: block.ID, microcycle: block.Microcycles[0].ID}
}

func (w isolationWorld) scalar(t *testing.T, query string, args ...any) any {
	t.Helper()
	var v any
	if err := w.db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A session started while a block is active lands in its current microcycle
// on its own, which closes once its routines are done; undoing the start
// takes the session out and reopens the microcycle.
func TestStartingASessionInAnActiveBlockPlacesItAndUndoTakesItOut(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	activated := structured(t, call(t, w.owner, "advance_block", map[string]any{"block_id": w.ids.block, "action": "activate"}))
	if activated["status"] != "accepted" || activated["block"].(map[string]any)["status"] != "active" {
		t.Fatalf("activate = %v", activated)
	}
	before := fullDump(t, w)

	started := structured(t, call(t, w.owner, "start_session", map[string]any{"date": isolationDay, "plan_id": w.ids.plan}))
	if started["status"] != "accepted" {
		t.Fatalf("start = %v", started)
	}
	if got := w.scalar(t, `SELECT block_microcycle_id FROM sessions WHERE user_id=1 ORDER BY id DESC LIMIT 1`); got != w.ids.microcycle {
		t.Fatalf("new session microcycle = %v, want %d", got, w.ids.microcycle)
	}
	if got := w.scalar(t, `SELECT block_off_plan FROM sessions WHERE user_id=1 ORDER BY id DESC LIMIT 1`); got != int64(0) {
		t.Fatalf("new session off_plan = %v", got)
	}
	if got := w.scalar(t, `SELECT status FROM block_microcycles WHERE id=?`, w.ids.microcycle); got != training.MicrocycleDone {
		t.Fatalf("microcycle status = %v, want done", got)
	}
	var view GetBlockOut
	decode(t, call(t, w.owner, "get_block", map[string]any{"block_id": w.ids.block}), &view)
	if m := view.Block.Microcycles; m[0].SessionsDone != 1 || m[0].State != training.StateDone || m[1].State != training.StateCurrent {
		t.Fatalf("microcycles = %+v", m)
	}
	var status training.TrainingStatus
	decode(t, call(t, w.owner, "training_status", map[string]any{}), &status)
	if status.Block == nil || status.Current == nil || status.Current.Position != 2 || len(status.Goals) != 1 {
		t.Fatalf("status = %+v", status)
	}

	if _, err := w.ownerService.UndoProposal(ctx, proposalOf(t, started)); err != nil {
		t.Fatal(err)
	}
	if got := fullDump(t, w); got != before {
		t.Fatalf("undo did not restore the block:\ngot\n%s\nwant\n%s", got, before)
	}
}

// Shrinking a block drops microcycles, so under the default approval mode it
// waits for the person; growing it does not.
func TestShrinkingABlockWaitsForThePerson(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetApprovalMode(ctx, training.DefaultApprovalMode); err != nil {
		t.Fatal(err)
	}
	grown := structured(t, call(t, w.owner, "save_block", map[string]any{"block_id": w.ids.block, "microcycle_count": 3}))
	if grown["status"] != "accepted" {
		t.Fatalf("grow = %v", grown)
	}
	if p, err := w.ownerService.GetProposal(ctx, proposalOf(t, grown)); err != nil || p.ActionType != "block.update" {
		t.Fatalf("grow proposal = %+v, %v", p, err)
	}
	before := fullDump(t, w)

	shrunk := structured(t, call(t, w.owner, "save_block", map[string]any{"block_id": w.ids.block, "microcycle_count": 1}))
	if shrunk["status"] != "pending" {
		t.Fatalf("shrink = %v", shrunk)
	}
	if got := fullDump(t, w); got != before {
		t.Fatal("a pending shrink changed rows")
	}
	id := proposalOf(t, shrunk)
	p, err := w.ownerService.GetProposal(ctx, id)
	if err != nil || p.ActionType != "block.shorten" || p.Tool != "save_block" {
		t.Fatalf("shrink proposal = %+v, %v", p, err)
	}
	if _, err = w.ownerService.AcceptProposal(ctx, id, training.AnswerAllowOnce, w.ownerServer.Apply); err != nil {
		t.Fatal(err)
	}
	if got := w.scalar(t, `SELECT COUNT(*) FROM block_microcycles WHERE block_id=?`, w.ids.block); got != int64(1) {
		t.Fatalf("microcycles after accepting = %v, want 1", got)
	}
	if _, err = w.ownerService.UndoProposal(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := fullDump(t, w); got != before {
		t.Fatalf("undo did not restore the dropped microcycles:\ngot\n%s\nwant\n%s", got, before)
	}
}

// Calls that break a rule come back as tool errors that say why and change
// nothing.
func TestPeriodizationToolsRejectInvalidCallsWithoutWriting(t *testing.T) {
	w := newIsolationWorld(t)
	before := fullDump(t, w)
	for name, args := range map[string]map[string]any{
		"complete on a planned block":    {"block_id": w.ids.block, "action": "complete_microcycle"},
		"assign with no target":          {"session_id": w.ids.session},
		"assign to a missing microcycle": {"session_id": w.ids.session, "microcycle_id": missingToolID},
	} {
		tool := "advance_block"
		if _, ok := args["session_id"]; ok {
			tool = "assign_session_block"
		}
		if result := call(t, w.owner, tool, args); !result.IsError {
			t.Errorf("%s: %s accepted %v", name, tool, structured(t, result))
		}
	}
	if result := call(t, w.owner, "save_block", map[string]any{"intent": "sin longitud"}); !result.IsError {
		t.Errorf("a block without microcycle_count was created: %v", structured(t, result))
	}
	if got := fullDump(t, w); got != before {
		t.Fatal("a rejected call changed rows")
	}
}
