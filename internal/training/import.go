package training

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Sources of an imported history.
const (
	ImportSourceHevy   = "hevy"
	ImportSourceStrong = "strong"
)

const (
	// MaxImportSets caps one import. A decade of four sessions a week is well under it.
	MaxImportSets      = 10000
	maxImportKeyLen    = 128
	maxImportNameLen   = 120
	maxImportNotesLen  = 4000
	maxImportFileName  = 200
	maxImportListLimit = 100
)

// ImportedSet is one set of an imported workout, its exercise already in the
// person's own name. Key is its natural key in the source file: a set whose key
// was imported before is never recorded again.
type ImportedSet struct {
	Key        string
	Exercise   string
	WeightKG   float64
	Reps       int
	RPE        float64
	RPEImputed bool
	Technique  string
}

// ImportedWorkout becomes one session.
type ImportedWorkout struct {
	Date string
	// StartedAt is when the workout began, "2006-01-02 15:04:05" in the source's clock.
	StartedAt string
	Notes     string
	Sets      []ImportedSet
}

// ImportRecord is a confirmed import, ready to be stored all or nothing.
type ImportRecord struct {
	Source, FileName, ContentHash string
	Workouts                      []ImportedWorkout
	// Mappings remember how each of the source's exercise names was named here.
	Mappings  map[string]string
	CreatedAt time.Time
}

// ImportSummary is what one import recorded.
type ImportSummary struct {
	ID          int64     `json:"id"`
	Source      string    `json:"source"`
	FileName    string    `json:"file_name"`
	ContentHash string    `json:"content_hash"`
	Sessions    int       `json:"sessions"`
	Sets        int       `json:"sets"`
	Imputed     int       `json:"imputed"`
	CreatedAt   time.Time `json:"created_at"`
}

// DaySet is a recorded set reduced to what tells two copies of it apart.
type DaySet struct {
	Date     string
	Exercise string
	WeightKG float64
	Reps     int
}

// ImportStore keeps one user's imports.
type ImportStore interface {
	// ImportedSetKeys reports which of the keys an earlier import already recorded.
	ImportedSetKeys(context.Context, []string) (map[string]bool, error)
	// DaySets lists every set recorded between two dates, inclusive.
	DaySets(ctx context.Context, from, to string) ([]DaySet, error)
	// ExerciseNames is every exercise name the person uses, sorted.
	ExerciseNames(context.Context) ([]string, error)
	// ImportMappings is the person's remembered names for one source's exercises.
	ImportMappings(ctx context.Context, source string) (map[string]string, error)
	// ApplyImport records the sets whose keys are new, all or nothing. When none
	// are new it stores nothing and returns a zero summary.
	ApplyImport(context.Context, ImportRecord) (ImportSummary, error)
	ListImports(ctx context.Context, limit int) ([]ImportSummary, error)
}

// ErrImportUnavailable means this Service was built without an import store.
var ErrImportUnavailable = errors.New("imports are not available here")

func validImportSource(s string) bool { return s == ImportSourceHevy || s == ImportSourceStrong }

func (s *Service) ImportedSetKeys(ctx context.Context, keys []string) (map[string]bool, error) {
	if s.imports == nil {
		return nil, ErrImportUnavailable
	}
	if len(keys) > MaxImportSets {
		return nil, ErrValidation
	}
	if len(keys) == 0 {
		return map[string]bool{}, nil
	}
	return s.imports.ImportedSetKeys(ctx, keys)
}

func (s *Service) DaySets(ctx context.Context, from, to string) ([]DaySet, error) {
	if s.imports == nil {
		return nil, ErrImportUnavailable
	}
	if !validDate(from) || !validDate(to) || from > to {
		return nil, ErrValidation
	}
	return s.imports.DaySets(ctx, from, to)
}

func (s *Service) ExerciseNames(ctx context.Context) ([]string, error) {
	if s.imports == nil {
		return nil, ErrImportUnavailable
	}
	return s.imports.ExerciseNames(ctx)
}

func (s *Service) ImportMappings(ctx context.Context, source string) (map[string]string, error) {
	if s.imports == nil {
		return nil, ErrImportUnavailable
	}
	if !validImportSource(source) {
		return nil, ErrValidation
	}
	return s.imports.ImportMappings(ctx, source)
}

// ApplyImport is the person importing their own history: applied at once, never
// proposed. Every set obeys the rules a set logged by hand obeys.
func (s *Service) ApplyImport(ctx context.Context, in ImportRecord) (ImportSummary, error) {
	if s.imports == nil {
		return ImportSummary{}, ErrImportUnavailable
	}
	in.FileName = strings.TrimSpace(in.FileName)
	if !validImportSource(in.Source) || len(in.ContentHash) != 64 || len(in.FileName) > maxImportFileName {
		return ImportSummary{}, ErrValidation
	}
	total := 0
	for i := range in.Workouts {
		w := &in.Workouts[i]
		w.Notes = strings.TrimSpace(w.Notes)
		if !validDate(w.Date) || len(w.Notes) > maxImportNotesLen {
			return ImportSummary{}, ErrValidation
		}
		if at, err := time.Parse("2006-01-02 15:04:05", w.StartedAt); err != nil || at.Format("2006-01-02") != w.Date {
			return ImportSummary{}, ErrValidation
		}
		for j := range w.Sets {
			set := &w.Sets[j]
			set.Exercise = strings.ToLower(strings.TrimSpace(set.Exercise))
			set.Technique = normalizeTechnique(set.Technique)
			if set.Key == "" || len(set.Key) > maxImportKeyLen || len(set.Exercise) > maxImportNameLen ||
				len(set.Technique) > maxTechniqueLen || !validSetFields(set.Exercise, set.WeightKG, set.Reps, set.RPE) {
				return ImportSummary{}, ErrValidation
			}
			if set.RPEImputed && set.RPE != ImputedRPE {
				return ImportSummary{}, ErrValidation
			}
		}
		total += len(w.Sets)
	}
	if total > MaxImportSets {
		return ImportSummary{}, ErrValidation
	}
	mappings := make(map[string]string, len(in.Mappings))
	for from, to := range in.Mappings {
		from, to = strings.TrimSpace(from), strings.ToLower(strings.TrimSpace(to))
		if from == "" || to == "" || len(from) > maxImportNameLen || len(to) > maxImportNameLen {
			return ImportSummary{}, ErrValidation
		}
		mappings[from] = to
	}
	in.Mappings = mappings
	if in.CreatedAt.IsZero() {
		in.CreatedAt = s.clock()
	}
	return s.imports.ApplyImport(ctx, in)
}

func (s *Service) ListImports(ctx context.Context, limit int) ([]ImportSummary, error) {
	if s.imports == nil {
		return nil, ErrImportUnavailable
	}
	if limit < 0 || limit > maxImportListLimit {
		return nil, ErrValidation
	}
	if limit == 0 {
		limit = 20
	}
	return s.imports.ListImports(ctx, limit)
}
