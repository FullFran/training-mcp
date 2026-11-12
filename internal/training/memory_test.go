package training

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// memoryFake records what the service asks the memory store for.
type memoryFake struct {
	UserStore
	saved     Memory
	pinned    *bool
	query     MemoryQuery
	listed    []Memory
	patchID   int64
	patch     MemoryPatch
	deletedID int64
}

func (m *memoryFake) SaveMemory(_ context.Context, in Memory, pinned *bool) (MemorySaved, error) {
	m.saved, m.pinned = in, pinned
	in.ID = 7
	return MemorySaved{Memory: in}, nil
}
func (m *memoryFake) GetMemory(_ context.Context, id int64) (Memory, error) {
	return Memory{ID: id, Type: MemoryNote}, nil
}
func (m *memoryFake) ListMemory(_ context.Context, q MemoryQuery) ([]Memory, error) {
	if q.View == MemoryViewDue {
		return nil, nil
	}
	m.query = q
	return m.listed, nil
}
func (m *memoryFake) ListMemoryConflicts(context.Context) ([]MemoryConflict, error) { return nil, nil }
func (m *memoryFake) UpdateMemory(_ context.Context, id int64, p MemoryPatch) (Memory, error) {
	m.patchID, m.patch = id, p
	return Memory{ID: id}, nil
}
func (m *memoryFake) DeleteMemory(_ context.Context, id int64) (Memory, error) {
	m.deletedID = id
	return Memory{ID: id}, nil
}

func memoryClock() time.Time { return time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC) }

func TestSaveMemoryNormalisesAndStamps(t *testing.T) {
	fake := &memoryFake{}
	svc := NewService(fake, memoryClock)
	got, err := svc.SaveMemory(context.Background(), MemoryInput{TopicKey: "  Lesion/Hombro Derecho ", Type: "limitation", Title: " Hombro ", Body: " Duele en press militar "})
	if err != nil {
		t.Fatal(err)
	}
	if got.Memory.ID != 7 || fake.saved.TopicKey != "lesion/hombro-derecho" || fake.saved.Title != "Hombro" || fake.saved.Body != "Duele en press militar" {
		t.Fatalf("saved %#v", fake.saved)
	}
	if !fake.saved.CreatedAt.Equal(memoryClock()) || !fake.saved.UpdatedAt.Equal(memoryClock()) {
		t.Fatalf("timestamps %#v", fake.saved)
	}
	if fake.pinned != nil {
		t.Fatalf("pinned should be left alone when not given, got %v", *fake.pinned)
	}
}

func TestSaveMemoryDerivesATopicKeyFromTypeAndTitle(t *testing.T) {
	fake := &memoryFake{}
	svc := NewService(fake, memoryClock)
	if _, err := svc.SaveMemory(context.Background(), MemoryInput{Title: "Sentadilla: barra baja", Body: "Prefiere barra baja."}); err != nil {
		t.Fatal(err)
	}
	if fake.saved.Type != MemoryNote || fake.saved.TopicKey != "note/sentadilla-barra-baja" {
		t.Fatalf("saved %#v", fake.saved)
	}
}

func TestSaveMemoryRejectsInvalidInput(t *testing.T) {
	long := strings.Repeat("a", maxMemoryBody+1)
	for name, in := range map[string]MemoryInput{
		"no title":      {Body: "x"},
		"no body":       {Title: "x"},
		"unknown type":  {Title: "x", Body: "x", Type: "secret"},
		"long body":     {Title: "x", Body: long},
		"long title":    {Title: strings.Repeat("t", maxMemoryTitle+1), Body: "x"},
		"long key":      {TopicKey: strings.Repeat("k", maxMemoryTopicKey+1), Title: "x", Body: "x"},
		"key of spaces": {TopicKey: " !! ", Title: "x", Body: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &memoryFake{}
			if _, err := NewService(fake, memoryClock).SaveMemory(context.Background(), in); !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v", err)
			}
			if fake.saved.Title != "" {
				t.Fatal("an invalid memory reached the store")
			}
		})
	}
}

func TestSearchMemoryBoundsTheLimitAndChecksTheType(t *testing.T) {
	fake := &memoryFake{}
	svc := NewService(fake, memoryClock)
	if _, err := svc.SearchMemory(context.Background(), MemoryQuery{Text: "hombro", Limit: 999}); err != nil {
		t.Fatal(err)
	}
	if fake.query.Limit != maxMemoryList || fake.query.Text != "hombro" {
		t.Fatalf("query %#v", fake.query)
	}
	if _, err := svc.SearchMemory(context.Background(), MemoryQuery{}); err != nil || fake.query.Limit != defaultMemorySearch {
		t.Fatalf("default limit %d, %v", fake.query.Limit, err)
	}
	if _, err := svc.SearchMemory(context.Background(), MemoryQuery{Type: "secret"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown type err = %v", err)
	}
}

// WeightUnit is kg: memory_context reports the person's unit.
func (*memoryFake) WeightUnit(context.Context) (string, error) { return "", nil }

func TestMemoryContextIsCompact(t *testing.T) {
	fake := &memoryFake{listed: []Memory{{ID: 1, Title: "t", Body: strings.Repeat("é", memoryContextBody+10), Pinned: true}}}
	got, err := NewService(fake, memoryClock).MemoryContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fake.query.Limit != memoryContextSize || fake.query.Text != "" {
		t.Fatalf("query %#v", fake.query)
	}
	if n := len([]rune(got.Memories[0].Body)); n != memoryContextBody+1 || !strings.HasSuffix(got.Memories[0].Body, "…") {
		t.Fatalf("body has %d runes: %q", n, got.Memories[0].Body)
	}
}

func TestUpdateMemoryValidatesThePatch(t *testing.T) {
	fake := &memoryFake{}
	svc := NewService(fake, memoryClock)
	empty := ""
	if _, err := svc.UpdateMemory(context.Background(), 3, MemoryPatch{Title: &empty}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title err = %v", err)
	}
	title, kind := " Nuevo ", "goal"
	if _, err := svc.UpdateMemory(context.Background(), 3, MemoryPatch{Title: &title, Type: &kind}); err != nil {
		t.Fatal(err)
	}
	if fake.patchID != 3 || *fake.patch.Title != "Nuevo" || !fake.patch.UpdatedAt.Equal(memoryClock()) {
		t.Fatalf("patch %d %#v", fake.patchID, fake.patch)
	}
	if _, err := svc.UpdateMemory(context.Background(), 0, MemoryPatch{Title: &title}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("id 0 err = %v", err)
	}
}

func TestDeleteMemoryRefusesANonPositiveID(t *testing.T) {
	fake := &memoryFake{}
	if _, err := NewService(fake, memoryClock).DeleteMemory(context.Background(), -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if fake.deletedID != 0 {
		t.Fatal("the store was asked to delete")
	}
}
