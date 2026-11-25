package mcpsrv

import (
	"context"
	"errors"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// Deleting the only session of a microcycle reopens it, and undoing the
// delete brings back the session and the closed microcycle together.
func TestDeletingTheOnlySessionReopensTheMicrocycleAndUndoClosesItAgain(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if out := structured(t, call(t, w.owner, "advance_block", map[string]any{"block_id": w.ids.block, "action": "activate"})); out["status"] != "accepted" {
		t.Fatalf("activate = %v", out)
	}
	var started StartOut
	decode(t, call(t, w.owner, "start_session", map[string]any{"date": isolationDay, "plan_id": w.ids.plan}), &started)
	if got := w.scalar(t, `SELECT status FROM block_microcycles WHERE id=?`, w.ids.microcycle); got != training.MicrocycleDone {
		t.Fatalf("microcycle = %v, want done", got)
	}
	before := fullDump(t, w)

	deleted := structured(t, call(t, w.owner, "delete_session", map[string]any{"session_id": started.Session.ID}))
	if deleted["status"] != "accepted" {
		t.Fatalf("delete = %v", deleted)
	}
	if got := w.scalar(t, `SELECT status || '|' || COALESCE(closed_on,'') || '|' || COALESCE(closed_by,'') FROM block_microcycles WHERE id=?`, w.ids.microcycle); got != "pending||" {
		t.Fatalf("microcycle after the delete = %v", got)
	}
	if _, err := w.ownerService.UndoProposal(ctx, proposalOf(t, deleted)); err != nil {
		t.Fatal(err)
	}
	if got := fullDump(t, w); got != before {
		t.Fatalf("undo did not restore:\ngot\n%s\nwant\n%s", got, before)
	}
}

// Undoing a block's activation once a session was placed in it is refused and
// changes nothing, so a sketch or planned block never holds a closed
// microcycle or a placed session; once the session's start is undone, the
// activation can be undone too.
func TestUndoingAnActivationAfterASessionWasPlacedIsRefused(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	initial := fullDump(t, w)
	activated := structured(t, call(t, w.owner, "advance_block", map[string]any{"block_id": w.ids.block, "action": "activate"}))
	if activated["status"] != "accepted" {
		t.Fatalf("activate = %v", activated)
	}
	started := structured(t, call(t, w.owner, "start_session", map[string]any{"date": isolationDay, "plan_id": w.ids.plan}))
	if started["status"] != "accepted" {
		t.Fatalf("start = %v", started)
	}
	placed := fullDump(t, w)

	_, err := w.ownerService.UndoProposal(ctx, proposalOf(t, activated))
	var conflict *training.UndoConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("undo of the activation = %v, want a conflict", err)
	}
	if got := fullDump(t, w); got != placed {
		t.Fatalf("a refused undo changed rows:\ngot\n%s\nwant\n%s", got, placed)
	}

	if _, err = w.ownerService.UndoProposal(ctx, proposalOf(t, started)); err != nil {
		t.Fatal(err)
	}
	if _, err = w.ownerService.UndoProposal(ctx, proposalOf(t, activated)); err != nil {
		t.Fatalf("undo of the activation once the session is gone = %v", err)
	}
	if got := fullDump(t, w); got != initial {
		t.Fatalf("rows after both undos:\ngot\n%s\nwant\n%s", got, initial)
	}
}
