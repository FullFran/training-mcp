package mcpsrv

import (
	"context"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
)

// While a log_sets waits for approval, the same call again (a second tap, or a
// retry after a lost answer) points at the proposal already waiting: it never
// queues a second proposal for the same set. A different key is a different set
// and gets its own proposal.
func TestLogSetsRepeatedWhilePendingKeepsOneProposal(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := training.NewService(store.ForUser(1), time.Now)
	if err := service.SetApprovalMode(ctx, training.ApprovalReviewAll); err != nil {
		t.Fatal(err)
	}
	server := New(only(service))
	session := mustConnect(t, server.Handler())

	first := structured(t, call(t, session, "log_sets", repsArgs("key-1", 10)))
	again := structured(t, call(t, session, "log_sets", repsArgs("key-1", 10)))
	if first["status"] != "pending" || again["status"] != "pending" || first["proposal_id"] != again["proposal_id"] {
		t.Fatalf("first = %v, again = %v; want the same pending proposal", first, again)
	}
	pending, err := service.ListProposals(ctx, training.ProposalFilter{Pending: true})
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending proposals = %d, %v; want 1", len(pending), err)
	}
	if n := listedSets(t, session); n != 0 {
		t.Fatalf("a pending proposal stored %d sets", n)
	}

	other := structured(t, call(t, session, "log_sets", repsArgs("key-2", 10)))
	if other["proposal_id"] == first["proposal_id"] {
		t.Fatalf("another key reused proposal %v", other["proposal_id"])
	}

	// Accepted, the same key replays the stored sets and stores nothing new.
	accepted, err := service.AcceptProposal(ctx, int64(first["proposal_id"].(float64)), training.AnswerAllowOnce, server.Apply)
	if err != nil || accepted.Status != training.ProposalAccepted {
		t.Fatalf("accept = %+v, %v", accepted, err)
	}
	if n := listedSets(t, session); n != 1 {
		t.Fatalf("sets after accepting = %d, want 1", n)
	}
}
