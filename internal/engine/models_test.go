package engine

import (
	"strings"
	"testing"
)

// The model list comes from the person's own engine: only what their ChatGPT
// login can use, with the thinking levels each model takes.
func TestModelsComeFromThePersonsEngine(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	list, err := h.m.Models(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Models) != 2 || list.Models[0].ID != "gpt-a" || list.Models[0].Name != "GPT A" {
		t.Fatalf("models = %+v", list.Models)
	}
	if got := strings.Join(list.Models[0].Thinking, ","); got != "off,low,medium,high" {
		t.Fatalf("thinking levels = %q", got)
	}
	// Nothing chosen yet: the engine's own default is reported.
	if list.Model != "gpt-a" || list.Thinking != "medium" {
		t.Fatalf("current = %q %q", list.Model, list.Thinking)
	}
}

func TestModelsNeedAConnection(t *testing.T) {
	h := newHarness(t, "ok", nil)
	if _, err := h.m.Models(1); codeOf(err) != CodeNotConnected {
		t.Fatalf("not connected: %v", err)
	}
}

func TestModelsFailureIsAFixedCode(t *testing.T) {
	h := newHarness(t, "modelsfail", nil)
	h.connect(1)
	if _, err := h.m.Models(1); codeOf(err) != CodeReconnect {
		t.Fatalf("failed list: %v", err)
	}
}

// A choice is kept per person and goes with every turn; only a model the
// person's engine listed, and a thinking level that model takes, is accepted.
func TestPreferencesGoWithEveryTurn(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	h.connect(2)
	if err := h.m.SetPreferences(1, "gpt-a", "high"); err != nil {
		t.Fatal(err)
	}
	list, err := h.m.Models(1)
	if err != nil || list.Model != "gpt-a" || list.Thinking != "high" {
		t.Fatalf("after choosing: %+v %v", list, err)
	}
	ch, closeSub := h.m.Subscribe(1)
	defer closeSub()
	if err := prompt(h.m, 1, "hola"); err != nil {
		t.Fatal(err)
	}
	if _, text := collect(t, ch); !strings.Contains(text, "MODEL<gpt-a:high>") {
		t.Fatalf("turn did not carry the choice: %q", text)
	}
	// The other person's turns carry no choice of the first.
	ch2, close2 := h.m.Subscribe(2)
	defer close2()
	if err := prompt(h.m, 2, "hola"); err != nil {
		t.Fatal(err)
	}
	if _, text := collect(t, ch2); !strings.Contains(text, "MODEL<:>") {
		t.Fatalf("second person's turn: %q", text)
	}
}

func TestPreferencesRefuseWhatTheEngineDidNotList(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	cases := map[string][2]string{
		"unknown model":         {"gpt-4o-from-elsewhere", "off"},
		"empty model":           {"", "off"},
		"level the model lacks": {"gpt-b", "high"},
		"unknown level":         {"gpt-a", "turbo"},
	}
	for name, c := range cases {
		if err := h.m.SetPreferences(1, c[0], c[1]); codeOf(err) != CodeBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	list, _ := h.m.Models(1)
	if list.Model != "gpt-a" || list.Thinking != "medium" {
		t.Fatalf("a refused choice changed the preference: %+v", list)
	}
}

// Reasoning text streams as its own event, never mixed into the answer.
func TestThinkingStreamsApart(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	ch, closeSub := h.m.Subscribe(1)
	defer closeSub()
	if err := prompt(h.m, 1, "hola"); err != nil {
		t.Fatal(err)
	}
	events, text := collect(t, ch)
	var thought string
	for _, ev := range events {
		if ev.Name == "thinking" {
			thought += ev.Data["text"].(string)
		}
	}
	if thought != "pensando en tu sesión" || strings.Contains(text, "pensando") {
		t.Fatalf("thinking = %q, answer = %q", thought, text)
	}
}

func TestModelsMessageIsValidated(t *testing.T) {
	good := `{"type":"models","models":[{"id":"gpt-5.1-codex","name":"GPT-5.1 Codex","thinking":["off","low"]}],"model":"gpt-5.1-codex","thinking":"low"}`
	m, ok := parseChildMessage([]byte(good))
	if !ok || len(m.Models) != 1 || m.Models[0].ID != "gpt-5.1-codex" || m.Model != "gpt-5.1-codex" || m.Thinking != "low" {
		t.Fatalf("good list: %+v %v", m, ok)
	}
	bad := []string{
		`{"type":"models","models":[{"id":"a b","name":"x","thinking":["off"]}]}`,
		`{"type":"models","models":[{"id":"a","name":"x","thinking":["turbo"]}]}`,
		`{"type":"models","models":[{"id":"a","name":"x\u0007","thinking":["off"]}]}`,
		`{"type":"models","models":[{"id":"a","name":"x","thinking":["off"]}],"model":"b"}`,
		`{"type":"models","models":[{"id":"a","name":"x","thinking":["off"]}],"thinking":"high"}`,
		`{"type":"models","models":"many"}`,
		`{"type":"thinking"}`,
	}
	for _, line := range bad {
		if _, ok := parseChildMessage([]byte(line)); ok {
			t.Errorf("accepted %s", line)
		}
	}
	if m, ok := parseChildMessage([]byte(`{"type":"models.failed","code":"says: secret"}`)); !ok || m.Code != CodeProviderError {
		t.Fatalf("models.failed: %+v %v", m, ok)
	}
}

// Disconnecting forgets the account's model list: nothing is served or chosen
// from it until the person connects again and their new engine reports one.
func TestDisconnectForgetsTheModelList(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	_, closeSub := h.m.Subscribe(1) // an open chat keeps the person tracked
	defer closeSub()
	if _, err := h.m.Models(1); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Disconnect(1); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Models(1); codeOf(err) != CodeNotConnected {
		t.Fatalf("models after disconnect: %v", err)
	}
	if err := h.m.SetPreferences(1, "gpt-a", "high"); codeOf(err) != CodeNotConnected {
		t.Fatalf("choice after disconnect: %v", err)
	}
	h.connect(1)
	if list, err := h.m.Models(1); err != nil || list.Model != "gpt-a" || list.Thinking != "medium" {
		t.Fatalf("after reconnecting: %+v %v", list, err)
	}
}
