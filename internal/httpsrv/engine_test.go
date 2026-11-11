package httpsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

type engineFixture struct {
	tokens  *engine.Tokens
	handler http.Handler
	store   *sqlitestore.Store
}

func newEngineFixture(t *testing.T, authDisabled bool) *engineFixture {
	t.Helper()
	store, err := sqlitestore.Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	services := training.NewServices(store, time.Now)
	tokens := engine.NewTokens(nil)
	token := staticToken
	if authDisabled {
		token = ""
	}
	return &engineFixture{tokens: tokens, store: store, handler: RoutesWithEngine(mcpsrv.New(services.ForRequest).Handler(), authDisabled, token, nil, nil, nil, tokens)}
}

// engineCall is a tools/call as the engine sends it: straight to the loopback
// address, no proxy header.
func engineCall(t *testing.T, handler http.Handler, token, tool string, args map[string]any, tweak func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/mcp", strings.NewReader(string(body)))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Authorization", "Bearer "+token)
	if tweak != nil {
		tweak(r)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestEngineTokenActsAsItsOwnPerson(t *testing.T) {
	f := newEngineFixture(t, false)
	ctx := t.Context()
	guest, err := f.store.ProvisionUser(ctx, "user_guest")
	if err != nil {
		t.Fatal(err)
	}
	ownerTok := f.tokens.Mint(training.OwnerUserID, time.Hour)
	guestTok := f.tokens.Mint(guest, time.Hour)
	plan := map[string]any{"name": "Guest plan", "items": []map[string]any{{"exercise": "banca", "target_sets": 1}}}
	if w := engineCall(t, f.handler, guestTok, "create_plan", plan, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "structuredContent") {
		t.Fatalf("guest create_plan: %d %s", w.Code, w.Body)
	}
	if w := engineCall(t, f.handler, ownerTok, "list_plans", map[string]any{}, nil); strings.Contains(w.Body.String(), "Guest plan") {
		t.Fatalf("the owner's engine sees the guest's plan: %s", w.Body)
	}
	if w := engineCall(t, f.handler, guestTok, "list_plans", map[string]any{}, nil); !strings.Contains(w.Body.String(), "Guest plan") {
		t.Fatalf("the guest's engine lost its plan: %s", w.Body)
	}
}

func TestEngineTokenIsHonouredOnlyOnDirectLoopback(t *testing.T) {
	f := newEngineFixture(t, false)
	tok := f.tokens.Mint(training.OwnerUserID, time.Hour)
	cases := map[string]func(*http.Request){
		"remote peer":     func(r *http.Request) { r.RemoteAddr = "203.0.113.5:4000" },
		"public host":     func(r *http.Request) { r.Host = "training.example.com" },
		"through a proxy": func(r *http.Request) { r.Header.Set("X-Forwarded-For", "203.0.113.5") },
	}
	for name, tweak := range cases {
		if w := engineCall(t, f.handler, tok, "list_plans", map[string]any{}, tweak); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, w.Code)
		}
	}
	f.tokens.Revoke(tok)
	if w := engineCall(t, f.handler, tok, "list_plans", map[string]any{}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: status %d", w.Code)
	}
	if w := engineCall(t, f.handler, engine.TokenPrefix+"invented", "list_plans", map[string]any{}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("unknown token: status %d", w.Code)
	}
}

// With the auth bypass on, /mcp acts as the owner for anyone; an engine token
// must still act as its own person, never as the owner.
func TestEngineTokenIsNotTheOwnerUnderTheAuthBypass(t *testing.T) {
	f := newEngineFixture(t, true)
	guest, err := f.store.ProvisionUser(t.Context(), "user_guest")
	if err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{"name": "Owner plan", "items": []map[string]any{{"exercise": "banca", "target_sets": 1}}}
	if w := engineCall(t, f.handler, "", "create_plan", plan, func(r *http.Request) { r.Header.Del("Authorization") }); w.Code != http.StatusOK {
		t.Fatalf("bypass create_plan: %d", w.Code)
	}
	if w := engineCall(t, f.handler, f.tokens.Mint(guest, time.Hour), "list_plans", map[string]any{}, nil); strings.Contains(w.Body.String(), "Owner plan") {
		t.Fatalf("guest's engine acted as the owner: %s", w.Body)
	}
}

func TestEngineTokenDoesNotOpenTheWebApp(t *testing.T) {
	tokens := engine.NewTokens(nil)
	web := &WebUI{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }), BasePath: "/app"}
	h := RoutesWithEngine(http.NotFoundHandler(), false, staticToken, web, nil, nil, tokens)
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/app/", nil)
	r.RemoteAddr = "127.0.0.1:4000"
	r.Header.Set("Authorization", "Bearer "+tokens.Mint(training.OwnerUserID, time.Hour))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("web app with an engine token: %d", w.Code)
	}
}

func TestStaticTokenStillWorksWithTheEngineOn(t *testing.T) {
	f := newEngineFixture(t, false)
	if w := engineCall(t, f.handler, staticToken, "list_plans", map[string]any{}, func(r *http.Request) { r.RemoteAddr = "203.0.113.5:4000" }); w.Code != http.StatusOK {
		t.Fatalf("static token: %d", w.Code)
	}
}
