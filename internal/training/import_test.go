package training

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// recordingImports is an ImportStore that keeps what it was asked to apply.
type recordingImports struct {
	ImportStore
	applied []ImportRecord
}

func (r *recordingImports) ApplyImport(_ context.Context, in ImportRecord) (ImportSummary, error) {
	r.applied = append(r.applied, in)
	return ImportSummary{ID: 1}, nil
}

func validImport() ImportRecord {
	return ImportRecord{Source: ImportSourceStrong, FileName: " strong.csv ", ContentHash: strings.Repeat("c", 64),
		Workouts: []ImportedWorkout{{Date: "2024-03-07", StartedAt: "2024-03-07 18:05:00", Notes: " piernas ",
			Sets: []ImportedSet{{Key: "k", Exercise: " Sentadilla ", WeightKG: 100, Reps: 5, RPE: ImputedRPE, RPEImputed: true, Technique: " Drop Set "}}}},
		Mappings: map[string]string{" Squat (Barbell) ": " Sentadilla "}}
}

func importService(imports ImportStore) *Service {
	svc := NewService(newMemoryStore(), func() time.Time { return time.Unix(42, 0) })
	svc.imports = imports
	return svc
}

func TestApplyImportNormalizesLikeAHandLoggedSet(t *testing.T) {
	imports := &recordingImports{}
	if _, err := importService(imports).ApplyImport(context.Background(), validImport()); err != nil {
		t.Fatal(err)
	}
	got := imports.applied[0]
	set := got.Workouts[0].Sets[0]
	if got.FileName != "strong.csv" || got.Workouts[0].Notes != "piernas" || set.Exercise != "sentadilla" || set.Technique != "drop set" {
		t.Fatalf("applied = %+v", got)
	}
	if got.Mappings["Squat (Barbell)"] != "sentadilla" || len(got.Mappings) != 1 {
		t.Fatalf("mappings = %v", got.Mappings)
	}
	if !got.CreatedAt.Equal(time.Unix(42, 0)) {
		t.Fatalf("created at = %v", got.CreatedAt)
	}
}

func TestApplyImportRefusesWhatAHandLoggedSetCouldNotBe(t *testing.T) {
	cases := map[string]func(*ImportRecord){
		"unknown source":       func(r *ImportRecord) { r.Source = "fitbod" },
		"short hash":           func(r *ImportRecord) { r.ContentHash = "abc" },
		"bad date":             func(r *ImportRecord) { r.Workouts[0].Date = "2024-02-30" },
		"start on another day": func(r *ImportRecord) { r.Workouts[0].StartedAt = "2024-03-08 01:00:00" },
		"no weight":            func(r *ImportRecord) { r.Workouts[0].Sets[0].WeightKG = 0 },
		"no reps":              func(r *ImportRecord) { r.Workouts[0].Sets[0].Reps = 0 },
		"rpe above 10":         func(r *ImportRecord) { r.Workouts[0].Sets[0].RPE, r.Workouts[0].Sets[0].RPEImputed = 11, false },
		"imputed is not 8":     func(r *ImportRecord) { r.Workouts[0].Sets[0].RPE = 9 },
		"no key":               func(r *ImportRecord) { r.Workouts[0].Sets[0].Key = "" },
		"empty mapping":        func(r *ImportRecord) { r.Mappings["Row"] = " " },
		"too many sets": func(r *ImportRecord) {
			sets := make([]ImportedSet, MaxImportSets+1)
			for i := range sets {
				sets[i] = r.Workouts[0].Sets[0]
			}
			r.Workouts[0].Sets = sets
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			imports := &recordingImports{}
			record := validImport()
			change(&record)
			if _, err := importService(imports).ApplyImport(context.Background(), record); !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v", err)
			}
			if len(imports.applied) != 0 {
				t.Fatal("an invalid import reached the store")
			}
		})
	}
}

func TestAServiceWithoutAnImportStoreSaysSo(t *testing.T) {
	if _, err := importService(nil).ApplyImport(context.Background(), validImport()); !errors.Is(err, ErrImportUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
