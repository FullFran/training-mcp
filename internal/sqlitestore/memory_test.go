package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

func memoryAt(minute int) time.Time { return time.Date(2026, 11, 11, 9, minute, 0, 0, time.UTC) }

func saveMemory(t *testing.T, u training.UserStore, key, title, body string, minute int) training.Memory {
	t.Helper()
	m, err := u.SaveMemory(context.Background(), training.Memory{TopicKey: key, Type: training.MemoryNote, Title: title, Body: body,
		CreatedAt: memoryAt(minute), UpdatedAt: memoryAt(minute)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m.Memory
}

func titles(ms []training.Memory) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Title)
	}
	return out
}

func TestSavingTheSameTopicKeyUpdatesTheFact(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	first := saveMemory(t, u, "lesion/hombro", "Hombro", "Duele en press militar", 0)
	pinned := true
	if _, err := u.UpdateMemory(ctx, first.ID, training.MemoryPatch{Pinned: &pinned, UpdatedAt: memoryAt(1)}); err != nil {
		t.Fatal(err)
	}
	saved, err := u.SaveMemory(ctx, training.Memory{TopicKey: "lesion/hombro", Type: training.MemoryLimitation, Title: "Hombro derecho",
		Body: "Ya no duele", SourceChatID: "chat-9", CreatedAt: memoryAt(5), UpdatedAt: memoryAt(5)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := saved.Memory
	if second.ID != first.ID || second.Title != "Hombro derecho" || second.Body != "Ya no duele" || second.Type != training.MemoryLimitation {
		t.Fatalf("second save = %#v", second)
	}
	if !second.CreatedAt.Equal(memoryAt(0)) || !second.UpdatedAt.Equal(memoryAt(5)) || second.SourceChatID != "chat-9" {
		t.Fatalf("dates or source = %#v", second)
	}
	if !second.Pinned {
		t.Fatal("a save without pinned unpinned the fact")
	}
	all, err := u.ListMemory(ctx, training.MemoryQuery{Limit: 10})
	if err != nil || len(all) != 1 {
		t.Fatalf("list = %v, %v", titles(all), err)
	}
	// The old wording is gone from the index, the new one is found.
	if old, _ := u.ListMemory(ctx, training.MemoryQuery{Text: "militar", Limit: 10}); len(old) != 0 {
		t.Fatalf("old body still indexed: %v", titles(old))
	}
	if found, _ := u.ListMemory(ctx, training.MemoryQuery{Text: "duele", Limit: 10}); len(found) != 1 {
		t.Fatalf("new body not indexed: %v", titles(found))
	}
}

func TestASaveWithoutASourceKeepsTheOldOne(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	if _, err := u.SaveMemory(ctx, training.Memory{TopicKey: "k", Type: "note", Title: "t", Body: "b", SourceChatID: "chat-1", CreatedAt: memoryAt(0), UpdatedAt: memoryAt(0)}, nil); err != nil {
		t.Fatal(err)
	}
	m := saveMemory(t, u, "k", "t", "b2", 1)
	if m.SourceChatID != "chat-1" {
		t.Fatalf("source = %q", m.SourceChatID)
	}
}

func TestListingPutsPinnedFirstThenTheMostRecent(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	old := saveMemory(t, u, "a", "antigua", "x", 0)
	saveMemory(t, u, "b", "media", "x", 1)
	saveMemory(t, u, "c", "nueva", "x", 2)
	pinned := true
	if _, err := u.UpdateMemory(ctx, old.ID, training.MemoryPatch{Pinned: &pinned, UpdatedAt: memoryAt(0)}); err != nil {
		t.Fatal(err)
	}
	got, err := u.ListMemory(ctx, training.MemoryQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"antigua", "nueva"}; len(got) != 2 || got[0].Title != want[0] || got[1].Title != want[1] {
		t.Fatalf("list = %v", titles(got))
	}
}

func TestSearchFoldsDiacriticsMatchesPrefixesAndFiltersByType(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	saveMemory(t, u, "lesion", "Lesión de rodilla", "Evitar sentadillas profundas", 0)
	if _, err := u.SaveMemory(ctx, training.Memory{TopicKey: "meta", Type: training.MemoryGoal, Title: "Sentadilla 140", Body: "Para marzo",
		CreatedAt: memoryAt(1), UpdatedAt: memoryAt(1)}, nil); err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]int{"lesion": 1, "sentadilla": 2, "rodi": 1, "nada": 0} {
		got, err := u.ListMemory(ctx, training.MemoryQuery{Text: text, Limit: 10})
		if err != nil || len(got) != want {
			t.Errorf("%q found %v, %v; want %d", text, titles(got), err, want)
		}
	}
	got, err := u.ListMemory(ctx, training.MemoryQuery{Text: "sentadilla", Type: training.MemoryGoal, Limit: 10})
	if err != nil || len(got) != 1 || got[0].Title != "Sentadilla 140" {
		t.Fatalf("typed search = %v, %v", titles(got), err)
	}
}

// Whatever the agent or the person types is words, never FTS syntax.
func TestSearchTextIsNeverParsedAsQuerySyntax(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	saveMemory(t, u, "k", "Rodilla", "dolor leve", 0)
	for _, text := range []string{`"`, `rodilla"`, `NEAR(`, `rodilla OR`, `-rodilla`, `*`, `title:rodilla`, `^rodilla`, `(`, `AND`, `'; DROP TABLE coach_memory; --`} {
		if _, err := u.ListMemory(ctx, training.MemoryQuery{Text: text, Limit: 10}); err != nil {
			t.Errorf("%q: %v", text, err)
		}
	}
	if got, err := u.ListMemory(ctx, training.MemoryQuery{Text: `"rodilla OR*`, Limit: 10}); err != nil || len(got) != 0 {
		t.Fatalf("operator-looking text must stay words: %v, %v", titles(got), err)
	}
	if got, err := u.ListMemory(ctx, training.MemoryQuery{Text: `!!!`, Limit: 10}); err != nil || len(got) != 1 {
		t.Fatalf("text with no words lists everything: %v, %v", titles(got), err)
	}
}

func TestDeletingAFactRemovesItFromSearch(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	m := saveMemory(t, u, "k", "Rodilla", "dolor", 0)
	gone, err := u.DeleteMemory(ctx, m.ID)
	if err != nil || gone.Title != "Rodilla" {
		t.Fatalf("delete = %#v, %v", gone, err)
	}
	if _, err := u.GetMemory(ctx, m.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
	if got, _ := u.ListMemory(ctx, training.MemoryQuery{Text: "rodilla", Limit: 10}); len(got) != 0 {
		t.Fatalf("still found: %v", titles(got))
	}
}

func TestUpdatingAFactKeepsUnsetFields(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	m := saveMemory(t, u, "k", "Rodilla", "dolor", 0)
	body := "sin dolor"
	got, err := u.UpdateMemory(ctx, m.ID, training.MemoryPatch{Body: &body, UpdatedAt: memoryAt(3)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Rodilla" || got.Body != "sin dolor" || got.Type != training.MemoryNote || got.TopicKey != "k" || !got.UpdatedAt.Equal(memoryAt(3)) {
		t.Fatalf("updated = %#v", got)
	}
}

func TestTwoUsersKeepTheSameTopicKeyApart(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	one, two := store.ForUser(1), store.ForUser(2)
	mine := saveMemory(t, one, "lesion/rodilla", "Rodilla de uno", "secreto de uno", 0)
	before := dumpUser(t, store, 1)

	theirs := saveMemory(t, two, "lesion/rodilla", "Rodilla de dos", "cosa de dos", 1)

	if theirs.ID == mine.ID {
		t.Fatal("user 2's save updated user 1's fact")
	}
	if after := dumpUser(t, store, 1); after != before {
		t.Fatalf("user 1's rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
	got, err := two.ListMemory(ctx, training.MemoryQuery{Text: "secreto", Limit: 10})
	if err != nil || len(got) != 0 {
		t.Fatalf("user 2 finds user 1's text: %v, %v", titles(got), err)
	}
}

// The export is a snapshot of the whole database; memory and its search index
// travel with it, so a restored backup still finds what was remembered.
func TestSnapshotCarriesMemoryAndItsIndex(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	saveMemory(t, store.ForUser(1), "k", "Rodilla", "dolor leve", 0)
	path := t.TempDir() + "/copy.db"
	if err := store.Snapshot(ctx, path); err != nil {
		t.Fatal(err)
	}
	copied, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	got, err := copied.ForUser(1).ListMemory(ctx, training.MemoryQuery{Text: "dolor", Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("restored search = %v, %v", titles(got), err)
	}
}

// A decomposed accent (e + U+0301) is part of its word, as a composed one is.
func TestSearchKeepsCombiningAccentsInTheWord(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	saveMemory(t, u, "k", "Lesión", "rodilla", 0)
	got, err := u.ListMemory(ctx, training.MemoryQuery{Text: "lesión", Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("decomposed accent found %v, %v", titles(got), err)
	}
}
