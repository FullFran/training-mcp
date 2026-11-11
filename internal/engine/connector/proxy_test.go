package connector

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
)

type proxyFixture struct {
	*fixture
	f       *fakeRemote
	proxy   *Proxy
	tokens  *engine.Tokens
	tokA    string
	tokB    string
	mu      sync.Mutex
	allowed map[string]int // "<user>/<token>/<tool>" -> calls allowed
}

func newProxyFixture(t *testing.T) *proxyFixture {
	f := newFakeRemote(t)
	px := &proxyFixture{fixture: newFixture(t, f), f: f, tokens: engine.NewTokens(nil), allowed: map[string]int{}}
	px.tokA = px.tokens.Mint(px.a, time.Hour)
	px.tokB = px.tokens.Mint(px.b, time.Hour)
	px.proxy = &Proxy{
		Service: px.svc, Tokens: px.tokens,
		Authorize: func(userID int64, token, tool string) bool {
			px.mu.Lock()
			defer px.mu.Unlock()
			k := key(userID, token, tool)
			if px.allowed[k] == 0 {
				return false
			}
			px.allowed[k]--
			return true
		},
		Loopback: func(*http.Request) bool { return true },
	}
	return px
}

func key(userID int64, token, tool string) string {
	return strings.Join([]string{string(rune('0' + userID)), token, tool}, "/")
}

func (px *proxyFixture) allow(userID int64, token, tool string) {
	px.mu.Lock()
	px.allowed[key(userID, token, tool)]++
	px.mu.Unlock()
}

func (px *proxyFixture) post(token, slug, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, ProxyPrefix+slug+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	px.proxy.ServeHTTP(w, req)
	return w
}

const (
	initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	callBody = `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"create-page","arguments":{"title":"x"}}}`
)

func TestProxySwapsTheEngineTokenForThePersonsCredential(t *testing.T) {
	px := newProxyFixture(t)
	px.connectOAuth(px.f, px.a)
	w := px.post(px.tokA, "notion", initBody, map[string]string{"Cookie": "session=x", "X-Forwarded-For": "1.2.3.4", "Mcp-Protocol-Version": "2025-06-18"})
	if w.Code != http.StatusOK || w.Header().Get("Mcp-Session-Id") != "sess-1" || !strings.Contains(w.Body.String(), `"serverInfo"`) {
		t.Fatalf("initialize = %d %q %s", w.Code, w.Header(), w.Body)
	}
	px.f.mu.Lock()
	seen := px.f.seenAuth[len(px.f.seenAuth)-1]
	hdr := px.f.lastHdr
	px.f.mu.Unlock()
	if !strings.HasPrefix(seen, "Bearer access-") || strings.Contains(seen, engine.TokenPrefix) {
		t.Fatalf("the remote saw %q", seen)
	}
	for _, all := range px.f.seenAuth {
		if strings.Contains(all, px.tokA) || strings.Contains(all, engine.TokenPrefix) {
			t.Fatal("the engine token reached the remote server")
		}
	}
	if hdr.Get("Cookie") != "" || hdr.Get("X-Forwarded-For") != "" || hdr.Get("Mcp-Protocol-Version") != "2025-06-18" {
		t.Fatalf("forwarded headers = %v", hdr)
	}
}

func TestProxyLetsThroughOnlyAllowedCalls(t *testing.T) {
	px := newProxyFixture(t)
	px.connectOAuth(px.f, px.a)
	w := px.post(px.tokA, "notion", callBody, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "permission_denied") || !strings.Contains(w.Body.String(), `"id":7`) {
		t.Fatalf("unallowed call = %d %s", w.Code, w.Body)
	}
	px.allow(px.a, px.tokA, "mcp__notion__create_page")
	if w := px.post(px.tokA, "notion", callBody, nil); strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatalf("allowed call = %s", w.Body)
	}
	if w := px.post(px.tokA, "notion", callBody, nil); !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal("one allow let two calls through")
	}
	if w := px.post(px.tokA, "notion", `[`+callBody+`]`, nil); !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal("a batch went through")
	}
	px.f.mu.Lock()
	calls := px.f.calls
	px.f.mu.Unlock()
	if len(calls) != 1 || calls[0] != "create-page" {
		t.Fatalf("calls reaching the server = %v", calls)
	}
}

func TestProxyIsolation(t *testing.T) {
	px := newProxyFixture(t)
	px.connectOAuth(px.f, px.a)
	// Person B has no notion: their token reaches nothing of A's.
	if w := px.post(px.tokB, "notion", initBody, nil); w.Code != http.StatusNotFound {
		t.Fatalf("B on A's connector = %d", w.Code)
	}
	px.allow(px.b, px.tokB, "mcp__notion__create_page")
	px.post(px.tokB, "notion", callBody, nil)
	// B connects their own: B's calls carry B's token, never A's.
	px.connectOAuth(px.f, px.b)
	px.post(px.tokA, "notion", initBody, nil)
	px.f.mu.Lock()
	tokA := px.f.seenAuth[len(px.f.seenAuth)-1]
	px.f.mu.Unlock()
	px.post(px.tokB, "notion", initBody, nil)
	px.f.mu.Lock()
	tokB := px.f.seenAuth[len(px.f.seenAuth)-1]
	calls := len(px.f.calls)
	px.f.mu.Unlock()
	if tokA == tokB {
		t.Fatal("two people share a remote token")
	}
	if calls != 0 {
		t.Fatal("a call reached the server through someone else's connector")
	}
	// A's allow, used with B's token, is not B's.
	px.allow(px.a, px.tokA, "mcp__notion__create_page")
	if w := px.post(px.tokB, "notion", callBody, nil); !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal("A's allow used by B")
	}
}

func TestProxyDoor(t *testing.T) {
	px := newProxyFixture(t)
	px.connectOAuth(px.f, px.a)
	revoked := px.tokens.Mint(px.a, time.Hour)
	px.tokens.Revoke(revoked)
	for name, tok := range map[string]string{"none": "", "not an engine token": "abc", "revoked": revoked, "unknown": engine.TokenPrefix + "nope"} {
		if w := px.post(tok, "notion", initBody, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	// The real loopback check: an outside peer is refused even with a live token.
	px.proxy.Loopback = nil
	if w := px.post(px.tokA, "notion", initBody, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("outside peer = %d", w.Code)
	}
	px.proxy.Loopback = func(*http.Request) bool { return true }
	for _, path := range []string{ProxyPrefix + "notion/other", ProxyPrefix + "NOTION/mcp", ProxyPrefix + "notion/mcp?x=1", ProxyPrefix + "../mcp"} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, strings.NewReader(initBody))
		req.Header.Set("Authorization", "Bearer "+px.tokA)
		w := httptest.NewRecorder()
		px.proxy.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d", path, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPut, ProxyPrefix+"notion/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+px.tokA)
	w := httptest.NewRecorder()
	px.proxy.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT = %d", w.Code)
	}
}

func TestProxyRefreshesOnceOnUnauthorized(t *testing.T) {
	px := newProxyFixture(t)
	px.connectOAuth(px.f, px.a)
	px.f.mu.Lock()
	px.f.RejectAccess = 1
	px.f.mu.Unlock()
	if w := px.post(px.tokA, "notion", initBody, nil); w.Code != http.StatusOK {
		t.Fatalf("after refresh = %d %s", w.Code, w.Body)
	}
	px.f.mu.Lock()
	px.f.RejectAccess = 10
	px.f.mu.Unlock()
	w := px.post(px.tokA, "notion", initBody, nil)
	if w.Code != http.StatusBadGateway || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("still refused = %d %v", w.Code, w.Header())
	}
}

func TestProxyStreamsGetAndDeletes(t *testing.T) {
	px := newProxyFixture(t)
	px.connectOAuth(px.f, px.a)
	req := httptest.NewRequest(http.MethodDelete, ProxyPrefix+"notion/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+px.tokA)
	req.Header.Set("Mcp-Session-Id", "sess-1")
	w := httptest.NewRecorder()
	px.proxy.ServeHTTP(w, req.WithContext(context.Background()))
	if w.Code != http.StatusMethodNotAllowed { // the fake answers 405 to anything but POST
		t.Fatalf("DELETE = %d", w.Code)
	}
	_, _ = io.Copy(io.Discard, w.Body)
}
