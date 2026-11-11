package importer

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// strip leaves what a test compares: everything but the keys, checked on their own.
func strip(sets []Set) []Set {
	out := make([]Set, len(sets))
	for i, s := range sets {
		s.Key = ""
		out[i] = s
	}
	return out
}

func TestParseHevyReadsWorkoutsOldestFirstWithTheirNotesAndSets(t *testing.T) {
	f, err := Parse(readFixture(t, "hevy_kg.csv"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != "hevy" || len(f.Hash) != 64 || f.Rows != 7 {
		t.Fatalf("file = %+v", f)
	}
	if len(f.Workouts) != 2 {
		t.Fatalf("workouts = %+v", f.Workouts)
	}
	legs, push := f.Workouts[0], f.Workouts[1]
	if legs.Date != "2025-12-20" || legs.StartedAt != "2025-12-20 18:30:00" || legs.Title != "Legs" {
		t.Fatalf("legs = %+v", legs)
	}
	if want := []Set{{Exercise: "Squat (Barbell)", WeightKG: 120.5, Reps: 5, RPE: 9}}; !reflect.DeepEqual(strip(legs.Sets), want) {
		t.Fatalf("legs sets = %+v", legs.Sets)
	}
	if push.StartedAt != "2025-12-22 08:00:00" || push.Notes != "Felt strong\nslept well\nBench Press (Barbell): Pause on chest" {
		t.Fatalf("push = %+v", push)
	}
	want := []Set{
		{Exercise: "Bench Press (Barbell)", WeightKG: 80, Reps: 8, RPE: 8.5},
		{Exercise: "Bench Press (Barbell)", WeightKG: 80, Reps: 7},
		{Exercise: "Bench Press (Barbell)", WeightKG: 60, Reps: 10, RPE: 10, Technique: "drop set"},
	}
	if !reflect.DeepEqual(strip(push.Sets), want) {
		t.Fatalf("push sets = %+v", push.Sets)
	}
	if want := map[string]int{SkipWarmup: 1, SkipNoLoad: 2}; !reflect.DeepEqual(f.Skipped, want) {
		t.Fatalf("skipped = %v", f.Skipped)
	}
}

func TestParseHevyConvertsPounds(t *testing.T) {
	f, err := Parse(readFixture(t, "hevy_lbs.csv"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Workouts[0].Sets[0].WeightKG; got != 102.06 {
		t.Fatalf("225 lb = %v kg", got)
	}
}

func TestParseStrongSkipsWarmupsRestTimersAndCardio(t *testing.T) {
	f, err := Parse(readFixture(t, "strong.csv"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != "strong" || len(f.Workouts) != 1 {
		t.Fatalf("file = %+v", f)
	}
	w := f.Workouts[0]
	if w.Date != "2024-03-07" || w.StartedAt != "2024-03-07 18:05:00" || w.Title != "Evening Workout" || w.Notes != "Knees ok\nSquat (Barbell): belt, low bar" {
		t.Fatalf("workout = %+v", w)
	}
	want := []Set{
		{Exercise: "Squat (Barbell)", WeightKG: 100, Reps: 5, RPE: 8},
		{Exercise: "Squat (Barbell)", WeightKG: 100, Reps: 5},
		{Exercise: "Leg Press", WeightKG: 140, Reps: 12, Technique: "drop set"},
	}
	if !reflect.DeepEqual(strip(w.Sets), want) {
		t.Fatalf("sets = %+v", w.Sets)
	}
	if want := map[string]int{SkipWarmup: 1, SkipNotASet: 1, SkipNoLoad: 1}; !reflect.DeepEqual(f.Skipped, want) {
		t.Fatalf("skipped = %v", f.Skipped)
	}
}

// Strong's file has no unit of its own: the person says which one the app used.
func TestParseStrongUsesTheUnitThePersonChose(t *testing.T) {
	f, err := Parse(readFixture(t, "strong.csv"), Options{StrongUnit: "lb"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Workouts[0].Sets[0].WeightKG; got != 45.36 {
		t.Fatalf("100 lb = %v kg", got)
	}
	if _, err = Parse(readFixture(t, "strong.csv"), Options{StrongUnit: "stone"}); !errors.Is(err, ErrFormat) {
		t.Fatalf("unknown unit: err = %v", err)
	}
}

// The newer export separates with semicolons, starts with a byte-order mark and
// names the unit in the weight header, which wins over the person's choice.
func TestParseStrongSemicolonExportWithUnitInTheHeader(t *testing.T) {
	f, err := Parse(readFixture(t, "strong_semicolon.csv"), Options{StrongUnit: "kg"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Set{{Exercise: "Deadlift (Barbell)", WeightKG: 142.88, Reps: 3}}; !reflect.DeepEqual(strip(f.Workouts[0].Sets), want) {
		t.Fatalf("sets = %+v", f.Workouts[0].Sets)
	}
}

func TestKeysAreStableAndTellSetsApart(t *testing.T) {
	first, err := Parse(readFixture(t, "hevy_kg.csv"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(readFixture(t, "hevy_kg.csv"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for wi, w := range first.Workouts {
		for si, s := range w.Sets {
			if s.Key == "" || seen[s.Key] || s.Key != again.Workouts[wi].Sets[si].Key {
				t.Fatalf("key %q of set %d.%d", s.Key, wi, si)
			}
			seen[s.Key] = true
		}
	}
	// Reordering a file's rows changes nothing about each set's key.
	lines := strings.Split(strings.TrimSpace(string(readFixture(t, "hevy_kg.csv"))), "\n")
	reordered := strings.Join(append([]string{lines[0], lines[len(lines)-1]}, lines[1:len(lines)-1]...), "\n")
	moved, err := Parse([]byte(reordered), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Workouts[1].Sets[0].Key != first.Workouts[1].Sets[0].Key || moved.Workouts[0].Sets[0].Key != first.Workouts[0].Sets[0].Key {
		t.Fatal("keys depend on row order across workouts")
	}
}

func TestParseRefusesWhatIsNotAnExport(t *testing.T) {
	cases := map[string]string{
		"empty":          "",
		"unknown header": "a,b,c\n1,2,3\n",
		"header only":    `"title","start_time","end_time","description","exercise_title","superset_id","exercise_notes","set_index","set_type","weight_kg","reps","distance_km","duration_seconds","rpe"` + "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body), Options{}); !errors.Is(err, ErrFormat) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestParseRefusesTooManyRowsOrBytes(t *testing.T) {
	var b strings.Builder
	b.WriteString("Date,Workout Name,Exercise Name,Set Order,Weight,Reps\n")
	for range MaxRows + 1 {
		b.WriteString("2024-03-07 18:05:00,W,Squat,1,100,5\n")
	}
	if _, err := Parse([]byte(b.String()), Options{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("rows: err = %v", err)
	}
	if _, err := Parse(make([]byte, MaxBytes+1), Options{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bytes: err = %v", err)
	}
}

func TestParseCountsUnreadableRows(t *testing.T) {
	body := "Date,Workout Name,Exercise Name,Set Order,Weight,Reps\n" +
		"yesterday,W,Squat,1,100,5\n" +
		"2024-03-07 18:05:00,W,Squat,1,heavy,5\n" +
		"2024-03-07 18:05:00,W,Squat,2,100,5\n"
	f, err := Parse([]byte(body), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Skipped[SkipUnreadable] != 2 || len(f.Workouts) != 1 || len(f.Workouts[0].Sets) != 1 {
		t.Fatalf("file = %+v", f)
	}
}

// Hevy writes dates in the app's language; Spanish month names read like English ones.
func TestParseHevyReadsSpanishDates(t *testing.T) {
	body := `"title","start_time","end_time","description","exercise_title","superset_id","exercise_notes","set_index","set_type","weight_kg","reps","distance_km","duration_seconds","rpe"
"Pierna","22 dic 2025, 8:05","22 dic 2025, 9:00","","Sentadilla",,"",0,"normal",100,5,,,
"Pierna","3 ene. 2026, 18:00","3 ene. 2026, 19:00","","Sentadilla",,"",0,"normal",100,5,,,
`
	f, err := Parse([]byte(body), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Workouts) != 2 || f.Workouts[0].StartedAt != "2025-12-22 08:05:00" || f.Workouts[1].StartedAt != "2026-01-03 18:00:00" {
		t.Fatalf("workouts = %+v", f.Workouts)
	}
}
