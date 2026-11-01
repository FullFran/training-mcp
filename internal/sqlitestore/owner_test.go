package sqlitestore

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

const ownerMigration = 13

// databaseBeforeOwners builds a database as production has it, with rows in every
// table the owner migration touches, stopping at the migration before it.
func databaseBeforeOwners(t *testing.T) string {
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
		if version >= ownerMigration {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(string(body))
		mustExec(`INSERT INTO schema_migrations(version) VALUES (?)`, version)
	}
	mustExec(`INSERT INTO plans(id,name,created_at) VALUES (7,'Push','2026-01-01')`)
	mustExec(`INSERT INTO sessions(id,date,started_at,plan_id) VALUES (3,'2026-01-02','2026-01-02T10:00:00Z',7)`)
	mustExec(`INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at)
		VALUES (3,1,'Press',100,5,8,1.5,'2026-01-02','2026-01-02')`)
	mustExec(`INSERT INTO exercise_notes(exercise,note,updated_at) VALUES ('Press','Seat 4','2026-01-02')`)
	mustExec(`INSERT INTO exercise_groups(exercise,muscle_group,updated_at) VALUES ('Press','Chest','2026-01-02')`)
	mustExec(`INSERT INTO volume_landmarks(muscle_group,mev,mrv,updated_at) VALUES ('Chest',10,20,'2026-01-02')`)
	mustExec(`INSERT INTO idempotency_keys(key,fingerprint,response,created_at) VALUES ('k1','f1','{}','2026-01-02')`)
	return path
}

func TestOwnerMigrationKeepsEveryRowAndGivesItToTheOwner(t *testing.T) {
	store, err := Open(databaseBeforeOwners(t))
	if err != nil {
		t.Fatalf("migrating real-shaped data failed: %v", err)
	}
	defer store.Close()

	for _, table := range []string{"sessions", "plans", "exercise_notes", "exercise_groups", "volume_landmarks", "idempotency_keys"} {
		var owned int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE user_id=1`).Scan(&owned); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if owned != 1 {
			t.Errorf("%s: rows owned by the owner = %d, want 1", table, owned)
		}
	}

	// A child row would vanish if a parent table were rebuilt with cascades on.
	var sets, planID int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sets WHERE session_id=3`).Scan(&sets); err != nil {
		t.Fatal(err)
	}
	if sets != 1 {
		t.Errorf("sets kept = %d, want 1", sets)
	}
	if err := store.db.QueryRow(`SELECT plan_id FROM sessions WHERE id=3`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if planID != 7 {
		t.Errorf("session plan = %d, want 7", planID)
	}

	var note, group, fingerprint string
	var mev, mrv int
	if err := store.db.QueryRow(`SELECT note FROM exercise_notes WHERE exercise='Press'`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT muscle_group FROM exercise_groups WHERE exercise='Press'`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT mev,mrv FROM volume_landmarks WHERE muscle_group='Chest'`).Scan(&mev, &mrv); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT fingerprint FROM idempotency_keys WHERE key='k1'`).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if note != "Seat 4" || group != "Chest" || mev != 10 || mrv != 20 || fingerprint != "f1" {
		t.Errorf("values changed: note=%q group=%q mev=%d mrv=%d fingerprint=%q", note, group, mev, mrv, fingerprint)
	}

	var foreignKeys int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Error("foreign keys are off after migrating")
	}
}

func TestOwnerIsTheOnlyUserAndHasNoSubjectYet(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var users int
	var subject sql.NullString
	if err := store.db.QueryRow(`SELECT COUNT(*), MAX(clerk_subject) FROM users WHERE id=1`).Scan(&users, &subject); err != nil {
		t.Fatal(err)
	}
	if users != 1 || subject.Valid {
		t.Fatalf("owner rows = %d, subject = %+v", users, subject)
	}
	if _, err := store.db.Exec(`INSERT INTO users(id,clerk_subject,created_at) VALUES (2,'user_a','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO users(id,clerk_subject,created_at) VALUES (3,'user_a','now')`); err == nil {
		t.Fatal("two users shared one subject")
	}
}

func TestPersonalKeysAreUniquePerUserAndUpsertsLeaveOthersAlone(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO users(id,clerk_subject,created_at) VALUES (2,'user_other','x')`,
		`INSERT INTO exercise_notes(user_id,exercise,note,updated_at) VALUES (2,'Press','Other','x')`,
		`INSERT INTO exercise_groups(user_id,exercise,muscle_group,updated_at) VALUES (2,'Press','Other','x')`,
		`INSERT INTO volume_landmarks(user_id,muscle_group,mev,mrv,updated_at) VALUES (2,'Chest',1,2,'x')`,
		`INSERT INTO idempotency_keys(user_id,key,fingerprint,response,created_at) VALUES (2,'k1','other','{}','x')`,
	} {
		if _, err := store.db.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	// Twice, as the second write is the one that takes the conflict path.
	for range 2 {
		if err := store.SetExerciseNote(ctx, training.ExerciseNote{Exercise: "Press", Note: "Mine"}); err != nil {
			t.Fatal(err)
		}
		if err := store.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "Press", MuscleGroup: "Chest"}); err != nil {
			t.Fatal(err)
		}
		if err := store.SetLandmarks(ctx, training.Landmarks{MuscleGroup: "Chest", MEV: 10, MRV: 20}); err != nil {
			t.Fatal(err)
		}
	}

	var note, group string
	var mev int
	if err := store.db.QueryRow(`SELECT note FROM exercise_notes WHERE user_id=2`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT muscle_group FROM exercise_groups WHERE user_id=2`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT mev FROM volume_landmarks WHERE user_id=2`).Scan(&mev); err != nil {
		t.Fatal(err)
	}
	if note != "Other" || group != "Other" || mev != 1 {
		t.Fatalf("another user's rows changed: note=%q group=%q mev=%d", note, group, mev)
	}
	var mine string
	if err := store.db.QueryRow(`SELECT note FROM exercise_notes WHERE user_id=1`).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if mine != "Mine" {
		t.Fatalf("owner's note = %q", mine)
	}
}
