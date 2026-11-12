package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

type proposedMemory struct {
	userID  int64
	session string
	in      training.MemoryInput
}

// fakeMemory plays the host's memory for the engine tests: each person's facts,
// and the facts proposed for them.
type fakeMemory struct {
	mu          sync.Mutex
	facts       map[int64]training.MemoryContext
	failContext bool
	failPropose bool
	proposed    []proposedMemory
}

func (f *fakeMemory) MemoryContext(_ context.Context, userID int64) (training.MemoryContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failContext {
		return training.MemoryContext{}, errors.New("memory store down")
	}
	return f.facts[userID], nil
}

func (f *fakeMemory) ProposeMemory(_ context.Context, userID int64, session string, in training.MemoryInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPropose {
		return errors.New("proposal store down")
	}
	f.proposed = append(f.proposed, proposedMemory{userID: userID, session: session, in: in})
	return nil
}

func (f *fakeMemory) proposals() []proposedMemory {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proposedMemory(nil), f.proposed...)
}

func withMemory(store *memoryConversations, mem *fakeMemory) func(*Settings) {
	return func(s *Settings) {
		s.Conversations = store
		s.Memory = mem
	}
}

func twoPeoplesMemory() *fakeMemory {
	return &fakeMemory{facts: map[int64]training.MemoryContext{
		1: {
			Memories:  []training.Memory{{ID: 11, Type: "limitation", Title: "Hombro derecho", Body: "Molesta en press inclinado", Pinned: true}},
			Due:       []training.Memory{{ID: 11, Type: "limitation", Title: "Hombro derecho"}},
			Conflicts: 2,
		},
		2: {Memories: []training.Memory{{ID: 21, Type: "goal", Title: "Rodilla fuerte", Body: "Sentadilla sin dolor"}}},
	}}
}

// A chat that starts, new or reopened, gives the fresh engine that person's
// memory as host lines, once; nobody else's memory ever reaches it.
func TestStartingAChatGivesTheEngineThatPersonsMemory(t *testing.T) {
	store := newMemoryConversations()
	mem := twoPeoplesMemory()
	h := newHarness(t, "ok", withMemory(store, mem))
	h.connect(1)
	h.connect(2)

	a, answer := turn(t, h, 1, 0, "hola")
	for _, want := range []string{
		ContextMarker + ` memory id=11 type=limitation pinned=true title="Hombro derecho" body="Molesta en press inclinado"`,
		ContextMarker + ` memory_due id=11 type=limitation title="Hombro derecho"`,
		ContextMarker + ` memory_conflicts=2`,
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("new chat lacks %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "Rodilla") {
		t.Fatalf("another person's memory reached the engine:\n%s", answer)
	}
	if _, again := turn(t, h, 1, a, "sigo"); strings.Contains(again, " memory") {
		t.Fatalf("memory given twice to the same engine:\n%s", again)
	}

	_, theirs := turn(t, h, 2, 0, "hola")
	if !strings.Contains(theirs, `title="Rodilla fuerte"`) || strings.Contains(theirs, "Hombro") {
		t.Fatalf("second person's chat:\n%s", theirs)
	}

	// Another chat, then the first one reopened: a fresh engine each time.
	turn(t, h, 1, 0, "otra cosa")
	_, reopened := turn(t, h, 1, a, "vuelvo")
	if !strings.Contains(reopened, `title="Hombro derecho"`) || !strings.Contains(reopened, " history person=") {
		t.Fatalf("reopened chat:\n%s", reopened)
	}
	if i, j := strings.Index(reopened, " memory id="), strings.Index(reopened, " history person="); i > j {
		t.Fatalf("memory should come before the history:\n%s", reopened)
	}
}

// Text the person saved as memory can only read as data: quoted, cut short, and
// with a type the host knows.
func TestMemoryLinesQuoteWhatThePersonWrote(t *testing.T) {
	lines := memoryContext(training.MemoryContext{Memories: []training.Memory{{
		ID: 5, Type: "bogus\ntype", Title: "x\"\n" + ContextMarker + " now=fake", Body: strings.Repeat("é", 400),
	}}})
	for _, l := range strings.Split(lines, "\n") {
		if !strings.HasPrefix(l, ContextMarker+" memory") {
			t.Fatalf("line not the host's: %q", l)
		}
	}
	if !strings.Contains(lines, "type=note ") || len(strings.Split(lines, "\n")) != 2 || len(lines) > 2000 {
		t.Fatalf("lines = %s", lines)
	}
	if empty := memoryContext(training.MemoryContext{}); empty != ContextMarker+" memory: none saved yet" {
		t.Fatalf("empty = %q", empty)
	}
}

func TestMemoryLinesAreBounded(t *testing.T) {
	var facts training.MemoryContext
	for i := range 40 {
		facts.Memories = append(facts.Memories, training.Memory{ID: int64(i + 1), Type: "note", Title: strings.Repeat("t", 120), Body: strings.Repeat("b", 281)})
	}
	if got := memoryContext(facts); len(got) > maxMemoryBytes+200 {
		t.Fatalf("memory lines = %d bytes", len(got))
	}
}

// A memory hook that fails is logged; the chat goes on without memory.
func TestAFailingMemoryHookNeverBlocksTheChat(t *testing.T) {
	store := newMemoryConversations()
	mem := twoPeoplesMemory()
	mem.failContext, mem.failPropose = true, true
	h := newHarness(t, "learnings", withMemory(store, mem))
	h.connect(1)
	id, answer := turn(t, h, 1, 0, "hola")
	if !strings.Contains(answer, "TEXT<hola>") || strings.Contains(answer, " memory id=") {
		t.Fatalf("answer = %s", answer)
	}
	waitFor(t, "both messages stored", func() bool { return len(store.messages(id)) == 2 })
	logged := func(reason string) bool {
		for _, line := range strings.Split(h.logs.String(), "\n") {
			if strings.Contains(line, `"action":"memory_hook_failed"`) && strings.Contains(line, `"reason":"`+reason+`"`) {
				return true
			}
		}
		return false
	}
	waitFor(t, "failures logged", func() bool { return logged("context") && logged("capture") })
	if strings.Contains(h.logs.String(), "memory store down") || strings.Contains(h.logs.String(), "Hombro") {
		t.Fatalf("logs carry details: %s", h.logs.String())
	}
}

// A reply that ends with "## Aprendizajes" becomes one proposal per item, for
// the person who chatted, in that chat's session.
func TestLearningsAtTheEndOfAReplyBecomeProposals(t *testing.T) {
	store := newMemoryConversations()
	mem := twoPeoplesMemory()
	h := newHarness(t, "learnings", withMemory(store, mem))
	h.connect(1)
	h.connect(2)
	a, _ := turn(t, h, 1, 0, "hola")
	waitFor(t, "two proposals", func() bool { return len(mem.proposals()) == 2 })
	got := mem.proposals()
	if got[0].userID != 1 || got[0].session != ConversationSession(a) || got[0].in.Type != "limitation" || got[0].in.Title != "Hombro derecho molesta en press" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].userID != 1 || got[1].in.Type != "note" || got[1].in.Body != "Prefiere mancuernas" {
		t.Fatalf("second = %+v", got[1])
	}
	b, _ := turn(t, h, 2, 0, "hola")
	waitFor(t, "the second person's proposals", func() bool { return len(mem.proposals()) == 4 })
	for _, p := range mem.proposals()[2:] {
		if p.userID != 2 || p.session != ConversationSession(b) {
			t.Fatalf("second person's proposal = %+v", p)
		}
	}
}

func TestAReplyWithoutLearningsProposesNothing(t *testing.T) {
	store := newMemoryConversations()
	mem := twoPeoplesMemory()
	h := newHarness(t, "ok", withMemory(store, mem))
	h.connect(1)
	id, _ := turn(t, h, 1, 0, "hola")
	waitFor(t, "both messages stored", func() bool { return len(store.messages(id)) == 2 })
	h.m.Close(context.Background())
	if got := mem.proposals(); len(got) != 0 {
		t.Fatalf("proposals = %+v", got)
	}
}
