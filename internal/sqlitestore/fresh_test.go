package sqlitestore

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// freshDB is sqlitestoretest.Path for this package's own tests, which cannot
// import that package without a cycle. It returns a new, fully migrated
// database file, copied from one migrated once per test binary.
func freshDB(t testing.TB) string {
	t.Helper()
	freshOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sqlitestore-fresh-")
		if err != nil {
			freshErr = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "template.db")
		store, err := Open(path)
		if err != nil {
			freshErr = err
			return
		}
		if freshErr = store.Close(); freshErr != nil {
			return
		}
		freshTemplate, freshErr = os.ReadFile(path)
	})
	if freshErr != nil {
		t.Fatalf("migrating the template database: %v", freshErr)
	}
	path := filepath.Join(t.TempDir(), "training.db")
	if err := os.WriteFile(path, freshTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

var (
	freshOnce     sync.Once
	freshTemplate []byte
	freshErr      error
)
