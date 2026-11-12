package sqlitestore

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const importDay = "2025-01-27"

// seedImport is one imported workout of one set, keyed "seed-key", on importDay.
func seedImport(exercise string) training.ImportRecord {
	return training.ImportRecord{Source: training.ImportSourceHevy, FileName: "workouts.csv", ContentHash: strings.Repeat("a", 64),
		Workouts: []training.ImportedWorkout{{Date: importDay, StartedAt: importDay + " 18:00:00", Notes: "empuje",
			Sets: []training.ImportedSet{{Key: "seed-key", Exercise: exercise, WeightKG: 60, Reps: 8, RPE: training.ImputedRPE, RPEImputed: true}}}},
		Mappings: map[string]string{"Bench Press (Barbell)": exercise}, CreatedAt: time.Unix(0, 0)}
}

// seedImporter gives a user everything seedUser does plus an import.
func seedImporter(t *testing.T, store *Store, userID int64, group string) {
	t.Helper()
	seedUser(t, store.ForUser(userID), group)
	if _, err := store.ImportsFor(userID).ApplyImport(context.Background(), seedImport("banca")); err != nil {
		t.Fatal(err)
	}
}

// importReadCases read everything the user has imported.
var importReadCases = map[string]func(context.Context, training.ImportStore) (any, error){
	"ImportedSetKeys": func(c context.Context, u training.ImportStore) (any, error) {
		return u.ImportedSetKeys(c, []string{"seed-key", "other"})
	},
	"DaySets": func(c context.Context, u training.ImportStore) (any, error) {
		return u.DaySets(c, "2000-01-01", "2099-12-31")
	},
	"ExerciseNames": func(c context.Context, u training.ImportStore) (any, error) { return u.ExerciseNames(c) },
	"ImportMappings": func(c context.Context, u training.ImportStore) (any, error) {
		return u.ImportMappings(c, training.ImportSourceHevy)
	},
	"ListImports": func(c context.Context, u training.ImportStore) (any, error) { return u.ListImports(c, 50) },
}

func TestImportReadsSeeNothingOfAnotherUser(t *testing.T) {
	for name, run := range importReadCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := openTwoUsers(t)
			seedImporter(t, store, 1, "pecho")
			owner, err := run(ctx, store.ImportsFor(1))
			if err != nil {
				t.Fatal(err)
			}
			alongside, err := run(ctx, store.ImportsFor(2))
			if err != nil {
				t.Fatal(err)
			}
			alone, err := Open(freshDB(t))
			if err != nil {
				t.Fatal(err)
			}
			defer alone.Close()
			want, err := run(ctx, alone.ImportsFor(1))
			if err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(owner, want) {
				t.Fatalf("the owner reads the same as an empty database, so this proves nothing: %#v", owner)
			}
			if !reflect.DeepEqual(alongside, want) {
				t.Fatalf("a user with no data sees %#v beside the owner, %#v on an empty database", alongside, want)
			}
		})
	}
}

// The owner already imported a set under the same key and the same file: user 2's
// own import still records it, and the owner's rows stay as they were.
func TestAnImportBelongsToTheUserWhoMadeIt(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	seedImporter(t, store, 1, "pecho")
	before := dumpUser(t, store, 1)
	summary, err := store.ImportsFor(2).ApplyImport(ctx, seedImport("sentadilla"))
	if err != nil || summary.Sets != 1 {
		t.Fatalf("summary = %+v, %v", summary, err)
	}
	n := countRows(t, store, `SELECT COUNT(*) FROM imported_sets k JOIN imports i ON i.id=k.import_id JOIN sets st ON st.id=k.set_id
		JOIN sessions se ON se.id=st.session_id WHERE k.user_id=2 AND i.user_id=2 AND se.user_id=2 AND k.natural_key='seed-key' AND st.exercise='sentadilla'`)
	if n != 1 {
		t.Fatalf("user 2's imported sets = %d", n)
	}
	if n := countRows(t, store, `SELECT COUNT(*) FROM import_exercise_map WHERE user_id=2 AND exercise='sentadilla'`); n != 1 {
		t.Fatalf("user 2's mappings = %d", n)
	}
	if after := dumpUser(t, store, 1); after != before {
		t.Fatalf("user 1's rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// A method added to the import port later must say how it is isolated, or this fails.
func TestEveryImportMethodHasATwoUserCase(t *testing.T) {
	port := reflect.TypeFor[training.ImportStore]()
	for i := range port.NumMethod() {
		name := port.Method(i).Name
		if _, ok := importReadCases[name]; !ok && name != "ApplyImport" {
			t.Errorf("%s has no two-user case", name)
		}
	}
}
