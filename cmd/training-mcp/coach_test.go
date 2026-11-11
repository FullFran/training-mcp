package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFakeNodeEngine is the engine process of TestCoachEndToEnd: the server runs
// "node <entry>" with this test binary as node and a -test.run selecting this
// test as the entry. It only acts with the engine's environment, PATH alone,
// which is also how it proves the server passed nothing else. In a normal test
// run the environment is richer and it skips.
func TestFakeNodeEngine(t *testing.T) {
	if env := os.Environ(); len(env) != 1 || !strings.HasPrefix(env[0], "PATH=") {
		t.Skip("engine helper process only")
	}
	fakeNodeEngine()
	os.Exit(0)
}

func fakeNodeEngine() {
	out := json.NewEncoder(os.Stdout)
	send := func(v map[string]any) { out.Encode(v) }
	send(map[string]any{"type": "ready", "piVersion": "fake"})
	var mcpURL, mcpToken string
	call := func(method string, params map[string]any) string {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req, _ := http.NewRequest(http.MethodPost, mcpURL, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+mcpToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return "error"
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return fmt.Sprintf("%d %s", res.StatusCode, b)
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for in.Scan() {
		var m map[string]any
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		switch m["type"] {
		case "login.start":
			send(map[string]any{"type": "login.code", "userCode": "WXYZ-9876"})
			send(map[string]any{"type": "login.done", "credential": map[string]any{
				"access": "e2e-access", "refresh": "e2e-refresh", "expires": time.Now().Add(time.Hour).UnixMilli(), "accountId": "acct"}})
		case "start":
			mcpURL, _ = m["mcpUrl"].(string)
			mcpToken, _ = m["mcpToken"].(string)
			listed := call("tools/list", map[string]any{})
			tools := []string{}
			for _, name := range []string{"list_plans", "get_session", "log_sets"} {
				if strings.Contains(listed, `"`+name+`"`) {
					tools = append(tools, "mcp__training__"+name)
				}
			}
			send(map[string]any{"type": "session", "tools": tools})
		case "prompt":
			send(map[string]any{"type": "token.get", "id": "t1"})
		case "token":
			send(map[string]any{"type": "tool", "phase": "start", "call": 1, "name": "mcp__training__list_plans"})
			result := call("tools/call", map[string]any{"name": "list_plans", "arguments": map[string]any{}})
			send(map[string]any{"type": "tool", "phase": "end", "call": 1, "name": "mcp__training__list_plans", "ok": strings.HasPrefix(result, "200 ")})
			send(map[string]any{"type": "delta", "text": result})
			send(map[string]any{"type": "done", "aborted": false})
		case "shutdown":
			return
		}
	}
}

func TestCoachEndToEnd(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	dataDir := t.TempDir()
	key := make([]byte, 32)
	t.Setenv("MCP_BEARER_TOKEN", "test-token")
	t.Setenv("MCP_AUTH_DISABLED", "")
	t.Setenv("MCP_OAUTH_ISSUER", "")
	t.Setenv("MCP_OAUTH_RESOURCE", "")
	t.Setenv("MCP_OAUTH_SUBJECT", "")
	t.Setenv("MCP_OAUTH_PROVISION", "")
	t.Setenv("WEB_OAUTH_CLIENT_ID", "")
	t.Setenv("WEB_OAUTH_CLIENT_SECRET", "")
	t.Setenv("WEB_BASE_PATH", "app")
	t.Setenv("TRAINING_DB_PATH", filepath.Join(dataDir, "training.db"))
	t.Setenv("TRAINING_COACH", "1")
	t.Setenv("TRAINING_COACH_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("TRAINING_COACH_NODE", os.Args[0])
	t.Setenv("TRAINING_COACH_ENGINE", "-test.run=^TestFakeNodeEngine$")
	t.Setenv("TRAINING_COACH_MAX_PROCESSES", "")
	t.Setenv("TRAINING_COACH_IDLE_SECONDS", "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithContext(ctx, []string{"--addr", addr}) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("server did not stop")
		}
	}()
	base := "http://" + addr
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("X-Coach", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for i := 0; i < 100; i++ {
		if code, _ := do("GET", "/health", ""); code == http.StatusOK {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	plan := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_plan","arguments":{"name":"Plan e2e","items":[{"exercise":"banca","target_sets":1}]}}}`
	if code, body := do("POST", "/mcp", plan); code != http.StatusOK {
		t.Fatalf("create_plan: %d %s", code, body)
	}
	if code, body := do("GET", "/app/coach/status", ""); code != http.StatusOK || !strings.Contains(body, `"disconnected"`) {
		t.Fatalf("status before login: %d %s", code, body)
	}
	if code, body := do("POST", "/app/coach/login", "{}"); code != http.StatusOK || !strings.Contains(body, "WXYZ-9876") {
		t.Fatalf("login: %d %s", code, body)
	}
	connected := false
	for i := 0; i < 100 && !connected; i++ {
		_, body := do("GET", "/app/coach/status", "")
		connected = strings.Contains(body, `"connected"`)
		time.Sleep(20 * time.Millisecond)
	}
	if !connected {
		t.Fatal("never connected")
	}
	files, _ := os.ReadDir(filepath.Join(dataDir, "coach", "credentials"))
	if len(files) != 1 {
		t.Fatalf("credential files = %d", len(files))
	}
	info, _ := files[0].Info()
	raw, _ := os.ReadFile(filepath.Join(dataDir, "coach", "credentials", files[0].Name()))
	if info.Mode().Perm() != 0o600 || strings.Contains(string(raw), "e2e-") {
		t.Fatalf("credential file mode %v, clear text %v", info.Mode().Perm(), strings.Contains(string(raw), "e2e-"))
	}

	req, _ := http.NewRequest("GET", base+"/app/coach/stream", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	res, err := http.DefaultClient.Do(req.WithContext(streamCtx))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	if line := <-lines; line != ": open" {
		t.Fatalf("stream opened with %q", line)
	}
	if code, body := do("POST", "/app/coach/prompt", `{"text":"¿qué rutinas tengo?"}`); code != http.StatusAccepted {
		t.Fatalf("prompt: %d %s", code, body)
	}
	var got strings.Builder
	timeout := time.After(15 * time.Second)
	for !strings.Contains(got.String(), "event: done") {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed: %s", got.String())
			}
			got.WriteString(line + "\n")
		case <-timeout:
			t.Fatalf("no answer: %s", got.String())
		}
	}
	s := got.String()
	if !strings.Contains(s, `"name":"list_plans","ok":true`) || !strings.Contains(s, "Plan e2e") {
		t.Fatalf("the engine did not reach /mcp as the owner:\n%s", s)
	}
	if strings.Contains(s, "e2e-access") || strings.Contains(s, "tme_") {
		t.Fatalf("a secret reached the browser:\n%s", s)
	}
	// Connectors: the production guard refuses internal addresses, and the
	// engines' proxy door admits no other bearer.
	for _, target := range []string{"https://127.0.0.1/mcp", "https://169.254.169.254/latest", "https://localhost:8080/mcp", "http://example.com/mcp"} {
		if code, body := do("POST", "/app/coach/connectors", `{"url":"`+target+`","label":"x"}`); code != http.StatusBadRequest {
			t.Fatalf("connector %s: %d %s", target, code, body)
		}
	}
	if code, body := do("GET", "/app/coach/connectors/list", ""); code != http.StatusOK || !strings.Contains(body, `"connectors":[]`) {
		t.Fatalf("connector list: %d %s", code, body)
	}
	if code, _ := do("POST", "/engine/connectors/notion/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`); code != http.StatusUnauthorized {
		t.Fatalf("proxy with a non-engine bearer: %d", code)
	}
	if code, _ := do("POST", "/app/coach/disconnect", "{}"); code != http.StatusOK {
		t.Fatalf("disconnect: %d", code)
	}
	if files, _ := os.ReadDir(filepath.Join(dataDir, "coach", "credentials")); len(files) != 0 {
		t.Fatalf("credential left after disconnect: %d files", len(files))
	}
}

func TestCoachOffLeavesTheAppUnchanged(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	t.Setenv("MCP_BEARER_TOKEN", "test-token")
	t.Setenv("WEB_BASE_PATH", "app")
	t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
	t.Setenv("TRAINING_COACH", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithContext(ctx, []string{"--addr", addr}) }()
	defer func() { cancel(); <-done }()
	var res *http.Response
	for i := 0; i < 100; i++ {
		req, _ := http.NewRequest("GET", "http://"+addr+"/app/coach/status", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		if res, err = http.DefaultClient.Do(req); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("coach status with the coach off: %d", res.StatusCode)
	}
}
