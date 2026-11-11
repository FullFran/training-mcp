package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const proposalsMigration = 14

const sessionAnswersMigration = 18

// databaseBeforeProposals is a database with two accounts, stopped before migration 14.
func databaseBeforeProposals(t *testing.T) string {
	t.Helper()
	return databaseBefore(t, proposalsMigration)
}

// databaseBefore is a database with two accounts, stopped before a migration.
func databaseBefore(t *testing.T, stop int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "training.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	mustExec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`)
	for _, name := range mustMigrationNames() {
		version, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		if version >= stop {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(string(body))
		mustExec(`INSERT INTO schema_migrations(version) VALUES (?)`, version)
	}
	mustExec(`INSERT INTO users(id,clerk_subject,created_at) VALUES (2,'user_existing','2026-11-01')`)
	return path
}

// Existing accounts keep today's behavior (agent writes apply at once); an
// account created after the migration starts asking for everything.
func TestProposalsMigrationAcceptsAllForExistingAccountsOnly(t *testing.T) {
	store, err := Open(databaseBeforeProposals(t))
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, id := range []int64{1, 2} {
		perms, err := store.ForUser(id).Permissions(ctx)
		if err != nil || !perms.AcceptAll {
			t.Fatalf("existing user %d: perms=%+v err=%v, want accept-all", id, perms, err)
		}
	}
	newID, err := store.ProvisionUser(ctx, "user_new")
	if err != nil {
		t.Fatal(err)
	}
	perms, err := store.ForUser(newID).Permissions(ctx)
	if err != nil || perms.AcceptAll || len(perms.Rules) != 0 {
		t.Fatalf("new user: perms=%+v err=%v, want ask-everything", perms, err)
	}
}

func proposal(tool string) training.Proposal {
	return training.Proposal{Tool: tool, ActionType: "set.log", Payload: json.RawMessage(`{"exercise":"banca"}`),
		Status: training.ProposalPending, CreatedAt: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)}
}

func TestProposalLifecycle(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	user := store.ForUser(1)
	p, err := user.CreateProposal(ctx, proposal("log_sets"))
	if err != nil || p.ID == 0 || p.Status != training.ProposalPending {
		t.Fatalf("create = %+v, %v", p, err)
	}
	if err := user.ClaimProposal(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := user.ClaimProposal(ctx, p.ID); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("second claim = %v, want ErrNotPending", err)
	}
	at := time.Date(2026, 11, 11, 10, 5, 0, 0, time.UTC)
	done, err := user.FinishProposal(ctx, p.ID, training.ProposalOutcome{Status: training.ProposalAccepted, DecidedBy: training.DecidedByPerson, Result: json.RawMessage(`{"ok":true}`), At: at})
	if err != nil || done.Status != training.ProposalAccepted || string(done.Result) != `{"ok":true}` || !done.DecidedAt.Equal(at) || done.DecidedBy != training.DecidedByPerson {
		t.Fatalf("finish = %+v, %v", done, err)
	}
	if _, err := user.RejectProposal(ctx, p.ID, training.DecidedByPerson, at); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("reject answered = %v", err)
	}
	pending, _ := user.CreateProposal(ctx, proposal("add_set"))
	rejected, err := user.RejectProposal(ctx, pending.ID, training.DecidedByDenial, at)
	if err != nil || rejected.Status != training.ProposalRejected || rejected.DecidedBy != training.DecidedByDenial {
		t.Fatalf("reject = %+v, %v", rejected, err)
	}
	open, _ := user.CreateProposal(ctx, proposal("delete_set"))
	list, err := user.ListProposals(ctx, training.ProposalFilter{Pending: true, Limit: 10})
	if err != nil || len(list) != 1 || list[0].ID != open.ID {
		t.Fatalf("pending = %+v, %v", list, err)
	}
	history, err := user.ListProposals(ctx, training.ProposalFilter{Limit: 10})
	if err != nil || len(history) != 2 || history[0].ID != pending.ID || history[1].ID != p.ID {
		t.Fatalf("history = %+v, %v", history, err)
	}
}

func TestRulesAndAcceptAll(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	user := store.ForUser(2)
	until := time.Date(2026, 11, 11, 22, 0, 0, 0, time.UTC)
	origin, err := user.RecordDecision(ctx, training.DecisionRecord{Time: until, Permission: "set.log", Outcome: training.OutcomeAllow,
		Operation: "proposal:1", Session: "coach:a", DeciderKind: training.DeciderSubject, DeciderID: training.AccountHolder, Channel: training.ChannelWeb, Answer: training.AnswerAllowAlways})
	if err != nil {
		t.Fatal(err)
	}
	grant := training.AnswerRule{ActionType: "set.log", Effect: training.EffectAllow, Session: "coach:a", ExpiresAt: until, Origin: origin}
	first, err := user.AddRule(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	// The same answer again in the same session replaces the first one.
	grant.ExpiresAt = until.Add(time.Hour)
	again, err := user.AddRule(ctx, grant)
	if err != nil || again != first {
		t.Fatalf("second grant = %d, %v; want id %d", again, err, first)
	}
	denial := grant
	denial.Effect = training.EffectDeny
	if _, err := user.AddRule(ctx, denial); err != nil {
		t.Fatal(err)
	}
	if err := user.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	perms, err := user.Permissions(ctx)
	if err != nil || !perms.AcceptAll || len(perms.Rules) != 2 || perms.Rules[0].Effect != training.EffectAllow || !perms.Rules[0].ExpiresAt.Equal(grant.ExpiresAt) || perms.Rules[0].Origin != origin {
		t.Fatalf("perms = %+v, %v", perms, err)
	}
	if err := user.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	forgotten, err := user.ForgetRule(ctx, first)
	if err != nil || forgotten.ID != first || forgotten.Session != "coach:a" {
		t.Fatalf("forget = %+v, %v", forgotten, err)
	}
	if _, err := user.ForgetRule(ctx, first); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("forget twice = %v", err)
	}
	perms, _ = user.Permissions(ctx)
	if perms.AcceptAll || len(perms.Rules) != 1 || perms.Rules[0].Effect != training.EffectDeny {
		t.Fatalf("after off and forget = %+v", perms)
	}
	for _, bad := range []training.AnswerRule{
		{ActionType: "set.log", Effect: "maybe", Session: "coach:a", ExpiresAt: until, Origin: origin},
		{ActionType: "set.log", Effect: training.EffectAllow, Session: "", ExpiresAt: until, Origin: origin},
		{ActionType: "set.log", Effect: training.EffectAllow, Session: "coach:a", Origin: origin},
		{ActionType: "set.log", Effect: training.EffectAllow, Session: "coach:a", ExpiresAt: until},
	} {
		if _, err := user.AddRule(ctx, bad); !errors.Is(err, training.ErrValidation) {
			t.Fatalf("stored %+v: %v", bad, err)
		}
	}
	records, err := user.Decisions(ctx, 10)
	if err != nil || len(records) != 1 || records[0].ID != origin || records[0].Session != "coach:a" || !records[0].Time.Equal(until) {
		t.Fatalf("decisions = %+v, %v", records, err)
	}
}

// databaseBeforeSessionAnswers is a database stopped before migration 18, with
// a permanent and a 12-hour answer for one account and accept-all on another.
func databaseBeforeSessionAnswers(t *testing.T) string {
	t.Helper()
	path := databaseBefore(t, sessionAnswersMigration)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO approval_rules(user_id,action_type,remember,expires_at,created_at) VALUES (2,'set.log','always','','2026-11-01T00:00:00Z')`,
		`INSERT INTO approval_rules(user_id,action_type,remember,expires_at,created_at) VALUES (2,'plan.create','session','2099-01-01T00:00:00Z','2026-11-01T00:00:00Z')`,
		`INSERT INTO proposals(user_id,tool,action_type,payload,status,decided_by,created_at) VALUES (2,'log_sets','set.log','{}','accepted','always','2026-11-01T00:00:00Z')`,
		`UPDATE agent_permissions SET accept_all=0 WHERE user_id=2`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

// Migration 18 drops every stored answer, which had no session, records each as
// revoked so the person is asked again, and leaves accept-all as it was.
func TestSessionAnswersMigrationDropsUnlimitedAnswers(t *testing.T) {
	store, err := Open(databaseBeforeSessionAnswers(t))
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	perms, err := store.ForUser(2).Permissions(ctx)
	if err != nil || perms.AcceptAll || len(perms.Rules) != 0 {
		t.Fatalf("user 2 perms = %+v, %v", perms, err)
	}
	if owner, _ := store.ForUser(1).Permissions(ctx); !owner.AcceptAll {
		t.Fatal("the owner's accept-all changed")
	}
	records, err := store.ForUser(2).Decisions(ctx, 10)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v, %v", records, err)
	}
	for _, r := range records {
		if r.Outcome != training.OutcomeRevoked || r.DeciderKind != training.DeciderPolicy || r.Channel != "migration:018" || r.Time.IsZero() {
			t.Fatalf("record = %+v", r)
		}
	}
	history, err := store.ForUser(2).ListProposals(ctx, training.ProposalFilter{Limit: 10})
	if err != nil || len(history) != 1 || history[0].DecidedBy != training.DecidedByAlways || history[0].Session != "" {
		t.Fatalf("old proposal = %+v, %v", history, err)
	}
}

// Another user's proposal id behaves as a missing one, and nobody's settings
// or answers reach another account.
func TestProposalsAreIsolatedBetweenUsers(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	owner, second := store.ForUser(1), store.ForUser(2)
	p, err := owner.CreateProposal(ctx, proposal("log_sets"))
	if err != nil {
		t.Fatal(err)
	}
	origin, err := owner.RecordDecision(ctx, training.DecisionRecord{Time: time.Now(), Permission: "set.log", Outcome: training.OutcomeAllow,
		DeciderKind: training.DeciderSubject, DeciderID: training.AccountHolder, Channel: training.ChannelWeb})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := owner.AddRule(ctx, training.AnswerRule{ActionType: "set.log", Effect: training.EffectAllow, Session: "coach:a", ExpiresAt: time.Now().Add(time.Hour), Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	before := dumpAll(t, store)
	if _, err := second.GetProposal(ctx, p.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("get other's = %v", err)
	}
	if err := second.ClaimProposal(ctx, p.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("claim other's = %v", err)
	}
	if _, err := second.RejectProposal(ctx, p.ID, training.DecidedByPerson, time.Now()); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("reject other's = %v", err)
	}
	if _, err := second.FinishProposal(ctx, p.ID, training.ProposalOutcome{Status: training.ProposalAccepted, At: time.Now()}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("finish other's = %v", err)
	}
	if _, err := second.ForgetRule(ctx, rule); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("forget other's = %v", err)
	}
	if _, err := second.AddRule(ctx, training.AnswerRule{ActionType: "set.log", Effect: training.EffectAllow, Session: "coach:a", ExpiresAt: time.Now().Add(time.Hour), Origin: origin}); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("a grant naming another account's record = %v", err)
	}
	if after := dumpAll(t, store); after != before {
		t.Fatalf("another user's calls changed data:\nbefore %s\nafter %s", before, after)
	}
	for _, pending := range []bool{true, false} {
		list, err := second.ListProposals(ctx, training.ProposalFilter{Pending: pending, Limit: 10})
		if err != nil || len(list) != 0 {
			t.Fatalf("second sees %+v, %v", list, err)
		}
	}
	perms, err := second.Permissions(ctx)
	if err != nil || perms.AcceptAll || len(perms.Rules) != 0 {
		t.Fatalf("second perms = %+v, %v", perms, err)
	}
	if records, err := second.Decisions(ctx, 10); err != nil || len(records) != 0 {
		t.Fatalf("second sees records %+v, %v", records, err)
	}
	if err := second.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	if perms, _ := store.ForUser(3).Permissions(ctx); perms.AcceptAll {
		t.Fatal("accept-all leaked to another account")
	}
}

// A proposal left applying (the process stopped mid-apply) stays visible in the
// history, so the person can check whether it landed.
func TestApplyingProposalShowsInHistory(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	user := store.ForUser(1)
	p, _ := user.CreateProposal(ctx, proposal("delete_set"))
	if err := user.ClaimProposal(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	history, err := user.ListProposals(ctx, training.ProposalFilter{Limit: 10})
	if err != nil || len(history) != 1 || history[0].Status != training.ProposalApplying {
		t.Fatalf("history = %+v, %v", history, err)
	}
}
