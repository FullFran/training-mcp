package engine

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeConnectors gives every person the servers listed for them.
type fakeConnectors struct {
	mu  sync.Mutex
	per map[int64][]Server
}

func (f *fakeConnectors) ForEngine(userID int64) []Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Server(nil), f.per[userID]...)
}

func connectorHarness(t *testing.T, mode string, tweak func(*Settings)) (*harness, *fakeConnectors) {
	fc := &fakeConnectors{per: map[int64][]Server{
		1: {{Name: "notion", Label: "Notion"}},
		2: {{Name: "notion", Label: "Notion de B"}},
	}}
	h := newHarness(t, mode, func(s *Settings) {
		s.Connectors = fc
		if tweak != nil {
			tweak(s)
		}
	})
	return h, fc
}

// nextEvent waits for an event of one name, skipping the others.
func nextEvent(t *testing.T, ch <-chan Event, name string) Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed waiting for %s", name)
			}
			if ev.Name == name {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", name)
		}
	}
}

func (h *harness) engineToken(userID int64) string {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	if p := h.m.people[userID]; p != nil && p.proc != nil {
		return p.proc.token
	}
	return ""
}

func TestConnectorServersLoadNextToTrainingTools(t *testing.T) {
	h, _ := connectorHarness(t, "", nil)
	h.connect(1)
	ch, done := h.m.Subscribe(1)
	defer done()
	if err := h.m.Prompt(1, "hola"); err != nil {
		t.Fatal(err)
	}
	if events, _ := collect(t, ch); last(events).Name != "done" {
		t.Fatalf("turn with a connector = %v", events)
	}
	if loaded := h.m.Loaded(1); !loaded["notion"] {
		t.Fatalf("loaded = %v", loaded)
	}
	if loaded := h.m.Loaded(2); len(loaded) != 0 {
		t.Fatalf("another person's loaded servers = %v", loaded)
	}
}

func TestAServerThatFailedToLoadIsReported(t *testing.T) {
	h, fc := connectorHarness(t, "", nil)
	fc.per[1] = []Server{{Name: "notion", Label: "Notion"}, {Name: "broken", Label: "Broken"}}
	h.connect(1)
	ch, done := h.m.Subscribe(1)
	defer done()
	h.m.Prompt(1, "hola")
	collect(t, ch)
	loaded := h.m.Loaded(1)
	if !loaded["notion"] || loaded["broken"] {
		t.Fatalf("loaded = %v", loaded)
	}
}

func TestToolsOfAServerTheHostDidNotGiveStopTheEngine(t *testing.T) {
	h, _ := connectorHarness(t, "roguetools", nil)
	h.connect(1)
	ch, done := h.m.Subscribe(1)
	defer done()
	h.m.Prompt(1, "hola")
	events, _ := collect(t, ch)
	if ev := last(events); ev.Name != "error" || ev.Data["code"] != string(CodeEngineUnavailable) {
		t.Fatalf("rogue tools = %v", events)
	}
	if !strings.Contains(h.logs.String(), `"reason":"unexpected_tools"`) {
		t.Fatalf("logs = %s", h.logs.String())
	}
}

func approvalTurn(t *testing.T, h *harness, userID int64) (<-chan Event, func()) {
	t.Helper()
	h.connect(userID)
	ch, done := h.m.Subscribe(userID)
	if err := h.m.Prompt(userID, "busca en notion"); err != nil {
		t.Fatal(err)
	}
	return ch, done
}

func TestAllowOnceAsksAgainAndGivesOneCallTicket(t *testing.T) {
	h, _ := connectorHarness(t, "approval", nil)
	ch, done := approvalTurn(t, h, 1)
	defer done()
	ev := nextEvent(t, ch, "approval")
	if ev.Data["server"] != "Notion" || ev.Data["tool"] != "search" || ev.Data["args"] != `{"query":"secret-arg"}` {
		t.Fatalf("approval event = %v", ev.Data)
	}
	id := ev.Data["id"].(string)
	tok := h.engineToken(1)
	if h.m.Authorize(1, tok, "mcp__notion__search") {
		t.Fatal("a call was authorized before any answer")
	}
	if err := h.m.Answer(1, id, AnswerAllowOnce); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Answer(1, id, AnswerAllowOnce); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("second answer to the same request = %v", err)
	}
	waitFor(t, "ticket", func() bool { return h.m.Authorize(1, tok, "mcp__notion__search") })
	if h.m.Authorize(1, tok, "mcp__notion__search") {
		t.Fatal("one allow_once gave two calls")
	}
	// The second call asks again.
	ev = nextEvent(t, ch, "approval")
	h.m.Answer(1, ev.Data["id"].(string), AnswerRejectOnce)
	_, text := collect(t, ch)
	if !strings.Contains(text, "approvals=allow,deny") {
		t.Fatalf("answer = %q", text)
	}
	logs := h.logs.String()
	if strings.Contains(logs, "secret-arg") {
		t.Fatal("a decision record holds the call's arguments")
	}
	for _, want := range []string{`"event":"coach.decision"`, `"answer":"allow_once"`, `"answer":"reject_once"`, `"entry":"connector.notion"`, `"permission":"tool.search"`, `"channel":"coach:approval"`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs lack %s: %s", want, logs)
		}
	}
}

func TestAllowAlwaysIsASessionGrantTheHostCanRevoke(t *testing.T) {
	h, _ := connectorHarness(t, "approval", nil)
	ch, done := approvalTurn(t, h, 1)
	defer done()
	ev := nextEvent(t, ch, "approval")
	h.m.Answer(1, ev.Data["id"].(string), AnswerAllowAlways)
	// The second call needs no question: the grant answers it.
	_, text := collect(t, ch)
	if !strings.Contains(text, "approvals=allow,allow") {
		t.Fatalf("answer = %q", text)
	}
	if !strings.Contains(h.logs.String(), `"decider":{"kind":"grant","id":"`) {
		t.Fatalf("no grant decision record: %s", h.logs.String())
	}
	if g := h.m.Grants(1); len(g) != 1 || g[0].Tool != "mcp__notion__search" || g[0].Answer != AnswerAllowAlways {
		t.Fatalf("grants = %+v", g)
	}
	if g := h.m.Grants(2); len(g) != 0 {
		t.Fatalf("another person's grants = %+v", g)
	}
	h.m.Revoke(1, "notion")
	if !strings.Contains(h.logs.String(), `"outcome":"revoked"`) {
		t.Fatal("no revocation record")
	}
	h.m.Prompt(1, "otra vez")
	ev = nextEvent(t, ch, "approval")
	h.m.Answer(1, ev.Data["id"].(string), AnswerRejectAlways)
	_, text = collect(t, ch)
	if !strings.Contains(text, "approvals=deny,deny") {
		t.Fatalf("after revoke and reject_always = %q", text)
	}
}

func TestAlwaysAnswersDoNotOutliveTheSession(t *testing.T) {
	h, _ := connectorHarness(t, "approval", nil)
	ch, done := approvalTurn(t, h, 1)
	defer done()
	ev := nextEvent(t, ch, "approval")
	h.m.Answer(1, ev.Data["id"].(string), AnswerAllowAlways)
	collect(t, ch)
	old := h.engineToken(1)
	h.m.Reload(1) // a connector changed: the session ends
	waitFor(t, "process stopped", func() bool { return h.engineToken(1) == "" })
	if h.m.Authorize(1, old, "mcp__notion__search") {
		t.Fatal("an old session's token still authorizes")
	}
	h.m.Prompt(1, "nueva sesión")
	nextEvent(t, ch, "approval") // asked again
}

func TestApprovalsFailClosed(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		h, _ := connectorHarness(t, "approval", func(s *Settings) { s.ApprovalTimeout = 50 * time.Millisecond })
		ch, done := approvalTurn(t, h, 1)
		defer done()
		_, text := collect(t, ch)
		if !strings.Contains(text, "approvals=deny,deny") || !strings.Contains(h.logs.String(), `"outcome":"timeout"`) {
			t.Fatalf("answer = %q logs %s", text, h.logs.String())
		}
	})
	t.Run("lost connection", func(t *testing.T) {
		h, _ := connectorHarness(t, "approval", nil)
		ch, done := approvalTurn(t, h, 1)
		nextEvent(t, ch, "approval")
		done() // the only stream closes while the question is open
		waitFor(t, "denied as error", func() bool { return strings.Count(h.logs.String(), `"outcome":"error"`) >= 2 })
	})
	t.Run("no stream", func(t *testing.T) {
		h, _ := connectorHarness(t, "approval", nil)
		h.connect(1)
		// A reaction or a prompt with no open stream: nobody can answer.
		h.m.Prompt(1, "sin stream")
		waitFor(t, "denied", func() bool { return strings.Count(h.logs.String(), `"outcome":"error"`) >= 2 })
	})
	t.Run("unknown answer", func(t *testing.T) {
		h, _ := connectorHarness(t, "approval", nil)
		ch, done := approvalTurn(t, h, 1)
		defer done()
		ev := nextEvent(t, ch, "approval")
		if err := h.m.Answer(1, ev.Data["id"].(string), "allow_forever"); !errors.Is(err, ErrBadAnswer) {
			t.Fatalf("bad answer = %v", err)
		}
	})
}

func TestApprovalsBelongToOnePerson(t *testing.T) {
	h, _ := connectorHarness(t, "approval", nil)
	ch, done := approvalTurn(t, h, 1)
	defer done()
	ev := nextEvent(t, ch, "approval")
	id := ev.Data["id"].(string)
	h.connect(2)
	// Person 2 cannot answer person 1's question.
	if err := h.m.Answer(2, id, AnswerAllowAlways); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("other person's answer = %v", err)
	}
	h.m.Answer(1, id, AnswerAllowOnce)
	tok := h.engineToken(1)
	// Person 1's token never authorizes as person 2, nor the other way round.
	waitFor(t, "ticket", func() bool { return h.m.Authorize(1, tok, "mcp__notion__search") })
	h.m.Answer(1, nextEvent(t, ch, "approval").Data["id"].(string), AnswerAllowOnce)
	time.Sleep(50 * time.Millisecond)
	if h.m.Authorize(2, tok, "mcp__notion__search") {
		t.Fatal("person 1's ticket used as person 2")
	}
	h.m.Revoke(2, "notion")
	if !h.m.Authorize(1, tok, "mcp__notion__search") {
		t.Fatal("person 2's revoke touched person 1")
	}
}

func TestPermissionIDsAreDotted(t *testing.T) {
	for tool, want := range map[string]string{
		"mcp__notion__search":         "tool.search",
		"mcp__notion__notion_fetch":   "tool.notion-fetch",
		"mcp__notion__Create__Page_x": "tool.create-page-x-",
	} {
		got := permissionID(tool)
		if !strings.HasPrefix(got, want) {
			t.Errorf("%s = %s, want prefix %s", tool, got, want)
		}
		if !dottedID.MatchString(got) || !dottedID.MatchString(entryID("notion")) {
			t.Errorf("%s is not a dotted id", got)
		}
	}
}
