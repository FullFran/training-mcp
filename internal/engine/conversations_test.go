package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// memoryConversations is a ConversationStore for the tests.
type memoryConversations struct {
	mu      sync.Mutex
	next    int64
	owner   map[int64]int64
	conv    map[int64]*Conversation
	msgs    map[int64][]ConversationMessage
	purgeAt []time.Time
}

func newMemoryConversations() *memoryConversations {
	return &memoryConversations{owner: map[int64]int64{}, conv: map[int64]*Conversation{}, msgs: map[int64][]ConversationMessage{}}
}

func (s *memoryConversations) CreateCoachConversation(_ context.Context, userID int64, title string, at time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.owner[s.next] = userID
	s.conv[s.next] = &Conversation{ID: s.next, Title: title, UpdatedAt: at}
	return s.next, nil
}

func (s *memoryConversations) mine(userID, id int64) bool {
	return s.conv[id] != nil && s.owner[id] == userID
}

func (s *memoryConversations) AppendCoachMessage(_ context.Context, userID, id int64, m ConversationMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.mine(userID, id) {
		return ErrNoConversation
	}
	s.msgs[id] = append(s.msgs[id], m)
	if m.At.After(s.conv[id].UpdatedAt) {
		s.conv[id].UpdatedAt = m.At
	}
	return nil
}

func (s *memoryConversations) CoachConversations(_ context.Context, userID int64, since time.Time) ([]Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Conversation
	for id, c := range s.conv {
		if s.owner[id] == userID && !c.UpdatedAt.Before(since) {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (s *memoryConversations) CoachConversation(_ context.Context, userID, id int64, since time.Time) (Conversation, []ConversationMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.mine(userID, id) || s.conv[id].UpdatedAt.Before(since) {
		return Conversation{}, nil, ErrNoConversation
	}
	return *s.conv[id], append([]ConversationMessage(nil), s.msgs[id]...), nil
}

func (s *memoryConversations) RenameCoachConversation(_ context.Context, userID, id int64, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.mine(userID, id) {
		return ErrNoConversation
	}
	s.conv[id].Title = title
	return nil
}

func (s *memoryConversations) DeleteCoachConversation(_ context.Context, userID, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.mine(userID, id) {
		return ErrNoConversation
	}
	delete(s.conv, id)
	delete(s.msgs, id)
	return nil
}

func (s *memoryConversations) DeleteCoachConversations(_ context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.conv {
		if s.owner[id] == userID {
			delete(s.conv, id)
			delete(s.msgs, id)
		}
	}
	return nil
}

func (s *memoryConversations) PurgeCoachConversations(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeAt = append(s.purgeAt, before)
	return 0, nil
}

func (s *memoryConversations) messages(id int64) []ConversationMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ConversationMessage(nil), s.msgs[id]...)
}

func withConversations(store *memoryConversations) func(*Settings) {
	return func(s *Settings) { s.Conversations = store }
}

// turn sends one message and returns the conversation and the answer text.
func turn(t *testing.T, h *harness, userID, conv int64, text string) (int64, string) {
	t.Helper()
	ch, closeSub := h.m.Subscribe(userID)
	defer closeSub()
	id, err := h.m.Prompt(userID, conv, text)
	if err != nil {
		t.Fatal(err)
	}
	events, answer := collect(t, ch)
	if events[0].Name != "turn" || events[0].Data["conversation"] != id {
		t.Fatalf("turn event = %+v, want conversation %d", events[0], id)
	}
	return id, answer
}

func TestTurnsAreStoredInTheirConversation(t *testing.T) {
	store := newMemoryConversations()
	h := newHarness(t, "ok", withConversations(store))
	h.connect(1)
	id, _ := turn(t, h, 1, 0, "hola coach")
	waitFor(t, "both messages stored", func() bool { return len(store.messages(id)) == 2 })
	msgs := store.messages(id)
	if msgs[0].Role != "user" || msgs[0].Text != "hola coach" || msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Text, "TEXT<hola coach>") {
		t.Fatalf("messages = %+v", msgs)
	}
	if strings.Join(msgs[1].Steps, ",") != "get_session:ok" || strings.Contains(msgs[1].Text, "pensando") {
		t.Fatalf("assistant message = %+v", msgs[1])
	}
	if c, _, _ := store.CoachConversation(context.Background(), 1, id, time.Time{}); c.Title != "hola coach" {
		t.Fatalf("title = %q", c.Title)
	}
}

// A conversation opened again gives a fresh engine its earlier messages, as
// host lines with the text quoted, once.
func TestReopeningAConversationGivesTheEngineItsHistory(t *testing.T) {
	store := newMemoryConversations()
	h := newHarness(t, "ok", withConversations(store))
	h.connect(1)
	a, _ := turn(t, h, 1, 0, "me duele el hombro")
	waitFor(t, "first conversation stored", func() bool { return len(store.messages(a)) == 2 })
	b, answer := turn(t, h, 1, 0, "otra cosa")
	if b == a || strings.Contains(answer, "history") {
		t.Fatalf("a new conversation carried history: %d %q", b, answer)
	}
	_, answer = turn(t, h, 1, a, "¿y ahora?")
	if !strings.Contains(answer, ContextMarker+` history person="me duele el hombro"`) || !strings.Contains(answer, ContextMarker+" history coach=") {
		t.Fatalf("reopened conversation without its history: %q", answer)
	}
	_, answer = turn(t, h, 1, a, "gracias")
	if strings.Contains(answer, "history") {
		t.Fatalf("history repeated on the same engine: %q", answer)
	}
}

func TestAnotherPersonsConversationIsRefused(t *testing.T) {
	store := newMemoryConversations()
	h := newHarness(t, "ok", withConversations(store))
	h.connect(1)
	h.connect(2)
	a, _ := turn(t, h, 1, 0, "secreto")
	if _, err := h.m.Prompt(2, a, "léelo"); codeOf(err) != CodeNotFound {
		t.Fatalf("prompt into another's conversation: %v", err)
	}
	if _, _, err := h.m.Conversation(2, a); codeOf(err) != CodeNotFound {
		t.Fatalf("read another's conversation: %v", err)
	}
	for name, err := range map[string]error{
		"open":   h.m.OpenConversation(2, a),
		"rename": h.m.RenameConversation(2, a, "mía"),
		"delete": h.m.DeleteConversation(2, a),
	} {
		if codeOf(err) != CodeNotFound {
			t.Errorf("%s another's conversation: %v", name, err)
		}
	}
	if list, _ := h.m.Conversations(2); len(list) != 0 {
		t.Fatalf("person 2 lists %v", list)
	}
}

// A reaction to a logged set goes to the conversation the person has open.
func TestReactionsGoToTheOpenConversation(t *testing.T) {
	store := newMemoryConversations()
	h := newHarness(t, "ok", withConversations(store))
	h.connect(1)
	a, _ := turn(t, h, 1, 0, "empiezo")
	b, _ := turn(t, h, 1, 0, "otra")
	if err := h.m.OpenConversation(1, a); err != nil {
		t.Fatal(err)
	}
	ch, closeSub := h.m.Subscribe(1)
	defer closeSub()
	h.m.Notify(1, SetLogged(time.Now(), "banca", 80, 8, 8, ""))
	events, _ := collect(t, ch)
	if events[0].Data["conversation"] != a || events[0].Data["origin"] != "reaction" {
		t.Fatalf("reaction turn = %+v", events[0])
	}
	waitFor(t, "reaction stored in the open conversation", func() bool {
		msgs := store.messages(a)
		return len(msgs) == 3 && msgs[2].Origin == "reaction"
	})
	if n := len(store.messages(b)); n != 2 {
		t.Fatalf("other conversation has %d messages", n)
	}
}

func TestRenameAndDelete(t *testing.T) {
	store := newMemoryConversations()
	h := newHarness(t, "ok", withConversations(store))
	h.connect(1)
	a, _ := turn(t, h, 1, 0, "uno")
	for _, bad := range []string{"", "   ", strings.Repeat("x", 81), "a\x07b"} {
		if err := h.m.RenameConversation(1, a, bad); codeOf(err) != CodeBadRequest {
			t.Errorf("rename to %q: %v", bad, err)
		}
	}
	if err := h.m.RenameConversation(1, a, "  Pierna  "); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := h.m.Conversation(1, a); c.Title != "Pierna" {
		t.Fatalf("title = %q", c.Title)
	}
	// Deleting the open conversation also stops the engine that holds it.
	if err := h.m.DeleteConversation(1, a); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "engine stopped", func() bool { return h.m.LiveCount() == 0 })
	turn(t, h, 1, 0, "dos")
	turn(t, h, 1, 0, "tres")
	if err := h.m.DeleteConversations(1); err != nil {
		t.Fatal(err)
	}
	if list, _ := h.m.Conversations(1); len(list) != 0 {
		t.Fatalf("after «borrar todo»: %v", list)
	}
}

// Conversations are purged 90 days after their last message: when the coach
// starts and every hour after, by the injected clock.
func TestOldConversationsArePurged(t *testing.T) {
	store := newMemoryConversations()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	h := newHarness(t, "ok", func(s *Settings) {
		s.Conversations = store
		s.Now = func() time.Time { return now }
		s.PurgeEvery = 20 * time.Millisecond
	})
	waitFor(t, "two purges", func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.purgeAt) >= 2
	})
	store.mu.Lock()
	cutoff := store.purgeAt[0]
	store.mu.Unlock()
	if want := now.Add(-90 * 24 * time.Hour); !cutoff.Equal(want) {
		t.Fatalf("purge before %v, want %v", cutoff, want)
	}
	// Listing hides past the cutoff too, before any purge.
	store.CreateCoachConversation(context.Background(), 1, "vieja", now.Add(-91*24*time.Hour))
	store.CreateCoachConversation(context.Background(), 1, "reciente", now.Add(-89*24*time.Hour))
	h.connect(1)
	list, err := h.m.Conversations(1)
	if err != nil || len(list) != 1 || list[0].Title != "reciente" {
		t.Fatalf("list = %+v %v", list, err)
	}
}

// Closing the coach stores what was already queued before it returns.
func TestCloseStoresQueuedMessages(t *testing.T) {
	store := newMemoryConversations()
	h := newHarness(t, "ok", withConversations(store))
	h.connect(1)
	id, _ := turn(t, h, 1, 0, "última")
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()
	h.m.Close(ctx)
	if n := len(store.messages(id)); n != 2 {
		t.Fatalf("stored %d messages before Close returned", n)
	}
}
