package sqlitestore

import (
	"strings"
	"testing"
)

// The runner records each migration by its number, so two files with one
// number would leave one of them silently unapplied.
func TestMigrationVersionsAreUnique(t *testing.T) {
	if _, err := migrationVersions(mustMigrationNames()); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationVersionsRefuseADuplicateNumber(t *testing.T) {
	_, err := migrationVersions([]string{"migrations/017_coach_memory.sql", "migrations/018_a.sql", "migrations/018_b.sql"})
	if err == nil || !strings.Contains(err.Error(), "018_a.sql") || !strings.Contains(err.Error(), "018_b.sql") {
		t.Fatalf("err = %v", err)
	}
	if _, err = migrationVersions([]string{"migrations/x_no_number.sql"}); err == nil {
		t.Fatal("a file with no number was accepted")
	}
}
