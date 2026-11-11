package training_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

type proposalFixture struct {
	now      time.Time
	services *training.Services
	applied  []string
}

// newProposalFixture opens a store and provisions a user created after migration
// 14, so it starts in ask-everything.
func newProposalFixture(t *testing.T) (*proposalFixture, *training.Service) {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := &proposalFixture{now: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	f.services = training.NewServices(store, func() time.Time { return f.now })
	id, err := store.ProvisionUser(context.Background(), "user_new")
	if err != nil {
		t.Fatal(err)
	}
	return f, f.services.ForUser(id)
}

func (f *proposalFixture) apply(tool string) training.ApplyFunc {
	return func(context.Context) (json.RawMessage, error) {
		f.applied = append(f.applied, tool)
		return json.RawMessage(`{"done":"` + tool + `"}`), nil
	}
}

func (f *proposalFixture) applier(_ context.Context, _ *training.Service, tool string, _ json.RawMessage) (json.RawMessage, error) {
	return f.apply(tool)(context.Background())
}

func input(tool, action string) training.ProposalInput {
	return training.ProposalInput{Tool: tool, ActionType: action, Payload: json.RawMessage(`{"x":1}`)}
}

func TestNewAccountProposalWaitsAndRejectChangesNothing(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	p, err := svc.Propose(ctx, input("delete_set", "set.delete"), f.apply("delete_set"))
	if err != nil || p.Status != training.ProposalPending || len(f.applied) != 0 {
		t.Fatalf("propose = %+v, %v, applied %v", p, err, f.applied)
	}
	rejected, err := svc.RejectProposal(ctx, p.ID)
	if err != nil || rejected.Status != training.ProposalRejected || len(f.applied) != 0 {
		t.Fatalf("reject = %+v, %v, applied %v", rejected, err, f.applied)
	}
	if _, err := svc.AcceptProposal(ctx, p.ID, training.RememberOnce, f.applier); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("accepting a rejected proposal = %v", err)
	}
}

func TestAcceptOnceAppliesOnlyThatProposal(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(ctx, input("add_set", "set.log"), f.apply("add_set"))
	accepted, err := svc.AcceptProposal(ctx, p.ID, training.RememberOnce, f.applier)
	if err != nil || accepted.Status != training.ProposalAccepted || accepted.DecidedBy != training.DecidedByPerson || string(accepted.Result) != `{"done":"add_set"}` {
		t.Fatalf("accept = %+v, %v", accepted, err)
	}
	if _, err := svc.AcceptProposal(ctx, p.ID, training.RememberOnce, f.applier); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("second accept = %v", err)
	}
	next, _ := svc.Propose(ctx, input("add_set", "set.log"), f.apply("add_set"))
	if next.Status != training.ProposalPending || len(f.applied) != 1 {
		t.Fatalf("after once: %+v, applied %v", next, f.applied)
	}
}

// Remembering keys on the action type: another tool with the same action type is
// covered, the same tool's other action types are not.
func TestAcceptAlwaysCoversTheActionTypeNotTheTool(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(ctx, input("log_sets", "set.log"), f.apply("log_sets"))
	if _, err := svc.AcceptProposal(ctx, p.ID, training.RememberAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	same, err := svc.Propose(ctx, input("add_set", "set.log"), f.apply("add_set"))
	if err != nil || same.Status != training.ProposalAccepted || same.DecidedBy != training.DecidedByAlways {
		t.Fatalf("same action type = %+v, %v", same, err)
	}
	f.now = f.now.Add(365 * 24 * time.Hour)
	later, _ := svc.Propose(ctx, input("log_sets", "set.log"), f.apply("log_sets"))
	if later.Status != training.ProposalAccepted {
		t.Fatalf("always expired: %+v", later)
	}
	other, _ := svc.Propose(ctx, input("set_exercise_note", "exercise.note.remove"), f.apply("set_exercise_note"))
	if other.Status != training.ProposalPending {
		t.Fatalf("other action type = %+v", other)
	}
}

func TestAcceptSessionLastsTwelveHours(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(ctx, input("update_set", "set.update"), f.apply("update_set"))
	if _, err := svc.AcceptProposal(ctx, p.ID, training.RememberSession, f.applier); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(training.SessionWindow - time.Minute)
	inside, _ := svc.Propose(ctx, input("update_set", "set.update"), f.apply("update_set"))
	if inside.Status != training.ProposalAccepted || inside.DecidedBy != training.DecidedBySession {
		t.Fatalf("inside the window = %+v", inside)
	}
	f.now = f.now.Add(2 * time.Minute)
	outside, _ := svc.Propose(ctx, input("update_set", "set.update"), f.apply("update_set"))
	if outside.Status != training.ProposalPending {
		t.Fatalf("after the window = %+v", outside)
	}
	perms, err := svc.Permissions(ctx)
	if err != nil || len(perms.Rules) != 0 {
		t.Fatalf("expired rule still listed: %+v, %v", perms, err)
	}
}

func TestAcceptAllCoversEveryActionTypeAndStillRecords(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	if err := svc.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Propose(ctx, input("a_tool_added_later", "something.new"), f.apply("a_tool_added_later"))
	if err != nil || p.Status != training.ProposalAccepted || p.DecidedBy != training.DecidedByAcceptAll {
		t.Fatalf("accept-all = %+v, %v", p, err)
	}
	history, err := svc.ListProposals(ctx, training.ProposalFilter{})
	if err != nil || len(history) != 1 || history[0].ID != p.ID {
		t.Fatalf("not recorded: %+v, %v", history, err)
	}
	if err := svc.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	back, _ := svc.Propose(ctx, input("a_tool_added_later", "something.new"), f.apply("a_tool_added_later"))
	if back.Status != training.ProposalPending {
		t.Fatalf("after turning accept-all off = %+v", back)
	}
}

func TestTurningAcceptAllOffRestoresRememberedAnswers(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(ctx, input("log_sets", "set.log"), f.apply("log_sets"))
	if _, err := svc.AcceptProposal(ctx, p.ID, training.RememberAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	_ = svc.SetAcceptAll(ctx, true)
	_ = svc.SetAcceptAll(ctx, false)
	again, _ := svc.Propose(ctx, input("log_sets", "set.log"), f.apply("log_sets"))
	if again.DecidedBy != training.DecidedByAlways {
		t.Fatalf("remembered answer lost: %+v", again)
	}
	if err := svc.ForgetRule(ctx, "set.log"); err != nil {
		t.Fatal(err)
	}
	forgotten, _ := svc.Propose(ctx, input("log_sets", "set.log"), f.apply("log_sets"))
	if forgotten.Status != training.ProposalPending {
		t.Fatalf("after forgetting = %+v", forgotten)
	}
}

func TestFailedApplyIsRecordedWithThePublicError(t *testing.T) {
	ctx := context.Background()
	_, svc := newProposalFixture(t)
	_ = svc.SetAcceptAll(ctx, true)
	failing := func(context.Context) (json.RawMessage, error) {
		return nil, errors.Join(training.ErrNotFound, errors.New("secret detail"))
	}
	p, err := svc.Propose(ctx, input("delete_set", "set.delete"), failing)
	if !errors.Is(err, training.ErrNotFound) || p.Status != training.ProposalFailed || p.Error != training.ErrNotFound.Error() {
		t.Fatalf("failed apply = %+v, %v", p, err)
	}
}

func TestProposeAndAcceptRejectBadInput(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	for _, in := range []training.ProposalInput{
		{Tool: "", ActionType: "a", Payload: json.RawMessage(`{}`)},
		{Tool: "t", ActionType: " ", Payload: json.RawMessage(`{}`)},
		{Tool: "t", ActionType: "a", Payload: json.RawMessage(`{`)},
	} {
		if _, err := svc.Propose(ctx, in, f.apply("t")); !errors.Is(err, training.ErrValidation) {
			t.Fatalf("propose %+v = %v", in, err)
		}
	}
	p, _ := svc.Propose(ctx, input("add_set", "set.log"), f.apply("add_set"))
	if _, err := svc.AcceptProposal(ctx, p.ID, "forever", f.applier); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("unknown remember = %v", err)
	}
	if len(f.applied) != 0 {
		t.Fatalf("applied %v", f.applied)
	}
}

func TestOneUserCannotAnswerAnothersProposal(t *testing.T) {
	ctx := context.Background()
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(ctx, input("delete_set", "set.delete"), f.apply("delete_set"))
	other := f.services.ForUser(training.OwnerUserID)
	if _, err := other.AcceptProposal(ctx, p.ID, training.RememberAlways, f.applier); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("accept other's = %v", err)
	}
	if _, err := other.RejectProposal(ctx, p.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("reject other's = %v", err)
	}
	if perms, _ := other.Permissions(ctx); len(perms.Rules) != 0 {
		t.Fatalf("answer on another's proposal remembered: %+v", perms)
	}
	if got, err := svc.GetProposal(ctx, p.ID); err != nil || got.Status != training.ProposalPending || len(f.applied) != 0 {
		t.Fatalf("proposal changed: %+v, %v, applied %v", got, err, f.applied)
	}
}
