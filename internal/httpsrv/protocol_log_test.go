package httpsrv

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveLogged(t *testing.T, path string, headers map[string]string) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	h := LogProtocol(log.New(&buf, "", 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":{"secret":"s3cr3t"}}}`))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return buf.String(), w.Code
}

func TestLogProtocolRecordsRevisionMethodAndClient(t *testing.T) {
	out, code := serveLogged(t, "/mcp", map[string]string{
		"MCP-Protocol-Version": "2026-07-28",
		"Mcp-Method":           "tools/call",
		"User-Agent":           "openai-mcp/1.0",
	})
	if code != http.StatusTeapot {
		t.Fatalf("handler not called, status=%d", code)
	}
	want := "mcp request: version=2026-07-28 method=tools/call session=no client=openai-mcp/1.0\n"
	if out != want {
		t.Fatalf("log = %q, want %q", out, want)
	}
}

func TestLogProtocolMarksOlderRevisionsWithoutHeaders(t *testing.T) {
	out, _ := serveLogged(t, "/mcp", map[string]string{"Mcp-Session-Id": "abc"})
	want := "mcp request: version=none method=none session=yes client=none\n"
	if out != want {
		t.Fatalf("log = %q, want %q", out, want)
	}
}

func TestLogProtocolNeverLogsBodiesOrCredentials(t *testing.T) {
	out, _ := serveLogged(t, "/mcp", map[string]string{"Authorization": "Bearer tok-123"})
	for _, leak := range []string{"s3cr3t", "tok-123", "arguments"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaks %q: %q", leak, out)
		}
	}
}

func TestLogProtocolSanitizesAndClipsClientControlledHeaders(t *testing.T) {
	out, _ := serveLogged(t, "/mcp", map[string]string{
		"MCP-Protocol-Version": "2026-07-28 version=forged",
		"User-Agent":           strings.Repeat("a", 200) + "\x1b[31m",
	})
	want := "mcp request: version=2026-07-28_version_forged method=none session=no client=" + strings.Repeat("a", 64) + "\n"
	if out != want {
		t.Fatalf("log = %q, want %q", out, want)
	}
}

func TestLogProtocolIgnoresOtherPaths(t *testing.T) {
	out, code := serveLogged(t, "/health", nil)
	if out != "" || code != http.StatusTeapot {
		t.Fatalf("log=%q status=%d", out, code)
	}
}
