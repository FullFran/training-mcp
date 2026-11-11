package importer_test

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
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
	if p.Sessions != 2 || p.Sets != 5 || p.Warmups != 1 || p.Imputed != 1 || p.Exercises != 2 || p.Unmapped != 2 || p.AlreadyImported != 0 || p.InHistory != 0 {
		t.Fatalf("preview = %+v", p)
	}
	if p.From != "2025-12-20" || p.To != "2025-12-22" {
		t.Fatalf("range = %s..%s", p.From, p.To)
	}
	want := []importer.NameChoice{
		{Source: "Bench Press (Barbell)", Exercise: "bench press (barbell)", Sets: 4},
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
	if summary.Sessions != 2 || summary.Sets != 5 || summary.Imputed != 1 || p.Sets != 5 {
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
	if again.Sets != 0 || again.Sessions != 0 || again.AlreadyImported != 5 {
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
	if second != (training.ImportSummary{}) || count(t, w.db, `SELECT COUNT(*) FROM sets`) != 5 || count(t, w.db, `SELECT COUNT(*) FROM imports`) != 1 {
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
	if p.InHistory != 1 || p.Sets != 4 || p.Sessions != 1 || p.Unmapped != 1 {
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
		{Source: "Bench Press (Barbell)", Exercise: "bench press (barbell)", Suggestion: "bench press", Sets: 4},
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
	if p.Sets != 5 || p.AlreadyImported != 0 || p.InHistory != 0 || p.Names[0].Exercise != "bench press (barbell)" || p.Names[0].Remembered || len(p.Catalogue) != 0 {
		t.Fatalf("second user's preview = %+v", p)
	}
	// The same workouts get the second user's own sessions, never the owner's.
	if _, summary, err := importer.Apply(ctx, w.services.ForUser(2), hevyFile(t), nil, "workouts.csv"); err != nil || summary.Sessions != 2 || summary.Sets != 5 {
		t.Fatalf("second user's import = %+v, %v", summary, err)
	}
	if n := count(t, w.db, `SELECT COUNT(*) FROM sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=1`); n != 5 {
		t.Fatalf("owner's sets = %d", n)
	}
}

// The file's hash tells the preview the very same file was imported before.
func TestPreviewSaysWhenTheSameFileWasImportedBefore(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	svc := w.services.ForUser(1)
	if _, _, err := importer.Apply(ctx, svc, hevyFile(t), nil, "workouts.csv"); err != nil {
		t.Fatal(err)
	}
	p, err := importer.Plan(ctx, svc, hevyFile(t), nil, "again.csv")
	if err != nil {
		t.Fatal(err)
	}
	if p.ImportedBefore == nil || p.ImportedBefore.FileName != "workouts.csv" {
		t.Fatalf("imported before = %+v", p.ImportedBefore)
	}
	other, err := importer.Plan(ctx, w.services.ForUser(2), hevyFile(t), nil, "workouts.csv")
	if err != nil || other.ImportedBefore != nil {
		t.Fatalf("another user's preview = %+v, %v", other.ImportedBefore, err)
	}
}

// A name longer than a name may be still imports: it is clipped, and not remembered.
func TestAnOverlongExerciseNameStillImports(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	long := strings.Repeat("x", 140)
	body := `"title","start_time","end_time","description","exercise_title","superset_id","exercise_notes","set_index","set_type","weight_kg","reps","distance_km","duration_seconds","rpe"` +
		"\n\"Push\",\"22 Dec 2025, 08:00\",\"\",\"\",\"" + long + "\",,\"\",0,\"normal\",80,8,,,\n"
	f, err := importer.Parse([]byte(body), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, summary, err := importer.Apply(ctx, w.services.ForUser(1), f, nil, "long.csv")
	if err != nil || summary.Sets != 1 {
		t.Fatalf("summary = %+v, %v", summary, err)
	}
	if n := count(t, w.db, `SELECT COUNT(*) FROM import_exercise_map`); n != 0 {
		t.Fatalf("remembered %d overlong names", n)
	}
}

// withoutWarmups is the file as the importer read it while it skipped warm-up
// rows: the same working sets with the same keys.
func withoutWarmups(f importer.File) importer.File {
	out := f
	out.Workouts = nil
	for _, w := range f.Workouts {
		kept := w
		kept.Sets = nil
		for _, s := range w.Sets {
			if s.Category != training.SetWarmup {
				kept.Sets = append(kept.Sets, s)
			}
		}
		out.Workouts = append(out.Workouts, kept)
	}
	return out
}

// Warm-ups are imported as warm-ups. A file imported while warm-ups were
// skipped, imported again, gains only its warm-ups, and only once.
func TestReimportingAddsOnlyTheWarmupsOnce(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	svc := w.services.ForUser(1)
	chosen := map[string]string{"Bench Press (Barbell)": "Press banca"}
	if _, first, err := importer.Apply(ctx, svc, withoutWarmups(hevyFile(t)), chosen, "workouts.csv"); err != nil || first.Sets != 4 {
		t.Fatalf("first import = %+v, %v", first, err)
	}
	p, second, err := importer.Apply(ctx, svc, hevyFile(t), nil, "workouts.csv")
	if err != nil || second.Sets != 1 || second.Sessions != 0 || second.Imputed != 0 || p.AlreadyImported != 4 {
		t.Fatalf("second import = %+v, preview %+v, %v", second, p, err)
	}
	if n := count(t, w.db, `SELECT COUNT(*) FROM sets WHERE exercise='press banca' AND category='warmup' AND weight_kg=40 AND reps=10 AND si=0`); n != 1 {
		t.Fatalf("warm-up rows = %d", n)
	}
	// It joined the session its working sets are in.
	if n := count(t, w.db, `SELECT COUNT(DISTINCT session_id) FROM sets WHERE exercise='press banca'`); n != 1 {
		t.Fatalf("bench sessions = %d", n)
	}
	if _, third, err := importer.Apply(ctx, svc, hevyFile(t), nil, "workouts.csv"); err != nil || third.Sets != 0 {
		t.Fatalf("third import = %+v, %v", third, err)
	}
	if n := count(t, w.db, `SELECT COUNT(*) FROM sets`); n != 5 {
		t.Fatalf("sets = %d", n)
	}
}

// A set already in the history hides an imported one only when both are of
// the same category: a hand-logged working 40x10 is not the file's warm-up.
func TestHistoryMatchesSetsOfTheSameCategoryOnly(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	svc := w.services.ForUser(1)
	session, err := svc.StartSession(ctx, "2025-12-22")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "press banca", WeightKG: 40, Reps: 10, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	chosen := map[string]string{"Bench Press (Barbell)": "Press banca"}
	p, err := importer.Plan(ctx, svc, hevyFile(t), chosen, "workouts.csv")
	if err != nil || p.InHistory != 0 || p.Sets != 5 {
		t.Fatalf("working set hid the warm-up: %+v, %v", p, err)
	}
	if _, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "press banca", WeightKG: 40, Reps: 10, RPE: 6, Category: training.SetWarmup}); err != nil {
		t.Fatal(err)
	}
	if p, err = importer.Plan(ctx, svc, hevyFile(t), chosen, "workouts.csv"); err != nil || p.InHistory != 1 || p.Sets != 4 {
		t.Fatalf("logged warm-up did not hide the file's warm-up: %+v, %v", p, err)
	}
}
