package mcpsrv

import (
	"context"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// A memory the host proposes (a workout summary, a reply's learnings) is a
// memory_save proposal: accepting it runs the tool's own code, for the person
// who accepts it and nobody else.
func TestHostMemoryProposalIsAppliedByTheMemorySaveTool(t *testing.T) {
	ctx := context.Background()
	w := newMemoryWorld(t)
	hooks := training.HostMemory{ForUser: w.services.ForUser, Apply: w.server.Apply}
	session := training.CoachSessionPrefix + "conversation-3"
	if err := hooks.ProposeMemory(ctx, 1, session, training.MemoryInput{Type: "preference", Title: "Mancuernas", Body: "Prefiere mancuernas a barra"}); err != nil {
		t.Fatal(err)
	}
	if n := w.count(t, "1=1"); n != 0 {
		t.Fatalf("memory written before acceptance: %d", n)
	}
	owner := w.services.ForUser(1)
	pending, err := owner.ListProposals(ctx, training.ProposalFilter{Pending: true})
	if err != nil || len(pending) != 1 || pending[0].ActionType != memorySaveAction || pending[0].Session != session {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if _, err := owner.AcceptProposal(ctx, pending[0].ID, training.AnswerAllowOnce, w.server.Apply); err != nil {
		t.Fatal(err)
	}
	if n := w.count(t, "user_id=1 AND type='preference' AND title='Mancuernas' AND topic_key='preference/mancuernas'"); n != 1 {
		t.Fatalf("owner memory rows = %d", n)
	}
	if n := w.count(t, "user_id=2"); n != 0 {
		t.Fatalf("second user's memory rows = %d", n)
	}
}
