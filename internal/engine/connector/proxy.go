package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
)

// ProxyPrefix is where the engine reaches a person's connectors on loopback:
// ProxyPrefix + <name> + "/mcp".
const ProxyPrefix = "/engine/connectors/"

const (
	maxProxyRequest  = 4 << 20
	maxProxyResponse = 32 << 20
	proxyCallTimeout = 10 * time.Minute
)

// Proxy forwards an engine's MCP traffic for one connector to the remote
// server. It admits only a live engine token on a direct loopback connection,
// serves only that person's connected servers, replaces the engine token with
// the person's credential for the server, and lets a tools/call through only
// when the host allowed that one call. The engine token never leaves.
type Proxy struct {
	Service *Service
	Tokens  interface {
		Resolve(token string) (int64, bool)
	}
	// Authorize spends one allowed call (engine.Manager.Authorize).
	Authorize func(userID int64, token, tool string) bool
	// Loopback defaults to engine.DirectLoopback.
	Loopback func(*http.Request) bool
}

// requestHeaders are the only headers that go to the remote server.
var requestHeaders = []string{"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"}

// responseHeaders are the only headers that come back.
var responseHeaders = []string{"Content-Type", "Mcp-Session-Id", "Cache-Control"}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	loopback := p.Loopback
	if loopback == nil {
		loopback = engine.DirectLoopback
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	raw = strings.TrimSpace(raw)
	if !ok || !engine.LooksLikeToken(raw) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	userID, live := p.Tokens.Resolve(raw)
	if !live || !loopback(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rest, _ := strings.CutPrefix(r.URL.Path, ProxyPrefix)
	slug, tail, _ := strings.Cut(rest, "/")
	if tail != "mcp" || !engine.ValidServerName(slug) || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost, http.MethodGet, http.MethodDelete:
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		var err error
		body, err = readLimited(r.Body, maxProxyRequest)
		if err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		if denied, id := p.deniedCall(body, userID, raw, slug); denied {
			writeRPCError(w, id)
			return
		}
	}
	ctx := r.Context()
	if r.Method != http.MethodGet {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, proxyCallTimeout)
		defer cancel()
	}
	resp, err := p.forward(ctx, r, userID, slug, body, false)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		// An access token the server no longer takes: one refresh, one retry.
		resp, err = p.forward(ctx, r, userID, slug, body, true)
	}
	if err != nil {
		if errors.Is(err, errNotConnected) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range responseHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	status := resp.StatusCode
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		// The engine must not start a sign-in of its own: no challenge goes back.
		status = http.StatusBadGateway
	}
	w.WriteHeader(status)
	copyFlushing(w, io.LimitReader(resp.Body, maxProxyResponse))
}

var errNotConnected = errors.New("connector not connected")

func (p *Proxy) forward(ctx context.Context, r *http.Request, userID int64, slug string, body []byte, force bool) (*http.Response, error) {
	c, auth, err := p.Service.target(ctx, userID, slug, force)
	if err != nil {
		if CodeOf(err) == "not_found" {
			return nil, errNotConnected
		}
		return nil, err
	}
	// The record's URL passed the guard when it was added; the dial checks the
	// address again on every connection.
	if _, err := p.Service.Guard.CheckURL(c.URL); err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	out, err := http.NewRequestWithContext(ctx, r.Method, c.URL, rd)
	if err != nil {
		return nil, err
	}
	for _, h := range requestHeaders {
		if v := r.Header.Get(h); v != "" {
			out.Header.Set(h, v)
		}
	}
	if auth != "" {
		out.Header.Set("Authorization", auth)
	}
	return p.Service.Guard.Client(0).Do(out)
}

// deniedCall reports whether a POST is a tools/call the host did not allow.
// The host must read the body exactly as the remote will, so anything a
// stricter or looser parser could read differently is refused: a batch (MCP
// 2025-06-18 has none), invalid JSON, and an object with a key repeated or
// repeated in another case (encoding/json would match "METHOD" to method).
func (p *Proxy) deniedCall(body []byte, userID int64, token, slug string) (bool, json.RawMessage) {
	msg, ok := strictObject(bytes.TrimSpace(body))
	if !ok {
		return true, nil
	}
	id := msg["id"]
	var method string
	if raw, ok := msg["method"]; ok && json.Unmarshal(raw, &method) != nil {
		return true, id
	}
	if method != "tools/call" {
		return false, nil
	}
	params, ok := strictObject(msg["params"])
	if !ok {
		return true, id
	}
	var name string
	if json.Unmarshal(params["name"], &name) != nil || name == "" || !p.Authorize(userID, token, PiToolName(slug, name)) {
		return true, id
	}
	return false, nil
}

// strictObject decodes one JSON object whose keys are unique even ignoring
// case, and nothing after it.
func strictObject(raw []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	out := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := t.(string)
		folded := strings.ToLower(key)
		if !ok || seen[folded] {
			return nil, false
		}
		seen[folded] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out[key] = v
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32001, "message": "permission_denied: the person did not allow this call"},
	})
}

// copyFlushing streams a response, flushing each chunk (server-sent events).
func copyFlushing(w http.ResponseWriter, r io.Reader) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			rc.Flush()
		}
		if err != nil {
			return
		}
	}
}
