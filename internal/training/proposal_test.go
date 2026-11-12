package training_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
)

type proposalFixture struct {
	now      time.Time
	services *training.Services
	applied  []string
}

// newProposalFixture opens a store and provisions a user, then puts the account
// in review_all so every proposal waits for the person.
func newProposalFixture(t *testing.T) (*proposalFixture, *training.Service) {
	t.Helper()
	f, svc := newDefaultProposalFixture(t)
	if err := svc.SetApprovalMode(context.Background(), training.ApprovalReviewAll); err != nil {
		t.Fatal(err)
	}
	return f, svc
}

// newDefaultProposalFixture provisions a user created after migration 27, so it
// starts in the default mode, accept_except_delete.
func newDefaultProposalFixture(t *testing.T) (*proposalFixture, *training.Service) {
	t.Helper()
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
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

const (
	coachA = training.CoachSessionPrefix + "a"
	coachB = training.CoachSessionPrefix + "b"
)

func in(session string) context.Context {
	return training.WithAgentSession(context.Background(), session)
}

func onlyDecision(t *testing.T, svc *training.Service) training.DecisionRecord {
	t.Helper()
	records, err := svc.Decisions(context.Background(), 0)
	if err != nil || len(records) == 0 {
		t.Fatalf("decisions = %+v, %v", records, err)
	}
	return records[0]
}

func TestNewAccountProposalWaitsAndRejectChangesNothing(t *testing.T) {
	ctx := in(coachA)
	f, svc := newProposalFixture(t)
	p, err := svc.Propose(ctx, input("delete_set", "set.delete"), f.apply("delete_set"))
	if err != nil || p.Status != training.ProposalPending || p.Session != coachA || len(f.applied) != 0 {
		t.Fatalf("propose = %+v, %v, applied %v", p, err, f.applied)
	}
	if records, _ := svc.Decisions(ctx, 0); len(records) != 0 {
		t.Fatalf("a pending proposal is not a decision: %+v", records)
	}
	rejected, err := svc.RejectProposal(ctx, p.ID, training.AnswerRejectOnce)
	if err != nil || rejected.Status != training.ProposalRejected || rejected.DecidedBy != training.DecidedByPerson || len(f.applied) != 0 {
		t.Fatalf("reject = %+v, %v, applied %v", rejected, err, f.applied)
	}
	if _, err := svc.AcceptProposal(ctx, p.ID, training.AnswerAllowOnce, f.applier); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("accepting a rejected proposal = %v", err)
	}
}

// R005: a once answer decides its one proposal and is never stored.
func TestOnceAnswersAreNeverStored(t *testing.T) {
	ctx := in(coachA)
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(ctx, input("add_set", "set.log"), f.apply("add_set"))
	accepted, err := svc.AcceptProposal(ctx, p.ID, training.AnswerAllowOnce, f.applier)
	if err != nil || accepted.Status != training.ProposalAccepted || accepted.DecidedBy != training.DecidedByPerson || string(accepted.Result) != `{"done":"add_set"}` {
		t.Fatalf("accept = %+v, %v", accepted, err)
	}
	if d := onlyDecision(t, svc); d.Outcome != training.OutcomeAllow || d.Answer != training.AnswerAllowOnce || d.DeciderKind != training.DeciderSubject ||
		d.Channel != training.ChannelWeb || d.Session != coachA || d.Operation != "proposal:"+strconv.FormatInt(p.ID, 10) {
		t.Fatalf("allow_once record = %+v", d)
	}
	if _, err := svc.AcceptProposal(ctx, p.ID, training.AnswerAllowOnce, f.applier); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("second accept = %v", err)
	}
	next, _ := svc.Propose(ctx, input("add_set", "set.log"), f.apply("add_set"))
	if next.Status != training.ProposalPending || len(f.applied) != 1 {
		t.Fatalf("after allow_once: %+v, applied %v", next, f.applied)
	}
	if _, err := svc.RejectProposal(ctx, next.ID, training.AnswerRejectOnce); err != nil {
		t.Fatal(err)
	}
	if d := onlyDecision(t, svc); d.Outcome != training.OutcomeDeny || d.Answer != training.AnswerRejectOnce {
		t.Fatalf("reject_once record = %+v", d)
	}
	third, _ := svc.Propose(ctx, input("add_set", "set.log"), f.apply("add_set"))
	if third.Status != training.ProposalPending {
		t.Fatalf("after reject_once: %+v", third)
	}
	if perms, _ := svc.Permissions(ctx); len(perms.Rules) != 0 {
		t.Fatalf("once answers stored: %+v", perms.Rules)
	}
}

// R006: allow_always is a grant for the action type, limited to the session it
// was given in, whose origin is the record of the answer.
func TestAllowAlwaysGrantsTheActionTypeInThatSessionOnly(t *testing.T) {
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(in(coachA), input("log_sets", "set.log"), f.apply("log_sets"))
	if _, err := svc.AcceptProposal(context.Background(), p.ID, training.AnswerAllowAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	answer := onlyDecision(t, svc)
	perms, _ := svc.Permissions(context.Background())
	if len(perms.Rules) != 1 || perms.Rules[0].Effect != training.EffectAllow || perms.Rules[0].Session != coachA || perms.Rules[0].Origin != answer.ID ||
		answer.Answer != training.AnswerAllowAlways {
		t.Fatalf("grant = %+v, answer record %+v", perms.Rules, answer)
	}
	same, err := svc.Propose(in(coachA), input("add_set", "set.log"), f.apply("add_set"))
	if err != nil || same.Status != training.ProposalAccepted || same.DecidedBy != training.DecidedBySessionGrant {
		t.Fatalf("same session and action type = %+v, %v", same, err)
	}
	if d := onlyDecision(t, svc); d.DeciderKind != training.DeciderGrant || d.DeciderID != "rule:"+strconv.FormatInt(perms.Rules[0].ID, 10) || d.Channel != training.ChannelGrantStore {
		t.Fatalf("grant decision record = %+v", d)
	}
	other, _ := svc.Propose(in(coachA), input("set_exercise_note", "exercise.note.remove"), f.apply("set_exercise_note"))
	if other.Status != training.ProposalPending {
		t.Fatalf("other action type = %+v", other)
	}
}

// R008: a session-limited grant never matches another session or a check without one.
func TestGrantDoesNotMatchAnotherSessionOrNone(t *testing.T) {
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(in(coachA), input("log_sets", "set.log"), f.apply("log_sets"))
	if _, err := svc.AcceptProposal(context.Background(), p.ID, training.AnswerAllowAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{coachB, training.ExternalSession, ""} {
		got, err := svc.Propose(in(session), input("log_sets", "set.log"), f.apply("log_sets"))
		if err != nil || got.Status != training.ProposalPending {
			t.Fatalf("session %q = %+v, %v", session, got, err)
		}
	}
	if len(f.applied) != 1 {
		t.Fatalf("applied %v", f.applied)
	}
}

// R007: reject_always is a denial limited like a grant, and it wins over a grant.
func TestRejectAlwaysDeniesInThatSessionEvenWhenAGrantMatches(t *testing.T) {
	f, svc := newProposalFixture(t)
	first, _ := svc.Propose(in(coachA), input("delete_set", "set.delete"), f.apply("delete_set"))
	second, _ := svc.Propose(in(coachA), input("delete_set", "set.delete"), f.apply("delete_set"))
	if _, err := svc.AcceptProposal(context.Background(), first.ID, training.AnswerAllowAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	// second was asked before the grant existed, so it still waits.
	rejected, err := svc.RejectProposal(context.Background(), second.ID, training.AnswerRejectAlways)
	if err != nil || rejected.Status != training.ProposalRejected {
		t.Fatalf("reject_always = %+v, %v", rejected, err)
	}
	answer := onlyDecision(t, svc)
	f.applied = nil
	denied, err := svc.Propose(in(coachA), input("delete_set", "set.delete"), f.apply("delete_set"))
	if err != nil || denied.Status != training.ProposalRejected || denied.DecidedBy != training.DecidedByDenial || len(f.applied) != 0 {
		t.Fatalf("denied = %+v, %v, applied %v", denied, err, f.applied)
	}
	d := onlyDecision(t, svc)
	perms, _ := svc.Permissions(context.Background())
	var denial training.AnswerRule
	for _, r := range perms.Rules {
		if r.Effect == training.EffectDeny {
			denial = r
		}
	}
	if denial.Origin != answer.ID || denial.Session != coachA || d.Outcome != training.OutcomeDeny || d.DeciderKind != training.DeciderDenial ||
		d.DeciderID != "rule:"+strconv.FormatInt(denial.ID, 10) {
		t.Fatalf("denial %+v, answer %+v, decision %+v", denial, answer, d)
	}
	elsewhere, _ := svc.Propose(in(coachB), input("delete_set", "set.delete"), f.apply("delete_set"))
	if elsewhere.Status != training.ProposalPending {
		t.Fatalf("denial matched another session: %+v", elsewhere)
	}
}

// A denial also wins over the account's accept-all grant.
func TestDenialWinsOverAcceptAll(t *testing.T) {
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(in(coachA), input("delete_set", "set.delete"), f.apply("delete_set"))
	if _, err := svc.RejectProposal(context.Background(), p.ID, training.AnswerRejectAlways); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAcceptAll(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	denied, _ := svc.Propose(in(coachA), input("delete_set", "set.delete"), f.apply("delete_set"))
	if denied.Status != training.ProposalRejected || len(f.applied) != 0 {
		t.Fatalf("accept-all beat a denial: %+v, applied %v", denied, f.applied)
	}
	elsewhere, _ := svc.Propose(in(coachB), input("delete_set", "set.delete"), f.apply("delete_set"))
	if elsewhere.Status != training.ProposalAccepted || elsewhere.DecidedBy != training.DecidedByAcceptAll {
		t.Fatalf("accept-all in another session = %+v", elsewhere)
	}
}

func TestAlwaysAnswerNeedsASession(t *testing.T) {
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(context.Background(), input("add_set", "set.log"), f.apply("add_set"))
	if _, err := svc.AcceptProposal(context.Background(), p.ID, training.AnswerAllowAlways, f.applier); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("allow_always without a session = %v", err)
	}
	if _, err := svc.RejectProposal(context.Background(), p.ID, training.AnswerRejectAlways); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("reject_always without a session = %v", err)
	}
	got, _ := svc.GetProposal(context.Background(), p.ID)
	if got.Status != training.ProposalPending || len(f.applied) != 0 {
		t.Fatalf("proposal changed: %+v, applied %v", got, f.applied)
	}
	if perms, _ := svc.Permissions(context.Background()); len(perms.Rules) != 0 {
		t.Fatalf("stored %+v", perms.Rules)
	}
}

func TestSessionAnswerEndsAfterTheWindow(t *testing.T) {
	f, svc := newProposalFixture(t)
	ctx := in(training.ExternalSession)
	p, _ := svc.Propose(ctx, input("update_set", "set.update"), f.apply("update_set"))
	if _, err := svc.AcceptProposal(ctx, p.ID, training.AnswerAllowAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(training.SessionWindow - time.Minute)
	inside, _ := svc.Propose(ctx, input("update_set", "set.update"), f.apply("update_set"))
	if inside.Status != training.ProposalAccepted || inside.DecidedBy != training.DecidedBySessionGrant {
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

// Accept-all is the account's host grant: it covers every action type in every
// session and in none, and each proposal is still recorded with a decision.
func TestAcceptAllCoversEveryActionTypeAndStillRecords(t *testing.T) {
	f, svc := newProposalFixture(t)
	ctx := context.Background()
	if err := svc.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", coachA, training.ExternalSession} {
		p, err := svc.Propose(in(session), input("a_tool_added_later", "something.new"), f.apply("a_tool_added_later"))
		if err != nil || p.Status != training.ProposalAccepted || p.DecidedBy != training.DecidedByAcceptAll {
			t.Fatalf("accept-all in %q = %+v, %v", session, p, err)
		}
		if d := onlyDecision(t, svc); d.Outcome != training.OutcomeAllow || d.DeciderKind != training.DeciderGrant || d.DeciderID != training.AcceptAllGrant || d.Session != session {
			t.Fatalf("accept-all record = %+v", d)
		}
	}
	history, err := svc.ListProposals(ctx, training.ProposalFilter{})
	if err != nil || len(history) != 3 {
		t.Fatalf("not recorded: %+v, %v", history, err)
	}
	if err := svc.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	back, _ := svc.Propose(in(coachA), input("a_tool_added_later", "something.new"), f.apply("a_tool_added_later"))
	if back.Status != training.ProposalPending {
		t.Fatalf("after turning accept-all off = %+v", back)
	}
}

// R003 and R010: forgetting a grant revokes it for every later proposal, and the
// revocation is recorded.
func TestForgettingAGrantRevokesItAndIsRecorded(t *testing.T) {
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(in(coachA), input("log_sets", "set.log"), f.apply("log_sets"))
	if _, err := svc.AcceptProposal(context.Background(), p.ID, training.AnswerAllowAlways, f.applier); err != nil {
		t.Fatal(err)
	}
	_ = svc.SetAcceptAll(context.Background(), true)
	_ = svc.SetAcceptAll(context.Background(), false)
	perms, _ := svc.Permissions(context.Background())
	again, _ := svc.Propose(in(coachA), input("log_sets", "set.log"), f.apply("log_sets"))
	if again.DecidedBy != training.DecidedBySessionGrant || len(perms.Rules) != 1 {
		t.Fatalf("grant lost by toggling accept-all: %+v, %+v", again, perms.Rules)
	}
	if err := svc.ForgetRule(context.Background(), perms.Rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if d := onlyDecision(t, svc); d.Outcome != training.OutcomeRevoked || d.Permission != "set.log" || d.Session != coachA || d.DeciderKind != training.DeciderSubject {
		t.Fatalf("revocation record = %+v", d)
	}
	forgotten, _ := svc.Propose(in(coachA), input("log_sets", "set.log"), f.apply("log_sets"))
	if forgotten.Status != training.ProposalPending {
		t.Fatalf("after forgetting = %+v", forgotten)
	}
	if err := svc.ForgetRule(context.Background(), perms.Rules[0].ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("forgetting twice = %v", err)
	}
}

// R011: a decision record names the proposal, never what it carries.
func TestDecisionRecordsHoldNoPayloadOrResult(t *testing.T) {
	f, svc := newProposalFixture(t)
	_ = svc.SetAcceptAll(context.Background(), true)
	secret := training.ProposalInput{Tool: "create_plan", ActionType: "plan.create", Payload: json.RawMessage(`{"name":"secret-plan-name"}`)}
	if _, err := svc.Propose(in(coachA), secret, f.apply("create_plan")); err != nil {
		t.Fatal(err)
	}
	d := onlyDecision(t, svc)
	if text := fmt.Sprintf("%+v", d); strings.Contains(text, "secret-plan-name") || strings.Contains(text, "done") {
		t.Fatalf("record leaks content: %s", text)
	}
}

// failingRecords is a store whose decision records cannot be written.
type failingRecords struct{ training.UserStore }

func (failingRecords) RecordDecision(context.Context, training.DecisionRecord) (int64, error) {
	return 0, errors.New("disk full")
}

// R012: an operation whose decision cannot be recorded is not run.
func TestUnrecordableDecisionDeniesTheOperation(t *testing.T) {
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	id, _ := store.ProvisionUser(context.Background(), "user_new")
	if err := store.ForUser(id).SetAcceptAll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	f := &proposalFixture{now: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	svc := training.NewService(failingRecords{store.ForUser(id)}, func() time.Time { return f.now })
	p, err := svc.Propose(in(coachA), input("add_set", "set.log"), f.apply("add_set"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptProposal(context.Background(), p.ID, training.AnswerAllowAlways, f.applier); err == nil {
		t.Fatal("accepted without a record")
	}
	got, _ := svc.GetProposal(context.Background(), p.ID)
	if len(f.applied) != 0 || got.Status != training.ProposalFailed {
		t.Fatalf("applied %v, proposal %+v", f.applied, got)
	}
	if perms, _ := svc.Permissions(context.Background()); len(perms.Rules) != 0 {
		t.Fatalf("grant stored without its record: %+v", perms.Rules)
	}
	_ = svc.SetAcceptAll(context.Background(), true)
	if _, err := svc.Propose(in(coachA), input("add_set", "set.log"), f.apply("add_set")); err == nil || len(f.applied) != 0 {
		t.Fatalf("accept-all applied without a record: %v, %v", err, f.applied)
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
	for _, wrong := range []string{training.AnswerRejectOnce, training.AnswerRejectAlways, "always"} {
		if _, err := svc.AcceptProposal(ctx, p.ID, wrong, f.applier); !errors.Is(err, training.ErrValidation) {
			t.Fatalf("accept with %q = %v", wrong, err)
		}
	}
	for _, wrong := range []string{training.AnswerAllowOnce, "forever"} {
		if _, err := svc.RejectProposal(ctx, p.ID, wrong); !errors.Is(err, training.ErrValidation) {
			t.Fatalf("reject with %q = %v", wrong, err)
		}
	}
	if len(f.applied) != 0 {
		t.Fatalf("applied %v", f.applied)
	}
}

func TestOneUserCannotAnswerAnothersProposal(t *testing.T) {
	ctx := in(coachA)
	f, svc := newProposalFixture(t)
	p, _ := svc.Propose(ctx, input("delete_set", "set.delete"), f.apply("delete_set"))
	other := f.services.ForUser(training.OwnerUserID)
	if _, err := other.AcceptProposal(ctx, p.ID, training.AnswerAllowAlways, f.applier); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("accept other's = %v", err)
	}
	if _, err := other.RejectProposal(ctx, p.ID, training.AnswerRejectAlways); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("reject other's = %v", err)
	}
	if perms, _ := other.Permissions(ctx); len(perms.Rules) != 0 {
		t.Fatalf("answer on another's proposal remembered: %+v", perms)
	}
	if got, err := svc.GetProposal(ctx, p.ID); err != nil || got.Status != training.ProposalPending || len(f.applied) != 0 {
		t.Fatalf("proposal changed: %+v, %v, applied %v", got, err, f.applied)
	}
}
