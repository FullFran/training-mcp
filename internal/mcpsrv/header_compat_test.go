package mcpsrv

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// readResource2026 posts a resources/read in the 2026-07-28 revision, which carries
// its protocol version and client capabilities in _meta and requires matching headers.
func readResource2026(t *testing.T, mcpName string) (int, string) {
	t.Helper()
	h := New(only(training.NewService(testStore{}, liveClock))).Handler()
	body := `{"jsonrpc":"2.0","id":123456,"method":"resources/read","params":{"uri":"` + restTimerURI + `",` +
		`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2026-07-28")
	r.Header.Set("Mcp-Method", "resources/read")
	if mcpName != "" {
		r.Header.Set("Mcp-Name", mcpName)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	out, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(out)
}

// ChatGPT reads the app view without an Mcp-Name header; the read must still
// return the view instead of a 400 header error.
func TestResourcesReadWithoutMcpNameServesTheView(t *testing.T) {
	code, body := readResource2026(t, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", code, body)
	}
	if !strings.Contains(body, restTimerURI) || !strings.Contains(body, appMIMEType) {
		t.Fatalf("body does not carry the view: %s", body)
	}
}

func TestResourcesReadWithMatchingMcpNameServesTheView(t *testing.T) {
	code, body := readResource2026(t, restTimerURI)
	if code != http.StatusOK || !strings.Contains(body, restTimerURI) {
		t.Fatalf("status = %d, body: %s", code, body)
	}
}

// A header that contradicts the body is still rejected: only a missing header is filled.
func TestResourcesReadWithMismatchedMcpNameIsRejected(t *testing.T) {
	code, body := readResource2026(t, "ui://vibetraining/other")
	if code != http.StatusBadRequest || !strings.Contains(body, "does not match") {
		t.Fatalf("status = %d, want 400 mismatch; body: %s", code, body)
	}
}
