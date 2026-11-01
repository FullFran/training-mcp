package sqlitestore

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestProvisionUserIsOneAccountPerSubject(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if _, ok, err := store.UserBySubject(ctx, "user_a"); err != nil || ok {
		t.Fatalf("unknown subject: ok=%v err=%v", ok, err)
	}
	first, err := store.ProvisionUser(ctx, "user_a")
	if err != nil {
		t.Fatal(err)
	}
	if first == 1 {
		t.Fatal("a new subject was given the owner's account")
	}
	again, err := store.ProvisionUser(ctx, "user_a")
	if err != nil || again != first {
		t.Fatalf("second provision = %d, %v, want %d", again, err, first)
	}
	found, ok, err := store.UserBySubject(ctx, "user_a")
	if err != nil || !ok || found != first {
		t.Fatalf("lookup = %d, %v, %v, want %d", found, ok, err, first)
	}
	other, err := store.ProvisionUser(ctx, "user_b")
	if err != nil || other == first {
		t.Fatalf("other subject = %d, %v", other, err)
	}
}

// A client's first burst of calls arrives together, and must not split into two
// accounts or fail on the unique subject.
func TestConcurrentFirstCallsShareOneAccount(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const callers = 16
	ids := make([]int64, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = store.ProvisionUser(context.Background(), "user_a")
		}()
	}
	wg.Wait()
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("callers got different accounts: %v", ids)
		}
	}
	var users int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM users WHERE clerk_subject='user_a'`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Fatalf("accounts = %d", users)
	}
}
