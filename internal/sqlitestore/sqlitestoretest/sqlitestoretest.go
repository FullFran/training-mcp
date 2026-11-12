// Package sqlitestoretest gives tests a fully migrated database file without
// paying for the migrations every time.
//
// Applying every migration to an empty file is the slowest part of opening a
// store, and under -race it costs seconds. Path migrates once per test binary,
// keeps the resulting bytes, and writes a copy for each caller. The copy still
// goes through sqlitestore.Open, which finds every migration applied and
// leaves it alone, so callers exercise the same code path as production.
package sqlitestoretest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
)

var (
	once     sync.Once
	template []byte
	failure  error
)

// Path returns the path of a fresh, fully migrated database file inside
// t.TempDir(). Each call returns a new file that no other test shares.
func Path(t testing.TB) string {
	t.Helper()
	once.Do(func() { template, failure = migrated() })
	if failure != nil {
		t.Fatalf("migrating the template database: %v", failure)
	}
	path := filepath.Join(t.TempDir(), "training.db")
	if err := os.WriteFile(path, template, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func migrated() ([]byte, error) {
	dir, err := os.MkdirTemp("", "sqlitestoretest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	store, err := sqlitestore.Open(path)
	if err != nil {
		return nil, err
	}
	if err = store.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
