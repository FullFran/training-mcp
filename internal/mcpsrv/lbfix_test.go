package mcpsrv

import (
	"context"
	"database/sql"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

// An agent corrects sets from lb through a proposal: nothing changes while it
// waits, accepting it changes only those weights, the same sets are never
// corrected twice, and undoing it restores the exact rows.
func TestAgentCorrectsSetsFromLbThroughAProposal(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/training.db"
	store, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := training.NewService(store.ForUser(1), liveClock)
	if err := service.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	server := New(only(service))
	session := mustConnect(t, server.Handler())
	s, err := service.StartSession(ctx, "2026-08-01")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, w := range []float64{135, 225, 60} {
		set, _, err := service.AddSet(ctx, training.AddSetInput{SessionID: s.ID, Exercise: "sentadilla", WeightKG: w, Reps: 5, RPE: 8})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, set.ID)
	}

	var preview LbPreviewOut
	decode(t, call(t, session, "preview_lb_correction", map[string]any{"exercise": "sentadilla", "from": "2026-08-01", "to": "2026-08-01"}), &preview)
	if preview.Preview.Count != 3 || preview.Preview.Sets[0].OldKG != 135 || preview.Preview.Sets[0].NewKG != 61.25 {
		t.Fatalf("preview = %+v", preview.Preview)
	}
	if bad := call(t, session, "preview_lb_correction", map[string]any{"exercise": "sentadilla"}); !bad.IsError {
		t.Fatal("an exercise without dates was previewed")
	}

	before := dumpTables(t, db, nil)
	var out LbCorrectOut
	decode(t, call(t, session, "correct_lb_sets", map[string]any{"set_ids": ids[:2]}), &out)
	if out.Status != training.ProposalPending || dumpTables(t, db, nil) != before {
		t.Fatalf("a pending correction changed rows: %+v", out)
	}
	accepted, err := service.AcceptProposal(ctx, out.ProposalID, training.AnswerAllowOnce, server.Apply)
	if err != nil {
		t.Fatal(err)
	}
	var weights []float64
	rows, err := db.Query(`SELECT weight_kg FROM sets ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var w float64
		rows.Scan(&w)
		weights = append(weights, w)
	}
	rows.Close()
	if len(weights) != 3 || weights[0] != 61.25 || weights[1] != 102.05 || weights[2] != 60 {
		t.Fatalf("weights after the correction = %v", weights)
	}

	// The same sets again: refused, nothing changes.
	corrected := dumpTables(t, db, nil)
	var again LbCorrectOut
	decode(t, call(t, session, "correct_lb_sets", map[string]any{"set_ids": ids[:1]}), &again)
	if _, err := service.AcceptProposal(ctx, again.ProposalID, training.AnswerAllowOnce, server.Apply); err == nil {
		t.Fatal("a set was corrected twice")
	}
	if dumpTables(t, db, nil) != corrected {
		t.Fatal("a refused correction changed rows")
	}

	if _, err := service.UndoProposal(ctx, accepted.ID); err != nil {
		t.Fatal(err)
	}
	if after := dumpTables(t, db, nil); after != before {
		t.Fatalf("undo did not restore the rows:\nbefore\n%s\nafter\n%s", before, after)
	}
}
