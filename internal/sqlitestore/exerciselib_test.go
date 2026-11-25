package sqlitestore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

func libraryService(store *Store, user int64) *training.Service {
	clock := func() time.Time { return time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC) }
	return training.NewService(store.ForUser(user), clock)
}

// Migration 031 only adds two empty tables: a database from before it keeps
// every set and exercise group, and the library works on it.
func TestMigration031AddsAnEmptyLibraryAndKeepsExistingData(t *testing.T) {
	path := t.TempDir() + "/training.db"
	db := migrateTo(t, path, 30)
	for _, q := range []string{
		`INSERT INTO sessions(id,user_id,date,started_at) VALUES (1,1,'2026-11-20',CURRENT_TIMESTAMP)`,
		`INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at) VALUES (1,1,'press banca',80,8,8,0.6,'2026-11-20','2026-11-20')`,
		`INSERT INTO exercise_groups(user_id,exercise,muscle_group,updated_at) VALUES (1,'press banca','pecho','2026-11-20')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for _, table := range []string{"exercise_library_entries", "exercise_aliases"} {
		var n int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d rows, %v", table, n, err)
		}
	}
	ctx := context.Background()
	svc := libraryService(store, 1)
	groups, err := svc.ExerciseGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0] != (training.ExerciseGroup{Exercise: "press banca", MuscleGroup: "pecho"}) {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	needs, err := svc.ExercisesNeedingMapping(ctx)
	if err != nil || len(needs) != 1 || needs[0].Exercise != "press banca" || needs[0].Reason != training.NeedNotFound || needs[0].MuscleGroup != "pecho" {
		t.Fatalf("needs mapping = %+v, %v", needs, err)
	}
	if _, err := svc.SaveExerciseEntry(ctx, training.LibraryEntryInput{Name: "Press Banca", Primary: []string{"pecho"}, Secondary: []string{"triceps"}}); err != nil {
		t.Fatal(err)
	}
	// The tables refuse rows the service would never write.
	for _, q := range []string{
		`INSERT INTO exercise_library_entries(user_id,match_key,name,primary_muscles,secondary_muscles,based_on,updated_at) VALUES (1,'','x','["pecho"]','[]','','x')`,
		`INSERT INTO exercise_library_entries(user_id,match_key,name,primary_muscles,secondary_muscles,based_on,updated_at) VALUES (1,'y','y','[]','[]','','x')`,
		`INSERT INTO exercise_library_entries(user_id,match_key,name,primary_muscles,secondary_muscles,based_on,updated_at) VALUES (9,'z','z','["pecho"]','[]','','x')`,
		`INSERT INTO exercise_aliases(user_id,match_key,alias,target,updated_at) VALUES (1,'a','a','','x')`,
	} {
		if _, err := store.db.Exec(q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}
}

func TestLibraryRowsBelongToOnePerson(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	owner, second := libraryService(store, 1), libraryService(store, 2)
	if _, err := owner.SaveExerciseEntry(ctx, training.LibraryEntryInput{Name: "Bench Press - Powerlifting", Primary: []string{"pecho"}, Secondary: []string{"triceps"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.SaveExerciseEntry(ctx, training.LibraryEntryInput{Name: "Jalón al Pecho", Primary: []string{"espalda"}, Secondary: []string{"biceps"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.SetExerciseAlias(ctx, "Banca", "bench press - powerlifting"); err != nil {
		t.Fatal(err)
	}

	got, err := owner.LookupExercise(ctx, "banca")
	if err != nil || got.Layer != training.LayerOwn || got.Match != training.MatchAlias || got.Entry.BasedOn != "curated:bench press - powerlifting" {
		t.Fatalf("owner lookup = %+v, %v", got, err)
	}
	if got, err = owner.LookupExercise(ctx, "jalon al pecho"); err != nil || got.Layer != training.LayerOwn || got.Match != training.MatchNormalized {
		t.Fatalf("owner normalized lookup = %+v, %v", got, err)
	}
	if want := []string{"espalda"}; !reflect.DeepEqual(got.Entry.Muscles[0].MuscleGroup, want[0]) || got.Entry.Muscles[1].MuscleGroup != "biceps" || got.Entry.Muscles[1].Credit != 0.5 {
		t.Fatalf("stored muscles = %+v", got.Entry.Muscles)
	}

	// The second person sees the shared layers only.
	if got, err = second.LookupExercise(ctx, "banca"); err != nil || got.Found {
		t.Fatalf("second person resolved the owner's alias: %+v, %v", got, err)
	}
	if got, err = second.LookupExercise(ctx, "Bench Press - Powerlifting"); err != nil || got.Layer != training.LayerCurated {
		t.Fatalf("second person saw the owner's correction: %+v, %v", got, err)
	}
	if got, err = second.LookupExercise(ctx, "jalón al pecho"); err != nil || got.Found {
		t.Fatalf("second person saw the owner's entry: %+v, %v", got, err)
	}
	for _, c := range got.Candidates {
		if c.Layer == training.LayerOwn {
			t.Fatalf("second person's candidates hold the owner's entry: %+v", got.Candidates)
		}
	}
	// The same names saved by the second person are theirs alone.
	if _, err := second.SaveExerciseEntry(ctx, training.LibraryEntryInput{Name: "bench press - powerlifting", Primary: []string{"triceps"}}); err != nil {
		t.Fatal(err)
	}
	if got, err = owner.LookupExercise(ctx, "bench press - powerlifting"); err != nil || got.Entry.Muscles[0].MuscleGroup != "pecho" {
		t.Fatalf("owner's entry changed: %+v, %v", got, err)
	}
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM exercise_library_entries WHERE user_id=1`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("owner rows = %d, %v", rows, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM exercise_library_entries WHERE user_id=2`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("second rows = %d, %v", rows, err)
	}
}

func TestStoredEntryAndAliasRows(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	owner := libraryService(store, 1)
	if _, err := owner.SaveExerciseEntry(ctx, training.LibraryEntryInput{Name: "  Remo   Gironda ", Primary: []string{"espalda"}, Secondary: []string{"biceps", "hombro posterior"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.SetExerciseAlias(ctx, "Remo Sentado", "remo gironda"); err != nil {
		t.Fatal(err)
	}
	var key, name, primary, secondary, basedOn string
	if err := store.db.QueryRow(`SELECT match_key,name,primary_muscles,secondary_muscles,based_on FROM exercise_library_entries WHERE user_id=1`).
		Scan(&key, &name, &primary, &secondary, &basedOn); err != nil {
		t.Fatal(err)
	}
	if key != "remo gironda" || name != "remo gironda" || primary != `["espalda"]` || secondary != `["biceps","hombro posterior"]` || basedOn != "" {
		t.Fatalf("entry row = %q %q %s %s %q", key, name, primary, secondary, basedOn)
	}
	var alias, target string
	if err := store.db.QueryRow(`SELECT match_key,alias,target FROM exercise_aliases WHERE user_id=1`).Scan(&key, &alias, &target); err != nil {
		t.Fatal(err)
	}
	if key != "remo sentado" || alias != "remo sentado" || target != "remo gironda" {
		t.Fatalf("alias row = %q %q %q", key, alias, target)
	}
	// Setting it again points it elsewhere, in the same row.
	if _, err := owner.SetExerciseAlias(ctx, "remo sentado", "Bent Over Barbell Row"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*),MAX(target) FROM exercise_aliases WHERE user_id=1`).Scan(&n, &target); err != nil || n != 1 || target != "Bent Over Barbell Row" {
		t.Fatalf("aliases = %d, target %q, %v", n, target, err)
	}
}

func TestExerciseVolumeIsPerWeekAndExerciseOverWorkingSets(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	owner, second := store.ForUser(1), store.ForUser(2)
	add := func(user training.UserStore, date, exercise, category string, n int) {
		t.Helper()
		s, err := user.Start(ctx, training.Session{Date: date})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, _, err := user.AddSet(ctx, training.AddSetInput{SessionID: s.ID, Exercise: exercise, WeightKG: 80, Reps: 8, RPE: 8, Category: category}); err != nil {
				t.Fatal(err)
			}
		}
	}
	add(owner, "2026-11-17", "banca", "working", 2) // Tuesday: week of Monday 16
	add(owner, "2026-11-22", "banca", "working", 1) // Sunday: same week
	add(owner, "2026-11-22", "banca", "warmup", 3)  // not counted
	add(owner, "2026-11-24", "remo", "working", 1)  // week of Monday 23
	add(second, "2026-11-17", "banca", "working", 5)
	if err := owner.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "banca", MuscleGroup: "pecho"}); err != nil {
		t.Fatal(err)
	}
	ls := owner.(training.ExerciseLibraryStore)
	got, err := ls.ExerciseVolume(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].WeekStart != "2026-11-23" || got[0].Exercise != "remo" || got[0].MuscleGroup != "" || got[0].Sets != 1 ||
		got[1].WeekStart != "2026-11-16" || got[1].Exercise != "banca" || got[1].MuscleGroup != "pecho" || got[1].Sets != 3 || got[1].TotalSI <= 0 {
		t.Fatalf("volume = %+v", got)
	}
	// The weekly totals it sits beside count the same sets.
	weekly, err := owner.WeeklyVolume(ctx, training.ListFilter{})
	if err != nil || len(weekly) != 2 || weekly[1].Sets != got[1].Sets || weekly[1].TotalSI != got[1].TotalSI {
		t.Fatalf("weekly = %+v, %v", weekly, err)
	}
	if got, err = ls.ExerciseVolume(ctx, training.ListFilter{From: "2026-11-23", To: "2026-11-30"}); err != nil || len(got) != 1 || got[0].Exercise != "remo" {
		t.Fatalf("ranged volume = %+v, %v", got, err)
	}
}

// Saving an entry and setting an alias are undone like any other change: the
// row goes back to what it was, or away when the change added it.
func TestLibraryChangesAreUndone(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	user := store.ForUser(1)
	ls := user.(training.ExerciseLibraryStore)
	if err := ls.SaveOwnExerciseEntry(ctx, training.OwnExerciseEntry{Key: "remo gironda", Name: "remo gironda", Primary: []string{"espalda"}}); err != nil {
		t.Fatal(err)
	}
	if err := ls.SetExerciseAlias(ctx, training.ExerciseAlias{Key: "remo", Alias: "remo", Target: "remo gironda"}); err != nil {
		t.Fatal(err)
	}
	other := store.ForUser(2).(training.ExerciseLibraryStore)
	if err := other.SaveOwnExerciseEntry(ctx, training.OwnExerciseEntry{Key: "remo gironda", Name: "remo gironda", Primary: []string{"biceps"}}); err != nil {
		t.Fatal(err)
	}
	before := dumpData(t, store)
	id := acceptedWithUndo(t, user, training.UndoScope{}, func(ctx context.Context) error {
		if err := ls.SaveOwnExerciseEntry(ctx, training.OwnExerciseEntry{Key: "remo gironda", Name: "remo gironda", Primary: []string{"espalda"}, Secondary: []string{"biceps"}}); err != nil {
			return err
		}
		if err := ls.SaveOwnExerciseEntry(ctx, training.OwnExerciseEntry{Key: "dominadas", Name: "dominadas", Primary: []string{"espalda"}}); err != nil {
			return err
		}
		return ls.SetExerciseAlias(ctx, training.ExerciseAlias{Key: "remo", Alias: "remo", Target: "Bent Over Barbell Row"})
	})
	if dumpData(t, store) == before {
		t.Fatal("the change changed nothing")
	}
	if _, err := undo(user, id); err != nil {
		t.Fatal(err)
	}
	if got := dumpData(t, store); got != before {
		t.Fatalf("undo did not restore:\n%s\nwant\n%s", got, before)
	}
}
