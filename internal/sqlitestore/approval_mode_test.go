package sqlitestore

import (
	"context"
	"database/sql"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

const approvalModeMigration = 27

// databaseBeforeApprovalMode has accounts 1 to 5 stopped before migration 27:
// 1 with accept-all on, 2 with it off, 3 with no row at all, 4 with accept-all
// on and 5 with it off.
func databaseBeforeApprovalMode(t *testing.T) string {
	t.Helper()
	path := databaseBefore(t, approvalModeMigration)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO users(id,clerk_subject,created_at) VALUES (3,'user_no_row','2026-11-02')`,
		`INSERT INTO users(id,clerk_subject,created_at) VALUES (4,'user_on','2026-11-02')`,
		`INSERT INTO users(id,clerk_subject,created_at) VALUES (5,'user_off','2026-11-02')`,
		`DELETE FROM agent_permissions WHERE user_id=3`,
		`INSERT INTO agent_permissions(user_id,accept_all,updated_at) VALUES (4,1,'2026-11-02'),(5,0,'2026-11-02')`,
		`UPDATE agent_permissions SET accept_all=0 WHERE user_id=2`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

// Existing accounts keep exactly their behavior; only accounts created after
// the migration start in accept_except_delete.
func TestApprovalModeMigrationKeepsExistingBehavior(t *testing.T) {
	store, err := Open(databaseBeforeApprovalMode(t))
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	want := map[int64]string{
		1: training.ApprovalAcceptAll,
		2: training.ApprovalReviewAll,
		3: training.ApprovalReviewAll,
		4: training.ApprovalAcceptAll,
		5: training.ApprovalReviewAll,
	}
	for id, mode := range want {
		perms, err := store.ForUser(id).Permissions(ctx)
		if err != nil || perms.Mode != mode {
			t.Errorf("user %d: perms=%+v err=%v, want %s", id, perms, err, mode)
		}
	}
	// The user with no row got an explicit review_all row, and accept_all agrees with the mode.
	for id, mode := range want {
		var stored string
		var acceptAll bool
		if err := store.db.QueryRow(`SELECT mode, accept_all FROM agent_permissions WHERE user_id=?`, id).Scan(&stored, &acceptAll); err != nil {
			t.Fatalf("user %d row: %v", id, err)
		}
		if stored != mode || acceptAll != (mode == training.ApprovalAcceptAll) {
			t.Errorf("user %d row = %s/%v, want %s", id, stored, acceptAll, mode)
		}
	}
	newID, err := store.ProvisionUser(ctx, "user_after")
	if err != nil {
		t.Fatal(err)
	}
	perms, err := store.ForUser(newID).Permissions(ctx)
	if err != nil || perms.Mode != training.ApprovalAcceptExceptDelete {
		t.Fatalf("new user perms = %+v, %v", perms, err)
	}
}

func TestSetApprovalModeStoresTheModeAndKeepsAcceptAllInStep(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	user := store.ForUser(2)
	for _, mode := range []string{training.ApprovalAcceptAll, training.ApprovalReviewAll, training.ApprovalAcceptExceptDelete} {
		if err := user.SetApprovalMode(ctx, mode); err != nil {
			t.Fatal(err)
		}
		perms, err := user.Permissions(ctx)
		var acceptAll bool
		store.db.QueryRow(`SELECT accept_all FROM agent_permissions WHERE user_id=2`).Scan(&acceptAll)
		if err != nil || perms.Mode != mode || acceptAll != (mode == training.ApprovalAcceptAll) {
			t.Fatalf("%s: perms=%+v accept_all=%v err=%v", mode, perms, acceptAll, err)
		}
	}
	if err := user.SetApprovalMode(ctx, "nope"); err == nil {
		t.Fatal("an unknown mode was stored")
	}
	if _, err := store.db.Exec(`UPDATE agent_permissions SET mode='nope' WHERE user_id=2`); err == nil {
		t.Fatal("the table accepts an unknown mode")
	}
}
