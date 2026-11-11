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

// databaseBeforeProposals is a database with two accounts, stopped before migration 14.
func databaseBeforeProposals(t *testing.T) string {
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
		if version >= proposalsMigration {
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
	if _, err := user.RejectProposal(ctx, p.ID, at); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("reject answered = %v", err)
	}
	pending, _ := user.CreateProposal(ctx, proposal("add_set"))
	rejected, err := user.RejectProposal(ctx, pending.ID, at)
	if err != nil || rejected.Status != training.ProposalRejected {
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
	if err := user.SetRule(ctx, training.ApprovalRule{ActionType: "set.log", Remember: training.RememberSession, ExpiresAt: until}); err != nil {
		t.Fatal(err)
	}
	if err := user.SetRule(ctx, training.ApprovalRule{ActionType: "set.log", Remember: training.RememberAlways}); err != nil {
		t.Fatal(err)
	}
	if err := user.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	perms, err := user.Permissions(ctx)
	if err != nil || !perms.AcceptAll || len(perms.Rules) != 1 || perms.Rules[0].Remember != training.RememberAlways {
		t.Fatalf("perms = %+v, %v", perms, err)
	}
	if err := user.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := user.ForgetRule(ctx, "set.log"); err != nil {
		t.Fatal(err)
	}
	perms, _ = user.Permissions(ctx)
	if perms.AcceptAll || len(perms.Rules) != 0 {
		t.Fatalf("after off and forget = %+v", perms)
	}
	if err := user.SetRule(ctx, training.ApprovalRule{ActionType: "set.log", Remember: "forever"}); err == nil {
		t.Fatal("an unknown remember value was stored")
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
	if err := owner.SetRule(ctx, training.ApprovalRule{ActionType: "set.log", Remember: training.RememberAlways}); err != nil {
		t.Fatal(err)
	}
	before := dumpAll(t, store)
	if _, err := second.GetProposal(ctx, p.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("get other's = %v", err)
	}
	if err := second.ClaimProposal(ctx, p.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("claim other's = %v", err)
	}
	if _, err := second.RejectProposal(ctx, p.ID, time.Now()); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("reject other's = %v", err)
	}
	if _, err := second.FinishProposal(ctx, p.ID, training.ProposalOutcome{Status: training.ProposalAccepted, At: time.Now()}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("finish other's = %v", err)
	}
	if err := second.ForgetRule(ctx, "set.log"); err != nil {
		t.Fatal(err)
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
	if err := second.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	if perms, _ := store.ForUser(3).Permissions(ctx); perms.AcceptAll {
		t.Fatal("accept-all leaked to another account")
	}
}
