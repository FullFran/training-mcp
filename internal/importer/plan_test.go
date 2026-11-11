package importer_test

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/importer"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

func clock() time.Time { return time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC) }

type world struct {
	services *training.Services
	db       *sql.DB
}

func newWorld(t *testing.T) world {
	t.Helper()
	path := t.TempDir() + "/training.db"
	store, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return world{services: training.NewServices(store, clock), db: db}
}

func hevyFile(t *testing.T) importer.File {
	t.Helper()
	data, err := os.ReadFile("testdata/hevy_kg.csv")
	if err != nil {
		t.Fatal(err)
	}
	f, err := importer.Parse(data, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPreviewOfANewHistoryCountsWhatWouldBeImported(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p, err := importer.Plan(ctx, w.services.ForUser(1), hevyFile(t), nil, "workouts.csv")
	if err != nil {
		t.Fatal(err)
	}
	if p.Sessions != 2 || p.Sets != 4 || p.Imputed != 1 || p.Exercises != 2 || p.Unmapped != 2 || p.AlreadyImported != 0 || p.InHistory != 0 {
		t.Fatalf("preview = %+v", p)
	}
	if p.From != "2025-12-20" || p.To != "2025-12-22" {
		t.Fatalf("range = %s..%s", p.From, p.To)
	}
	want := []importer.NameChoice{
		{Source: "Bench Press (Barbell)", Exercise: "bench press (barbell)", Sets: 3},
		{Source: "Squat (Barbell)", Exercise: "squat (barbell)", Sets: 1},
	}
	if !reflect.DeepEqual(p.Names, want) {
		t.Fatalf("names = %+v", p.Names)
	}
	// Previewing writes nothing.
	if n := count(t, w.db, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("sessions after preview = %d", n)
	}
}

func TestApplyImportsOnceAndASecondTimeAddsNothing(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	svc := w.services.ForUser(1)
	chosen := map[string]string{"Bench Press (Barbell)": "Press banca"}
	p, summary, err := importer.Apply(ctx, svc, hevyFile(t), chosen, "workouts.csv")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Sessions != 2 || summary.Sets != 4 || summary.Imputed != 1 || p.Sets != 4 {
		t.Fatalf("summary = %+v, preview = %+v", summary, p)
	}
	if n := count(t, w.db, `SELECT COUNT(*) FROM sets WHERE exercise='press banca' AND rpe_imputed=1 AND rpe=8 AND weight_kg=80 AND reps=7`); n != 1 {
		t.Fatalf("imputed set rows = %d", n)
	}
	if n := count(t, w.db, `SELECT COUNT(*) FROM sets WHERE exercise='press banca' AND technique='drop set' AND rpe=10 AND rpe_imputed=0`); n != 1 {
		t.Fatalf("drop set rows = %d", n)
	}

	again, err := importer.Plan(ctx, svc, hevyFile(t), nil, "workouts.csv")
	if err != nil {
		t.Fatal(err)
	}
	if again.Sets != 0 || again.Sessions != 0 || again.AlreadyImported != 4 {
		t.Fatalf("second preview = %+v", again)
	}
	// The remembered name comes back without being chosen again.
	if again.Names[0].Exercise != "press banca" || !again.Names[0].Remembered {
		t.Fatalf("names = %+v", again.Names)
	}
	_, second, err := importer.Apply(ctx, svc, hevyFile(t), nil, "workouts.csv")
	if err != nil {
		t.Fatal(err)
	}
	if second != (training.ImportSummary{}) || count(t, w.db, `SELECT COUNT(*) FROM sets`) != 4 || count(t, w.db, `SELECT COUNT(*) FROM imports`) != 1 {
		t.Fatalf("second import = %+v", second)
	}
}

// Sets already in the history, logged by hand before any import (like the 2025
// Hevy backfill entered with RPE 8 placeholders), are not imported a second time.
func TestSetsAlreadyInTheHistoryAreNotImportedAgain(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	svc := w.services.ForUser(1)
	session, err := svc.StartSession(ctx, "2025-12-20")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "sentadilla", WeightKG: 120.5, Reps: 5, RPE: 8, RPEImputed: true}); err != nil {
		t.Fatal(err)
	}
	p, err := importer.Plan(ctx, svc, hevyFile(t), map[string]string{"Squat (Barbell)": "Sentadilla"}, "workouts.csv")
	if err != nil {
		t.Fatal(err)
	}
	if p.InHistory != 1 || p.Sets != 3 || p.Sessions != 1 || p.Unmapped != 1 {
		t.Fatalf("preview = %+v", p)
	}
	if _, _, err = importer.Apply(ctx, svc, hevyFile(t), map[string]string{"Squat (Barbell)": "Sentadilla"}, "workouts.csv"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, w.db, `SELECT COUNT(*) FROM sets WHERE exercise='sentadilla'`); n != 1 {
		t.Fatalf("squat sets = %d", n)
	}
}

func TestNamesMatchTheCatalogueAndSuggestCloseOnes(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	svc := w.services.ForUser(1)
	if err := svc.SetExerciseNote(ctx, "squat (barbell)", "barra baja"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetExerciseNote(ctx, "bench press", "agarre medio"); err != nil {
		t.Fatal(err)
	}
	p, err := importer.Plan(ctx, svc, hevyFile(t), nil, "workouts.csv")
	if err != nil {
		t.Fatal(err)
	}
	want := []importer.NameChoice{
		{Source: "Bench Press (Barbell)", Exercise: "bench press (barbell)", Suggestion: "bench press", Sets: 3},
		{Source: "Squat (Barbell)", Exercise: "squat (barbell)", Known: true, Sets: 1},
	}
	if !reflect.DeepEqual(p.Names, want) || p.Unmapped != 1 {
		t.Fatalf("names = %+v, unmapped %d", p.Names, p.Unmapped)
	}
	if !reflect.DeepEqual(p.Catalogue, []string{"bench press", "squat (barbell)"}) {
		t.Fatalf("catalogue = %v", p.Catalogue)
	}
}

// One person's history, names and imports never shape another's import.
func TestAnotherUsersHistoryAndNamesDoNotCount(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	owner := w.services.ForUser(1)
	if _, _, err := importer.Apply(ctx, owner, hevyFile(t), map[string]string{"Bench Press (Barbell)": "banca"}, "workouts.csv"); err != nil {
		t.Fatal(err)
	}
	p, err := importer.Plan(ctx, w.services.ForUser(2), hevyFile(t), nil, "workouts.csv")
	if err != nil {
		t.Fatal(err)
	}
	if p.Sets != 4 || p.AlreadyImported != 0 || p.InHistory != 0 || p.Names[0].Exercise != "bench press (barbell)" || p.Names[0].Remembered || len(p.Catalogue) != 0 {
		t.Fatalf("second user's preview = %+v", p)
	}
}
