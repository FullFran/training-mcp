package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

func importRecord(workouts ...training.ImportedWorkout) training.ImportRecord {
	return training.ImportRecord{Source: training.ImportSourceHevy, FileName: "workouts.csv", ContentHash: strings.Repeat("b", 64),
		Workouts: workouts, Mappings: map[string]string{"Squat (Barbell)": "sentadilla"}, CreatedAt: time.Unix(100, 0)}
}

func workout(date string, sets ...training.ImportedSet) training.ImportedWorkout {
	return training.ImportedWorkout{Date: date, StartedAt: date + " 07:30:00", Notes: "pierna", Sets: sets}
}

func importedSet(key string, weight float64) training.ImportedSet {
	return training.ImportedSet{Key: key, Exercise: "sentadilla", WeightKG: weight, Reps: 5, RPE: 8.5}
}

func countRows(t *testing.T, store *Store, query string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportRecordsWorkoutsAsSessionsWithTheirNotesStartAndImputedMark(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	imputed := training.ImportedSet{Key: "k2", Exercise: "sentadilla", WeightKG: 100, Reps: 5, RPE: training.ImputedRPE, RPEImputed: true}
	summary, err := store.ImportsFor(1).ApplyImport(ctx, importRecord(workout("2025-01-27", importedSet("k1", 100), imputed)))
	if err != nil {
		t.Fatal(err)
	}
	if summary.ID == 0 || summary.Sessions != 1 || summary.Sets != 2 || summary.Imputed != 1 || summary.Source != "hevy" {
		t.Fatalf("summary = %+v", summary)
	}
	var sessionID int64
	var startedAt string
	if err = store.db.QueryRow(`SELECT id,started_at FROM sessions WHERE user_id=1`).Scan(&sessionID, &startedAt); err != nil {
		t.Fatal(err)
	}
	if startedAt != "2025-01-27 07:30:00" {
		t.Fatalf("started_at = %q", startedAt)
	}
	got, err := store.ForUser(1).GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Date != "2025-01-27" || got.Notes != "pierna" || len(got.Sets) != 2 {
		t.Fatalf("session = %+v", got)
	}
	if got.Sets[0].RPEImputed || got.Sets[0].RPE != 8.5 || !got.Sets[1].RPEImputed || got.Sets[1].RPE != training.ImputedRPE {
		t.Fatalf("sets = %+v", got.Sets)
	}
	if got.TotalSI != training.NormalizeSI(training.CalculateSI(8.5)+training.CalculateSI(8)) {
		t.Fatalf("total si = %v", got.TotalSI)
	}
	mappings, err := store.ImportsFor(1).ImportMappings(ctx, training.ImportSourceHevy)
	if err != nil || !reflect.DeepEqual(mappings, map[string]string{"Squat (Barbell)": "sentadilla"}) {
		t.Fatalf("mappings = %v, %v", mappings, err)
	}
	listed, err := store.ImportsFor(1).ListImports(ctx, 10)
	if err != nil || len(listed) != 1 || listed[0] != summary {
		t.Fatalf("imports = %+v, %v; want %+v", listed, err, summary)
	}
}

func TestImportingTheSameSetsTwiceAddsNothing(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	record := importRecord(workout("2025-01-27", importedSet("k1", 100), importedSet("k2", 105)))
	if _, err := store.ImportsFor(1).ApplyImport(ctx, record); err != nil {
		t.Fatal(err)
	}
	before := dumpAll(t, store)
	again, err := store.ImportsFor(1).ApplyImport(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	if again != (training.ImportSummary{}) {
		t.Fatalf("second import = %+v", again)
	}
	if after := dumpAll(t, store); after != before {
		t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestALaterExportAddsOnlyTheSetsNotImportedBefore(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	if _, err := store.ImportsFor(1).ApplyImport(ctx, importRecord(workout("2025-01-27", importedSet("k1", 100)))); err != nil {
		t.Fatal(err)
	}
	later := importRecord(workout("2025-01-27", importedSet("k1", 100)), workout("2025-01-29", importedSet("k3", 110)))
	summary, err := store.ImportsFor(1).ApplyImport(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Sessions != 1 || summary.Sets != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if n := countRows(t, store, `SELECT COUNT(*) FROM sessions WHERE user_id=1`); n != 2 {
		t.Fatalf("sessions = %d", n)
	}
	if n := countRows(t, store, `SELECT COUNT(*) FROM sets`); n != 2 {
		t.Fatalf("sets = %d", n)
	}
}

func TestADeletedImportedSetIsNotImportedAgain(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	record := importRecord(workout("2025-01-27", importedSet("k1", 100)))
	if _, err := store.ImportsFor(1).ApplyImport(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	summary, err := store.ImportsFor(1).ApplyImport(ctx, record)
	if err != nil || summary.Sets != 0 {
		t.Fatalf("summary = %+v, %v", summary, err)
	}
	if n := countRows(t, store, `SELECT COUNT(*) FROM imported_sets WHERE set_id IS NULL`); n != 1 {
		t.Fatalf("keys left without their set = %d", n)
	}
}

func TestAnImportIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	seedUser(t, store.ForUser(1), "pecho")
	before := dumpAll(t, store)
	failure := errors.New("disk full")
	store.failpoint = func(stage string) error {
		if stage == "import-inserted" {
			return failure
		}
		return nil
	}
	_, err := store.ImportsFor(1).ApplyImport(ctx, importRecord(workout("2025-02-03", importedSet("k1", 100)), workout("2025-02-05", importedSet("k2", 100))))
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
	if after := dumpAll(t, store); after != before {
		t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestDaySetsAndExerciseNamesReadTheUsersHistory(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	seedUser(t, store.ForUser(1), "pecho")
	if _, err := store.ImportsFor(1).ApplyImport(ctx, seedImport("banca")); err != nil {
		t.Fatal(err)
	}
	sets, err := store.ImportsFor(1).DaySets(ctx, importDay, importDay)
	if err != nil {
		t.Fatal(err)
	}
	if want := []training.DaySet{{Date: importDay, Exercise: "banca", WeightKG: 60, Reps: 8, Category: training.SetWorking}}; !reflect.DeepEqual(sets, want) {
		t.Fatalf("day sets = %+v", sets)
	}
	names, err := store.ImportsFor(1).ExerciseNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"banca", "curl", "remo"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v", names)
	}
}

// Sessions logged here carry no notes, and their JSON is what it was before imports.
func TestALoggedSessionStillHasNoNotes(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	seeded := seedUser(t, store.ForUser(1), "pecho")
	got, err := store.ForUser(1).GetSession(ctx, seeded.session)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes != "" || strings.Contains(string(encoded), "notes") {
		t.Fatalf("session = %s", encoded)
	}
}
