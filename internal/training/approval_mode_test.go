package training_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

var deletingActions = []string{"set.delete", "session.delete", "plan.delete", "memory.forget", "plan.exercise.remove", "session.exercise.remove", "exercise.note.remove"}

func TestDeletingActionsAreExactlyTheDeletes(t *testing.T) {
	for _, a := range deletingActions {
		if !training.IsDeletingAction(a) {
			t.Errorf("%s is not deleting", a)
		}
	}
	for _, a := range []string{"set.log", "set.update", "plan.update", "memory.save", "exercise.note.set", "something.new", ""} {
		if training.IsDeletingAction(a) {
			t.Errorf("%s is deleting", a)
		}
	}
}

// A new account starts in accept_except_delete, and the three modes decide a
// non-deleting and a deleting action type as documented.
func TestApprovalModesDecideDeletingAndOtherActions(t *testing.T) {
	cases := []struct {
		mode       string // "" is the default of a new account
		deleting   bool
		wantStatus string
		decidedBy  string
		deciderID  string
	}{
		{"", false, training.ProposalAccepted, training.DecidedByAcceptExceptDelete, training.AcceptExceptDeleteGrant},
		{"", true, training.ProposalPending, "", ""},
		{training.ApprovalAcceptExceptDelete, false, training.ProposalAccepted, training.DecidedByAcceptExceptDelete, training.AcceptExceptDeleteGrant},
		{training.ApprovalAcceptExceptDelete, true, training.ProposalPending, "", ""},
		{training.ApprovalReviewAll, false, training.ProposalPending, "", ""},
		{training.ApprovalReviewAll, true, training.ProposalPending, "", ""},
		{training.ApprovalAcceptAll, false, training.ProposalAccepted, training.DecidedByAcceptAll, training.AcceptAllGrant},
		{training.ApprovalAcceptAll, true, training.ProposalAccepted, training.DecidedByAcceptAll, training.AcceptAllGrant},
	}
	for _, c := range cases {
		t.Run(c.mode+"/"+strconv.FormatBool(c.deleting), func(t *testing.T) {
			f, svc := newDefaultProposalFixture(t)
			ctx := context.Background()
			if c.mode != "" {
				if err := svc.SetApprovalMode(ctx, c.mode); err != nil {
					t.Fatal(err)
				}
			}
			action := "set.log"
			if c.deleting {
				action = "set.delete"
			}
			p, err := svc.Propose(in(coachA), input("a_tool", action), f.apply("a_tool"))
			if err != nil || p.Status != c.wantStatus || p.DecidedBy != c.decidedBy {
				t.Fatalf("propose = %+v, %v", p, err)
			}
			records, _ := svc.Decisions(ctx, 0)
			if c.wantStatus == training.ProposalPending {
				if len(f.applied) != 0 || len(records) != 0 {
					t.Fatalf("a pending proposal applied %v or recorded %+v", f.applied, records)
				}
				return
			}
			if len(f.applied) != 1 || len(records) != 1 {
				t.Fatalf("applied %v, records %+v", f.applied, records)
			}
			d := records[0]
			if d.Outcome != training.OutcomeAllow || d.DeciderKind != training.DeciderGrant || d.DeciderID != c.deciderID || d.Channel != training.ChannelGrantStore || d.Permission != action || d.Session != coachA {
				t.Fatalf("record = %+v", d)
			}
			if string(p.Result) != `{"done":"a_tool"}` {
				t.Fatalf("result = %s", p.Result)
			}
		})
	}
}

func TestEveryDeletingActionWaitsInAcceptExceptDelete(t *testing.T) {
	f, svc := newDefaultProposalFixture(t)
	for _, a := range deletingActions {
		p, err := svc.Propose(in(coachA), input("t", a), f.apply("t"))
		if err != nil || p.Status != training.ProposalPending {
			t.Errorf("%s = %+v, %v", a, p, err)
		}
	}
	if len(f.applied) != 0 {
		t.Fatalf("applied %v", f.applied)
	}
}

// A session grant still lets a deleting action through in accept_except_delete.
func TestAcceptExceptDeleteLeavesDeletesToSessionGrants(t *testing.T) {
	f, svc := newDefaultProposalFixture(t)
	first, _ := svc.Propose(in(coachA), input("delete_set", "set.delete"), f.apply("delete_set"))
	if _, err := svc.AcceptProposal(context.Background(), first.ID, training.AnswerAllowAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	second, _ := svc.Propose(in(coachA), input("delete_set", "set.delete"), f.apply("delete_set"))
	if second.Status != training.ProposalAccepted || second.DecidedBy != training.DecidedBySessionGrant {
		t.Fatalf("grant = %+v", second)
	}
}

// R007: a session denial wins over every mode.
func TestDenialWinsOverEveryMode(t *testing.T) {
	for _, mode := range []string{training.ApprovalAcceptExceptDelete, training.ApprovalAcceptAll, training.ApprovalReviewAll} {
		t.Run(mode, func(t *testing.T) {
			f, svc := newProposalFixture(t) // review_all, so both proposals wait to be denied
			ctx := context.Background()
			for _, action := range []string{"set.delete", "set.log"} {
				first, _ := svc.Propose(in(coachA), input("t", action), f.apply("x"))
				if _, err := svc.RejectProposal(ctx, first.ID, training.AnswerRejectAlways); err != nil {
					t.Fatal(err)
				}
			}
			if err := svc.SetApprovalMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"set.delete", "set.log"} {
				p, _ := svc.Propose(in(coachA), input("t", action), f.apply("x"))
				if p.Status != training.ProposalRejected || p.DecidedBy != training.DecidedByDenial || len(f.applied) != 0 {
					t.Fatalf("%s in %s = %+v, applied %v", action, mode, p, f.applied)
				}
			}
		})
	}
}

func TestSetApprovalModeRejectsAnUnknownMode(t *testing.T) {
	_, svc := newDefaultProposalFixture(t)
	for _, mode := range []string{"", "accept-all", "ACCEPT_ALL", "nope"} {
		if err := svc.SetApprovalMode(context.Background(), mode); !errors.Is(err, training.ErrValidation) {
			t.Errorf("mode %q = %v", mode, err)
		}
	}
	p, _ := svc.Permissions(context.Background())
	if p.Mode != training.ApprovalAcceptExceptDelete {
		t.Fatalf("mode changed to %q", p.Mode)
	}
}

// An auto-accepted proposal stays in history like any other.
func TestAutoAcceptedProposalIsInHistory(t *testing.T) {
	f, svc := newDefaultProposalFixture(t)
	p, _ := svc.Propose(in(coachA), input("log_sets", "set.log"), f.apply("log_sets"))
	history, err := svc.ListProposals(context.Background(), training.ProposalFilter{})
	if err != nil || len(history) != 1 || history[0].ID != p.ID || history[0].DecidedBy != training.DecidedByAcceptExceptDelete {
		t.Fatalf("history = %+v, %v", history, err)
	}
}
