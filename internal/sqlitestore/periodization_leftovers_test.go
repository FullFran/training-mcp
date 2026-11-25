package sqlitestore

import (
	"context"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// Deleting the session that closed a microcycle reopens it: the microcycle is
// pending again, with no close date, and is the current one.
func TestDeletingTheSessionThatClosedAMicrocycleReopensIt(t *testing.T) {
	ctx := context.Background()
	w := newPeriodWorld(t)
	b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso")})
	w.advance(b.ID, training.BlockActivate)
	only := w.start("", "torso")
	if m := w.get(b.ID).Microcycles[0]; m.Status != training.MicrocycleDone || m.ClosedBy != training.ClosedByPlacement {
		t.Fatalf("after the start = %+v", m)
	}
	if _, err := w.svc.DeleteSession(ctx, only.SessionID); err != nil {
		t.Fatal(err)
	}
	v := w.get(b.ID)
	if m := v.Microcycles[0]; m.Status != training.MicrocyclePending || m.ClosedOn != "" || m.ClosedBy != "" || m.State != training.StateCurrent || m.SessionsDone != 0 {
		t.Fatalf("after the delete = %+v", m)
	}
	if v.Microcycles[1].State != training.StateUpcoming {
		t.Fatalf("second = %+v", v.Microcycles[1])
	}
}

// A microcycle stays closed when deleting a session would not reopen it
// honestly: it was closed by hand, a later microcycle already moved on, or it
// still holds every routine of its rotation.
func TestDeletingASessionLeavesOtherwiseClosedMicrocyclesClosed(t *testing.T) {
	ctx := context.Background()
	del := func(w *periodWorld, id int64) {
		t.Helper()
		if _, err := w.svc.DeleteSession(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("closed by hand", func(t *testing.T) {
		w := newPeriodWorld(t)
		b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso", "pierna")})
		w.advance(b.ID, training.BlockActivate)
		s := w.start("", "torso")
		w.advance(b.ID, training.BlockCompleteCurrent)
		del(w, s.SessionID)
		if m := w.get(b.ID).Microcycles[0]; m.Status != training.MicrocycleDone || m.ClosedBy != training.ClosedByHand {
			t.Fatalf("microcycle = %+v", m)
		}
	})
	t.Run("a later microcycle moved on", func(t *testing.T) {
		w := newPeriodWorld(t)
		b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(3), Rotation: w.rotation("torso")})
		w.advance(b.ID, training.BlockActivate)
		first := w.start("", "torso")
		w.start("2026-11-17", "torso")
		del(w, first.SessionID)
		v := w.get(b.ID)
		if v.Microcycles[0].Status != training.MicrocycleDone || v.Microcycles[1].Status != training.MicrocycleDone {
			t.Fatalf("microcycles = %+v", v.Microcycles)
		}
	})
	t.Run("still full", func(t *testing.T) {
		w := newPeriodWorld(t)
		b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(1), Rotation: w.rotation("torso")})
		w.advance(b.ID, training.BlockActivate)
		w.start("", "torso")
		// A second session is off-plan and then assigned by hand to the
		// microcycle: deleting it leaves the microcycle full.
		extra := w.start("2026-11-17", "full")
		if _, err := w.svc.AssignSessionBlock(ctx, training.AssignInput{SessionID: extra.SessionID, MicrocycleID: b.Microcycles[0].ID}); err != nil {
			t.Fatal(err)
		}
		del(w, extra.SessionID)
		if m := w.get(b.ID).Microcycles[0]; m.Status != training.MicrocycleDone {
			t.Fatalf("microcycle = %+v", m)
		}
	})
	t.Run("calendar", func(t *testing.T) {
		w := newPeriodWorld(t)
		b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso"), Advance: strPtr(training.AdvanceCalendar)})
		w.advance(b.ID, training.BlockActivate)
		s := w.start("", "torso")
		before := w.get(b.ID).Microcycles
		del(w, s.SessionID)
		if after := w.get(b.ID).Microcycles; after[0].Status != before[0].Status || after[0].ClosedOn != before[0].ClosedOn {
			t.Fatalf("calendar microcycle changed: %+v", after[0])
		}
	})
}

// A failure to reopen never fails the delete: the session and its sets are
// gone and the microcycle is left as it was.
func TestAReopenFailureNeverFailsTheDelete(t *testing.T) {
	ctx := context.Background()
	w := newPeriodWorld(t)
	b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso")})
	w.advance(b.ID, training.BlockActivate)
	s := w.start("", "torso")
	if _, err := w.store.db.Exec(`CREATE TRIGGER fail_reopen BEFORE UPDATE OF status ON block_microcycles
		BEGIN SELECT RAISE(ABORT, 'reopen unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.DeleteSession(ctx, s.SessionID); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if _, err := w.svc.GetSession(ctx, s.SessionID); err == nil {
		t.Fatal("the session is still there")
	}
	if m := w.get(b.ID).Microcycles[0]; m.Status != training.MicrocycleDone {
		t.Fatalf("microcycle = %+v", m)
	}
}
