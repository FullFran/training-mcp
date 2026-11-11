package engine

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake engine is this test binary started again with -test.run selecting
// TestFakeEngine and the behaviour after "--". It speaks the real protocol and
// plays a fake provider: no Node, no Pi, no OpenAI.

func fakeCommand(mode string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFakeEngine$", "--", "fake-engine", mode)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		return cmd
	}
}

func TestFakeEngine(t *testing.T) {
	args := os.Args
	i := 0
	for i < len(args) && args[i] != "fake-engine" {
		i++
	}
	if i >= len(args)-1 {
		t.Skip("helper process only")
	}
	runFakeEngine(args[i+1])
	os.Exit(0)
}

func runFakeEngine(mode string) {
	var mu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	send := func(v map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	tokenReplies := map[string]chan map[string]any{}
	refreshes := 0
	approvalReplies := map[string]chan string{}
	var tmu sync.Mutex
	askApproval := func(id, tool string) string {
		ch := make(chan string, 1)
		tmu.Lock()
		approvalReplies[id] = ch
		tmu.Unlock()
		send(map[string]any{"type": "approval.request", "id": id, "name": tool, "args": `{"query":"secret-arg"}`})
		return <-ch
	}
	askToken := func(id string) map[string]any {
		ch := make(chan map[string]any, 1)
		tmu.Lock()
		tokenReplies[id] = ch
		tmu.Unlock()
		send(map[string]any{"type": "token.get", "id": id})
		return <-ch
	}
	send(map[string]any{"type": "ready", "piVersion": "fake-1.0"})
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 16<<20)
	turn := 0
	for in.Scan() {
		var m map[string]any
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		switch m["type"] {
		case "start":
			tools := []string{MCPToolPrefix + "get_session", MCPToolPrefix + "log_sets"}
			if mode == "badtools" {
				tools = append(tools, "bash")
			}
			if mode == "roguetools" {
				tools = append(tools, "mcp__other__search")
			}
			// A connector server loads unless the host pointed it anywhere but its proxy.
			servers, _ := m["servers"].([]any)
			for _, raw := range servers {
				srv, _ := raw.(map[string]any)
				name := fmt.Sprint(srv["name"])
				if strings.HasPrefix(fmt.Sprint(srv["url"]), "http://127.0.0.1:1/engine/connectors/"+name+"/mcp") && name != "broken" {
					tools = append(tools, "mcp__"+name+"__search")
				}
			}
			// The fake proves it got its token without echoing it.
			if tok, _ := m["mcpToken"].(string); !strings.HasPrefix(tok, TokenPrefix) {
				tools = append(tools, "no_token")
			}
			send(map[string]any{"type": "session", "tools": tools})
		case "login.start":
			send(map[string]any{"type": "login.code", "userCode": "ABCD-1234"})
			if mode != "loginslow" {
				go func() {
					time.Sleep(20 * time.Millisecond)
					expires := time.Now().Add(time.Hour).UnixMilli()
					if mode == "nearexpiry" || mode == "refreshfail" || mode == "refreshslow" {
						expires = time.Now().Add(time.Minute).UnixMilli()
					}
					send(map[string]any{"type": "login.done", "credential": map[string]any{"access": "fake-access-0", "refresh": "fake-refresh-0", "expires": expires, "accountId": "acct"}})
				}()
			}
		case "refresh":
			tmu.Lock()
			refreshes++
			tmu.Unlock()
			id := m["id"]
			cred, _ := m["credential"].(map[string]any)
			go func() {
				delay := 30 * time.Millisecond
				if mode == "refreshslow" {
					delay = 400 * time.Millisecond
				}
				time.Sleep(delay)
				if mode == "refreshfail" {
					send(map[string]any{"type": "refresh.failed", "id": id})
					return
				}
				send(map[string]any{"type": "refresh.done", "id": id, "credential": map[string]any{"access": "fake-access-1", "refresh": fmt.Sprint(cred["refresh"]) + "-r", "expires": time.Now().Add(time.Hour).UnixMilli(), "accountId": "acct"}})
			}()
		case "token":
			tmu.Lock()
			ch := tokenReplies[fmt.Sprint(m["id"])]
			tmu.Unlock()
			if ch != nil {
				ch <- m
			}
		case "models.get":
			go func() {
				if reply := askToken("t-models"); reply["error"] != nil {
					send(map[string]any{"type": "models.failed", "code": reply["error"]})
					return
				}
				if mode == "modelsfail" {
					send(map[string]any{"type": "models.failed", "code": "reconnect"})
					return
				}
				send(map[string]any{"type": "models", "model": "gpt-a", "thinking": "medium", "models": []any{
					map[string]any{"id": "gpt-a", "name": "GPT A", "thinking": []string{"off", "low", "medium", "high"}},
					map[string]any{"id": "gpt-b", "name": "GPT B", "thinking": []string{"off"}},
				}})
			}()
		case "prompt":
			turn++
			n := turn
			text, _ := m["text"].(string)
			// The fake proves which model and thinking level the turn asked for.
			choice := fmt.Sprintf("MODEL<%v:%v>", orEmpty(m["model"]), orEmpty(m["thinking"]))
			ctx, _ := m["context"].(string)
			// The fake proves which photos it got: type, size and a digest, never the bytes.
			photos := ""
			ims, _ := m["images"].([]any)
			for _, raw := range ims {
				im, _ := raw.(map[string]any)
				data, err := base64.StdEncoding.DecodeString(fmt.Sprint(im["data"]))
				if err != nil {
					photos += "<undecodable>"
					continue
				}
				photos += fmt.Sprintf("<%v:%d:%x>", im["mimeType"], len(data), sha256.Sum256(data))
			}
			go func() {
				if mode == "hang" {
					return
				}
				// Two token requests at once, as Pi can make: the host must refresh once.
				var wg sync.WaitGroup
				replies := make([]map[string]any, 2)
				for i := range 2 {
					wg.Add(1)
					go func() { defer wg.Done(); replies[i] = askToken(fmt.Sprintf("t-%d-%d", n, i)) }()
				}
				wg.Wait()
				if e, ok := replies[0]["error"].(string); ok {
					send(map[string]any{"type": "error", "code": e})
					return
				}
				send(map[string]any{"type": "tool", "phase": "start", "call": 1, "name": MCPToolPrefix + "get_session", "ok": true})
				send(map[string]any{"type": "tool", "phase": "end", "call": 1, "name": MCPToolPrefix + "get_session", "ok": true})
				if mode == "pending" {
					send(map[string]any{"type": "tool", "phase": "end", "call": 2, "name": MCPToolPrefix + "log_sets", "ok": true, "pending": true})
					send(map[string]any{"type": "tool", "phase": "end", "call": 3, "name": MCPToolPrefix + "log_sets", "ok": false, "pending": true})
					send(map[string]any{"type": "tool", "phase": "end", "call": 4, "name": MCPToolPrefix + "log_sets", "ok": true, "pending": "yes"})
				}
				if mode == "approval" {
					// Two calls of the same connector tool in one turn, one at a time.
					var got []string
					for i := range 2 {
						got = append(got, askApproval(fmt.Sprintf("a-%d-%d", n, i), "mcp__notion__search"))
					}
					send(map[string]any{"type": "delta", "text": "approvals=" + strings.Join(got, ",") + "\n"})
				}
				if mode == "flood" {
					for range 50 {
						send(map[string]any{"type": "delta", "text": strings.Repeat("x", 1000)})
					}
				}
				tmu.Lock()
				r := refreshes
				tmu.Unlock()
				send(map[string]any{"type": "delta", "text": fmt.Sprintf("refreshes=%d access=%v\n", r, replies[0]["access"] != nil)})
				send(map[string]any{"type": "thinking", "text": "pensando en tu sesión"})
				send(map[string]any{"type": "delta", "text": "CONTEXT<" + ctx + ">TEXT<" + text + ">IMAGES" + photos + choice})
				send(map[string]any{"type": "done", "aborted": false})
			}()
		case "approval":
			tmu.Lock()
			ch := approvalReplies[fmt.Sprint(m["id"])]
			tmu.Unlock()
			if ch != nil {
				ch <- fmt.Sprint(m["decision"])
			}
		case "abort":
			if mode != "deaf" {
				send(map[string]any{"type": "done", "aborted": true})
			}
		case "shutdown":
			return
		}
	}
}

func orEmpty(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// --- helpers for the tests -------------------------------------------------

type harness struct {
	t      *testing.T
	m      *Manager
	vault  *Vault
	tokens *Tokens
	dir    string
	logs   *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newHarness(t *testing.T, mode string, tweak func(*Settings)) *harness {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	v, err := NewVault(dir+"/coach/credentials", key)
	if err != nil {
		t.Fatal(err)
	}
	tokens := NewTokens(nil)
	logs := &syncBuffer{}
	s := Settings{
		Vault: v, Tokens: tokens, Command: fakeCommand(mode), MCPURL: "http://127.0.0.1:1/mcp", SystemPrompt: SystemPrompt,
		ReplyTimeout: 5 * time.Second, Logger: newLogger(logs),
	}
	if tweak != nil {
		tweak(&s)
	}
	m, err := NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, m: m, vault: v, tokens: tokens, dir: dir, logs: logs}
	t.Cleanup(func() {
		ctx, cancel := contextWithTimeout(5 * time.Second)
		defer cancel()
		m.Close(ctx)
	})
	return h
}

// connect runs a device login to the end and waits for the stored credential.
func (h *harness) connect(userID int64) {
	h.t.Helper()
	info, err := h.m.StartLogin(userID)
	if err != nil {
		h.t.Fatalf("login: %v", err)
	}
	if info.UserCode != "ABCD-1234" || info.VerificationURI != DeviceVerificationURI {
		h.t.Fatalf("login info = %+v", info)
	}
	h.waitState(userID, "connected")
}

func (h *harness) waitState(userID int64, state string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.m.Status(userID).State == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("state = %q, want %q", h.m.Status(userID).State, state)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// collect reads events until the turn ends and returns them with the answer text.
func collect(t *testing.T, ch <-chan Event) ([]Event, string) {
	t.Helper()
	var events []Event
	var text strings.Builder
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events, text.String()
			}
			events = append(events, ev)
			if ev.Name == "delta" {
				text.WriteString(ev.Data["text"].(string))
			}
			if ev.Name == "done" || ev.Name == "error" {
				return events, text.String()
			}
		case <-timeout:
			t.Fatalf("turn did not end; got %v", events)
		}
	}
}

func last(events []Event) Event { return events[len(events)-1] }

// prompt starts a turn outside any stored conversation.
func prompt(m *Manager, userID int64, text string, images ...Image) error {
	_, err := m.Prompt(userID, 0, text, images...)
	return err
}
