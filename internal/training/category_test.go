package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

// recordingStore keeps what the service hands the store, so the normalisation
// rules can be checked without a database.
type recordingStore struct {
	*memoryStore
	added   []AddSetInput
	logged  []LogSetsRecord
	patches []SetPatch
}

func (r *recordingStore) AddSet(ctx context.Context, in AddSetInput) (Set, float64, error) {
	r.added = append(r.added, in)
	return r.memoryStore.AddSet(ctx, in)
}
func (r *recordingStore) LogSets(ctx context.Context, in LogSetsRecord) (LogSetsResult, error) {
	r.logged = append(r.logged, in)
	return LogSetsResult{}, nil
}
func (r *recordingStore) UpdateSet(ctx context.Context, id int64, p SetPatch) (Set, float64, error) {
	r.patches = append(r.patches, p)
	return Set{ID: id}, 0, nil
}

func newRecordingService() (*Service, *recordingStore) {
	store := &recordingStore{memoryStore: newMemoryStore()}
	return NewService(store, func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC) }), store
}

func strPtr(v string) *string { return &v }

func fingerprintOf(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func TestAddSetNormalizesCategory(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		category, technique string
		wantCategory        string
		wantTechnique       string
	}{
		{"", "", SetWorking, ""},
		{"working", "drop set", SetWorking, "drop set"},
		{" WarmUp ", "", SetWarmup, ""},
		{"warm-up", "", SetWarmup, ""},
		{"Calentamiento", "", SetWarmup, ""},
		// The way the coach recorded a warm-up before the category existed.
		{"", "Warm-Up", SetWarmup, ""},
		{"", "calentamiento", SetWarmup, ""},
	} {
		svc, store := newRecordingService()
		if _, _, err := svc.AddSet(ctx, AddSetInput{SessionID: 1, Exercise: "squat", WeightKG: 60, Reps: 5, RPE: 6, Category: tt.category, Technique: tt.technique}); err != nil {
			t.Fatalf("%+v: %v", tt, err)
		}
		got := store.added[0]
		if got.Category != tt.wantCategory || got.Technique != tt.wantTechnique {
			t.Fatalf("%+v stored category %q technique %q", tt, got.Category, got.Technique)
		}
	}
}

func TestUnknownCategoryIsRejectedByEveryWritePath(t *testing.T) {
	ctx := context.Background()
	svc, store := newRecordingService()
	if _, _, err := svc.AddSet(ctx, AddSetInput{SessionID: 1, Exercise: "squat", WeightKG: 60, Reps: 5, RPE: 6, Category: "top set"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("AddSet err = %v", err)
	}
	if _, _, err := svc.LogSet(ctx, "squat", 60, 5, 6, 1, "", "top set"); !errors.Is(err, ErrValidation) {
		t.Fatalf("LogSet err = %v", err)
	}
	if _, err := svc.LogSets(ctx, LogSetsInput{Key: "k", Exercise: "squat", Sets: []LoggedSet{{WeightKG: 60, Reps: 5, Category: "top set"}}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("LogSets err = %v", err)
	}
	if _, _, err := svc.UpdateSet(ctx, 1, SetPatch{Category: strPtr("top set")}); !errors.Is(err, ErrValidation) {
		t.Fatalf("UpdateSet err = %v", err)
	}
	if len(store.added)+len(store.logged)+len(store.patches) != 0 || len(store.sessions) != 0 {
		t.Fatalf("an invalid category reached the store: %+v %+v %+v %+v", store.added, store.logged, store.patches, store.sessions)
	}
}

// A batch with no warm-ups fingerprints as it did before categories existed,
// so a retry sent across the upgrade still replays instead of conflicting.
func TestLogSetsFingerprintOfWorkingSetsIsUnchanged(t *testing.T) {
	svc, store := newRecordingService()
	if _, err := svc.LogSets(context.Background(), LogSetsInput{Key: "k", Exercise: "squat", Sets: []LoggedSet{{WeightKG: 60, Reps: 5, RPE: 8, Category: "working"}}}); err != nil {
		t.Fatal(err)
	}
	// What the fingerprint was before categories existed.
	if got := store.logged[0].Fingerprint; got != fingerprintOf(`{"exercise":"squat","sets":[{"weight_kg":60,"reps":5,"rpe":8}]}`) {
		t.Fatalf("fingerprint = %s", got)
	}
	svc, store = newRecordingService()
	if _, err := svc.LogSets(context.Background(), LogSetsInput{Key: "k", Exercise: "squat", Sets: []LoggedSet{{WeightKG: 60, Reps: 5, RPE: 8, Category: "Warm-up"}}}); err != nil {
		t.Fatal(err)
	}
	if got := store.logged[0].Sets[0].Category; got != SetWarmup {
		t.Fatalf("warm-up category = %q", got)
	}
}

func TestUpdateSetWarmupWordInTechniqueMarksAWarmup(t *testing.T) {
	ctx := context.Background()
	svc, store := newRecordingService()
	if _, _, err := svc.UpdateSet(ctx, 1, SetPatch{Technique: strPtr("Warmup")}); err != nil {
		t.Fatal(err)
	}
	p := store.patches[0]
	if p.Category == nil || *p.Category != SetWarmup || p.Technique == nil || *p.Technique != "" {
		t.Fatalf("patch = %+v", p)
	}
	// An explicit category in the same call wins.
	if _, _, err := svc.UpdateSet(ctx, 1, SetPatch{Technique: strPtr("warmup"), Category: strPtr("working")}); err != nil {
		t.Fatal(err)
	}
	p = store.patches[1]
	if *p.Category != SetWorking || *p.Technique != "" {
		t.Fatalf("patch = %+v", p)
	}
}

func TestEveryWritePathRoundsWeightsToTwoDecimals(t *testing.T) {
	ctx := context.Background()
	const raw, want = 65.77089365, 65.77
	svc, store := newRecordingService()
	if _, _, err := svc.AddSet(ctx, AddSetInput{SessionID: 1, Exercise: "squat", WeightKG: raw, Reps: 5, RPE: 8}); err != nil || store.added[0].WeightKG != want {
		t.Fatalf("AddSet stored %v, %v", store.added, err)
	}
	if _, _, err := svc.LogSet(ctx, "squat", raw, 5, 8, 1, "", ""); err != nil || store.added[1].WeightKG != want {
		t.Fatalf("LogSet stored %v, %v", store.added, err)
	}
	if _, err := svc.LogSets(ctx, LogSetsInput{Key: "k", Exercise: "squat", Sets: []LoggedSet{{WeightKG: raw, Reps: 5}}}); err != nil || store.logged[0].Sets[0].WeightKG != want {
		t.Fatalf("LogSets stored %v, %v", store.logged, err)
	}
	w := raw
	if _, _, err := svc.UpdateSet(ctx, 1, SetPatch{WeightKG: &w}); err != nil || *store.patches[0].WeightKG != want {
		t.Fatalf("UpdateSet stored %v, %v", store.patches, err)
	}
	tiny := 0.001
	if _, _, err := svc.UpdateSet(ctx, 1, SetPatch{WeightKG: &tiny}); !errors.Is(err, ErrValidation) {
		t.Fatalf("a weight that rounds to zero was accepted: %v", err)
	}
}

func TestSetSIOfAWarmupIsZero(t *testing.T) {
	if got := SetSI(9, SetWarmup); got != 0 {
		t.Fatalf("warm-up SI = %v", got)
	}
	if got := SetSI(9, SetWorking); got != CalculateSI(9) {
		t.Fatalf("working SI = %v", got)
	}
}

// A declared warm-up is excluded with its own reason and does not set the
// session's top load, so a light working set is not mistaken for a warm-up.
func TestAnalyzeDropOffExcludesDeclaredWarmups(t *testing.T) {
	sets := []Set{
		{ID: 1, Position: 1, WeightKG: 200, Reps: 2, RPE: 6, Category: SetWarmup},
		{ID: 2, Position: 2, WeightKG: 100, Reps: 8, RPE: 8, Category: SetWorking},
		{ID: 3, Position: 3, WeightKG: 100, Reps: 7, RPE: 9, Category: SetWorking},
	}
	a := AnalyzeDropOff("squat", sets)
	if a.Sets[0].Comparable || a.Sets[0].ExcludedReason != ExcludedWarmup {
		t.Fatalf("warm-up = %+v", a.Sets[0])
	}
	if !a.Sets[1].Reference || !a.Sets[2].Comparable || !a.Comparable {
		t.Fatalf("working sets = %+v", a.Sets[1:])
	}
}
