package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// #57: an engine that crashes mid-turn. Everything it wrote before it died is
// read and delivered, then the crash is reported once, and nothing after.
func TestEngineCrashMidTurnDeliversItsOutputThenOneError(t *testing.T) {
	for run := range 5 {
		h := newHarness(t, "crash", func(s *Settings) { s.TurnMaxBytes = 1 << 20 })
		h.connect(1)
		ch, closeSub := h.m.Subscribe(1)
		if err := prompt(h.m, 1, "hi"); err != nil {
			t.Fatal(err)
		}
		events, text := collect(t, ch)
		if ev := last(events); ev.Name != "error" || ev.Data["code"] != string(CodeEngineUnavailable) {
			t.Fatalf("run %d: last event = %+v", run, ev)
		}
		var want strings.Builder
		for i := range crashDeltas {
			fmt.Fprintf(&want, "%04d%s", i, strings.Repeat("x", 996))
		}
		if text != want.String() {
			t.Fatalf("run %d: delivered %d of %d bytes written before the crash", run, len(text), want.Len())
		}
		// Nothing more arrives for that turn: no second error, no late output.
		select {
		case ev, ok := <-ch:
			if ok {
				t.Fatalf("run %d: event after the crash was reported: %+v", run, ev)
			}
		case <-time.After(200 * time.Millisecond):
		}
		if n := strings.Count(h.logs.String(), `"reason":"exit"`); n != 1 {
			t.Fatalf("run %d: exit logged %d times", run, n)
		}
		waitFor(t, "the slot to be freed", func() bool { return h.m.LiveCount() == 0 })
		closeSub()
	}
}

// #58: a turn while the person's device login is still pending is refused with
// its own code, and the pending login is left alone.
func TestTurnIsRefusedWhileALoginIsPending(t *testing.T) {
	h := newHarness(t, "loginslow", nil)
	// Two connected people; the first starts connecting again (another account,
	// say), and this fake never finishes a login.
	for _, id := range []int64{1, 2} {
		if err := h.vault.Write(id, &Credential{Access: "a", Refresh: "r", Expires: time.Now().Add(time.Hour).UnixMilli(), AccountID: "acct"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.m.StartLogin(1); err != nil {
		t.Fatal(err)
	}
	if err := prompt(h.m, 1, "hi"); codeOf(err) != CodeLoginPending {
		t.Fatalf("prompt during a pending login: %v", err)
	}
	// A logged set is not reacted to either; it waits for the next turn.
	events, closeEvents := h.m.Subscribe(1)
	defer closeEvents()
	h.m.Notify(1, WorkoutEvent{Kind: "set_logged", At: time.Now(), Exercise: "Sentadilla", WeightKG: 100, Reps: 5})
	if n := h.m.PendingEvents(1); n != 1 {
		t.Fatalf("pending events during a pending login = %d", n)
	}
	select {
	case ev := <-events:
		t.Fatalf("a reaction started during a pending login: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
	if st := h.m.Status(1); st.State != "pending" || st.UserCode != "ABCD-1234" {
		t.Fatalf("status after the refused turn = %+v", st)
	}
	// Another person is not affected.
	ch, closeSub := h.m.Subscribe(2)
	defer closeSub()
	if err := prompt(h.m, 2, "hi"); err != nil {
		t.Fatalf("other person's turn: %v", err)
	}
	if ev := last(mustCollect(t, ch)); ev.Name != "done" {
		t.Fatalf("other person's turn ended with %+v", ev)
	}
}

func mustCollect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	events, _ := collect(t, ch)
	return events
}
