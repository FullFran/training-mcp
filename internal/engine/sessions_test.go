package engine

import (
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// startTurn sends one message without waiting for its end.
func startTurn(t *testing.T, h *harness, userID, conv int64, text string) (int64, <-chan Event, func()) {
	t.Helper()
	ch, closeSub := h.m.Subscribe(userID)
	id, err := h.m.Prompt(userID, conv, text)
	if err != nil {
		closeSub()
		t.Fatal(err)
	}
	return id, ch, closeSub
}

func TestAStoredConversationIsTheHCSSession(t *testing.T) {
	h, _ := connectorHarness(t, "approval", withConversations(newMemoryConversations()))
	h.connect(1)
	a, ch, done := startTurn(t, h, 1, 0, "busca en notion")
	ev := nextEvent(t, ch, "approval")
	want := training.CoachSessionPrefix + "conversation-" + strconv.FormatInt(a, 10)
	if got, ok := h.m.SessionOf(h.engineToken(1)); !ok || got != want {
		t.Fatalf("SessionOf = %q %v, want %q", got, ok, want)
	}
	if ConversationSession(a) != want {
		t.Fatalf("ConversationSession(%d) = %q", a, ConversationSession(a))
	}
	h.m.Answer(1, ev.Data["id"].(string), AnswerAllowAlways)
	collect(t, ch)
	done()
	if !strings.Contains(h.logs.String(), `"session":"`+want+`"`) {
		t.Fatalf("decision records lack the conversation session %s: %s", want, h.logs.String())
	}

	// Another conversation is another session: the grant does not reach it.
	b, ch, done := startTurn(t, h, 1, 0, "otra charla")
	if b == a {
		t.Fatal("a second conversation reused the first one")
	}
	ev = nextEvent(t, ch, "approval")
	if got, _ := h.m.SessionOf(h.engineToken(1)); got != ConversationSession(b) {
		t.Fatalf("second conversation session = %q", got)
	}
	if g := h.m.Grants(1); len(g) != 0 {
		t.Fatalf("grants of another conversation leaked: %+v", g)
	}
	h.m.Answer(1, ev.Data["id"].(string), AnswerRejectAlways)
	if _, text := collect(t, ch); !strings.Contains(text, "approvals=deny,deny") {
		t.Fatalf("second conversation = %q", text)
	}
	done()

	// Back in the first conversation, on a fresh engine process, its grant
	// still answers: the session is the chat, not the process.
	_, ch, done = startTurn(t, h, 1, a, "sigue")
	defer done()
	events, text := collect(t, ch)
	for _, e := range events {
		if e.Name == "approval" {
			t.Fatalf("asked again in the conversation that granted: %v", events)
		}
	}
	if !strings.Contains(text, "approvals=allow,allow") {
		t.Fatalf("first conversation again = %q", text)
	}
	if g := h.m.Grants(1); len(g) != 1 || g[0].Answer != AnswerAllowAlways {
		t.Fatalf("grants back in the first conversation = %+v", g)
	}
}

func TestDeletingAConversationForgetsItsAnswers(t *testing.T) {
	h, _ := connectorHarness(t, "approval", withConversations(newMemoryConversations()))
	h.connect(1)
	a, ch, done := startTurn(t, h, 1, 0, "busca en notion")
	ev := nextEvent(t, ch, "approval")
	h.m.Answer(1, ev.Data["id"].(string), AnswerAllowAlways)
	collect(t, ch)
	done()
	if err := h.m.DeleteConversation(1, a); err != nil {
		t.Fatal(err)
	}
	h.m.mu.Lock()
	left := len(h.m.convAnswers)
	h.m.mu.Unlock()
	if left != 0 {
		t.Fatalf("answers of a deleted conversation kept: %d", left)
	}
}

func TestSessionOfFallsBackToTheProcessWithoutAStoredConversation(t *testing.T) {
	h, _ := connectorHarness(t, "", nil)
	h.connect(1)
	ch, done := h.m.Subscribe(1)
	defer done()
	prompt(h.m, 1, "hola")
	collect(t, ch)
	tok := h.engineToken(1)
	if got, ok := h.m.SessionOf(tok); !ok || got != coachSession(tok) {
		t.Fatalf("SessionOf without a conversation = %q %v", got, ok)
	}
	if _, ok := h.m.SessionOf("tmcp_engine_unknown"); ok {
		t.Fatal("an unknown token has a session")
	}
	if _, ok := h.m.SessionOf(""); ok {
		t.Fatal("an empty token has a session")
	}
}
