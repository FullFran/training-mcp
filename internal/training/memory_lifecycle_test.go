package training

import (
	"context"
	"errors"
	"testing"
	"time"
)

// lifecycleFake is a memory store with a few facts, recording what the service asks.
type lifecycleFake struct {
	UserStore
	facts        map[int64]Memory
	saved        Memory
	queries      []MemoryQuery
	confirmed    time.Time
	judged       string
	resolved     string
	consolidated MemoryConsolidation
	forgot       string
}

func newLifecycleFake() *lifecycleFake {
	return &lifecycleFake{facts: map[int64]Memory{
		1: {ID: 1, TopicKey: "lesion/hombro", Type: MemoryLimitation, Title: "Hombro", Body: "duele", Revision: 2, UpdatedAt: memoryClock()},
		2: {ID: 2, TopicKey: "lesion/hombro-2", Type: MemoryLimitation, Title: "Hombro otra vez", Body: "sigue", Revision: 1, Pinned: true, UpdatedAt: memoryClock()},
	}}
}

func (f *lifecycleFake) SaveMemory(_ context.Context, m Memory, _ *bool) (MemorySaved, error) {
	f.saved = m
	return MemorySaved{Memory: m}, nil
}
func (f *lifecycleFake) GetMemory(_ context.Context, id int64) (Memory, error) {
	if m, ok := f.facts[id]; ok {
		return m, nil
	}
	return Memory{}, ErrNotFound
}
func (f *lifecycleFake) ListMemory(_ context.Context, q MemoryQuery) ([]Memory, error) {
	f.queries = append(f.queries, q)
	if q.View == MemoryViewDue {
		return []Memory{f.facts[1]}, nil
	}
	return []Memory{f.facts[1], f.facts[2]}, nil
}
func (f *lifecycleFake) ConfirmMemory(_ context.Context, id int64, reviewAfter time.Time) (Memory, error) {
	f.confirmed = reviewAfter
	return f.facts[id], nil
}
func (f *lifecycleFake) ForgetMemory(_ context.Context, id int64, reason string, _ time.Time) (Memory, error) {
	f.forgot = reason
	return f.facts[id], nil
}
func (f *lifecycleFake) JudgeMemory(_ context.Context, id int64, relation, _ string, _ time.Time) (MemoryRelation, error) {
	f.judged = relation
	return MemoryRelation{ID: id, Relation: relation}, nil
}
func (f *lifecycleFake) ResolveMemoryConflict(_ context.Context, _ int64, keep string, _ time.Time) error {
	f.resolved = keep
	return nil
}
func (f *lifecycleFake) ListMemoryConflicts(context.Context) ([]MemoryConflict, error) {
	return []MemoryConflict{{}}, nil
}
func (f *lifecycleFake) ConsolidateMemory(_ context.Context, c MemoryConsolidation) (Memory, error) {
	f.consolidated = c
	return c.Survivor, nil
}

func TestSavingSetsTheReviewCycleOfItsType(t *testing.T) {
	for kind, days := range map[string]int{MemoryLimitation: 14, MemoryGoal: 30, MemoryPreference: 180, MemoryEquipment: 180, MemoryWorked: 60, MemoryNote: 90} {
		fake := newLifecycleFake()
		if _, err := NewService(fake, memoryClock).SaveMemory(context.Background(), MemoryInput{Type: kind, Title: "t", Body: "b"}); err != nil {
			t.Fatal(err)
		}
		if want := memoryClock().AddDate(0, 0, days); !fake.saved.ReviewAfter.Equal(want) {
			t.Errorf("%s review after %v, want %v", kind, fake.saved.ReviewAfter, want)
		}
	}
}

func TestMemoryContextSurfacesDueFactsAndConflicts(t *testing.T) {
	fake := newLifecycleFake()
	got, err := NewService(fake, memoryClock).MemoryContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Memories) != 2 || len(got.Due) != 1 || got.Due[0].ID != 1 || got.Conflicts != 1 {
		t.Fatalf("context = %#v", got)
	}
	due := fake.queries[1]
	if due.View != MemoryViewDue || !due.Now.Equal(memoryClock()) || due.Limit != memoryContextDue {
		t.Fatalf("due query = %#v", due)
	}
	if fake.queries[0].View != MemoryViewActive {
		t.Fatalf("context reads %q facts", fake.queries[0].View)
	}
}

func TestSearchDefaultsToActiveFactsAndChecksTheView(t *testing.T) {
	fake := newLifecycleFake()
	svc := NewService(fake, memoryClock)
	if _, err := svc.SearchMemory(context.Background(), MemoryQuery{}); err != nil || fake.queries[0].View != MemoryViewActive {
		t.Fatalf("default view %q, %v", fake.queries[0].View, err)
	}
	if _, err := svc.SearchMemory(context.Background(), MemoryQuery{View: "secret"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown view err = %v", err)
	}
}

func TestConfirmingRestartsTheCycleOfTheFactsType(t *testing.T) {
	fake := newLifecycleFake()
	if _, err := NewService(fake, memoryClock).ConfirmMemory(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if want := memoryClock().AddDate(0, 0, 14); !fake.confirmed.Equal(want) {
		t.Fatalf("review after %v, want %v", fake.confirmed, want)
	}
	if _, err := NewService(fake, memoryClock).ConfirmMemory(context.Background(), 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestForgettingIsSoftAndSaysWhy(t *testing.T) {
	fake := newLifecycleFake()
	if _, err := NewService(fake, memoryClock).ForgetMemory(context.Background(), 1); err != nil || fake.forgot != MemoryForgotten {
		t.Fatalf("reason %q, %v", fake.forgot, err)
	}
}

func TestJudgingAcceptsOnlyKnownRelations(t *testing.T) {
	fake := newLifecycleFake()
	svc := NewService(fake, memoryClock)
	for _, r := range MemoryRelations {
		if _, err := svc.JudgeMemory(context.Background(), 3, r, "porque"); err != nil || fake.judged != r {
			t.Fatalf("%s: %v", r, err)
		}
	}
	if _, err := svc.JudgeMemory(context.Background(), 3, "replaces", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown relation err = %v", err)
	}
}

func TestResolvingAConflictKeepsNewOldOrBoth(t *testing.T) {
	fake := newLifecycleFake()
	svc := NewService(fake, memoryClock)
	for _, keep := range []string{KeepNew, KeepOld, KeepBoth} {
		if err := svc.ResolveMemoryConflict(context.Background(), 3, keep); err != nil || fake.resolved != keep {
			t.Fatalf("%s: %v", keep, err)
		}
	}
	if err := svc.ResolveMemoryConflict(context.Background(), 3, "neither"); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad keep err = %v", err)
	}
}

func TestConsolidationPreviewsThenNeedsTheSameFingerprint(t *testing.T) {
	ctx := context.Background()
	fake := newLifecycleFake()
	svc := NewService(fake, memoryClock)
	in := ConsolidationInput{MemoryIDs: []int64{2, 1, 2}, Type: MemoryLimitation, Title: "Hombro", Body: "duele y sigue"}

	plan, err := svc.PreviewConsolidation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Sources) != 2 || plan.Sources[0].ID != 1 || plan.Fingerprint == "" || !plan.Survivor.Pinned || plan.Survivor.TopicKey != "limitation/hombro" {
		t.Fatalf("plan = %#v", plan)
	}
	again, _ := svc.PreviewConsolidation(ctx, in)
	if again.Fingerprint != plan.Fingerprint {
		t.Fatal("the same plan has two fingerprints")
	}
	if fake.consolidated.Sources != nil {
		t.Fatal("a preview wrote")
	}

	in.Fingerprint = "stale"
	if _, err = svc.ConsolidateMemory(ctx, in); !errors.Is(err, ErrStale) {
		t.Fatalf("wrong fingerprint err = %v", err)
	}
	in.Fingerprint = plan.Fingerprint
	if _, err = svc.ConsolidateMemory(ctx, in); err != nil {
		t.Fatal(err)
	}
	if fake.consolidated.Sources[1] != 2 || fake.consolidated.Sources[2] != 1 || fake.consolidated.Survivor.Title != "Hombro" {
		t.Fatalf("consolidated %#v", fake.consolidated)
	}

	// A source edited after the preview changes the fingerprint.
	m := fake.facts[1]
	m.Revision = 3
	fake.facts[1] = m
	if _, err = svc.ConsolidateMemory(ctx, in); !errors.Is(err, ErrStale) {
		t.Fatalf("drifted source err = %v", err)
	}
}

func TestConsolidationNeedsTwoDistinctFacts(t *testing.T) {
	svc := NewService(newLifecycleFake(), memoryClock)
	for name, ids := range map[string][]int64{"one": {1}, "repeated": {1, 1}, "eleven": {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}} {
		if _, err := svc.PreviewConsolidation(context.Background(), ConsolidationInput{MemoryIDs: ids, Title: "t", Body: "b"}); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := svc.PreviewConsolidation(context.Background(), ConsolidationInput{MemoryIDs: []int64{1, 9}, Title: "t", Body: "b"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing source err = %v", err)
	}
}
