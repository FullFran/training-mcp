package httpsrv

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBearerMiddlewareRejectsWithoutInvokingHandler(t *testing.T) {
	called := false
	h := Bearer("correct", false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusTeapot) }))
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || called || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("status=%d called=%v headers=%v", w.Code, called, w.Header())
	}
}

func TestBearerAllowsValidAndRejectsOversizedBodies(t *testing.T) {
	h := Bearer("correct", false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("x"))
	r.Header.Set("Authorization", "Bearer correct")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("valid status=%d", w.Code)
	}
	large := strings.Repeat("x", 1<<20+1)
	r = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(large))
	r.Header.Set("Authorization", "Bearer correct")
	w = httptest.NewRecorder()
	MaxBody(Bearer("correct", false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))).ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status=%d", w.Code)
	}
}

func TestBearerAuthorizationPolicyCanDenyAuthenticatedRequest(t *testing.T) {
	called := false
	h := BearerWithPolicy("correct", false, func(*http.Request) bool { return false }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer correct")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || called || !strings.Contains(w.Body.String(), "forbidden") {
		t.Fatalf("status=%d called=%v body=%q", w.Code, called, w.Body.String())
	}
}

func TestRoutesHealthAndMethodBehaviorAndBypassWarning(t *testing.T) {
	h := Routes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }), true, "")
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		r := httptest.NewRequest(method, "/health", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if method == http.MethodGet && (w.Code != http.StatusOK || w.Body.String() != "{\"status\":\"ok\"}\n") {
			t.Fatalf("health response=%d %q", w.Code, w.Body.String())
		}
		if method != http.MethodGet && w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("health %s status=%d", method, w.Code)
		}
	}
	var logs bytes.Buffer
	WarningIfDisabled(true, log.New(&logs, "", 0))
	if !strings.Contains(logs.String(), "unsafe") || !strings.Contains(logs.String(), "tunnel-only") {
		t.Fatalf("warning=%q", logs.String())
	}
}

func TestRoutesRequireBearerForEveryMCPMethodWithoutLeakingCredential(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		r := httptest.NewRequest(method, "/mcp", strings.NewReader("{}"))
		w := httptest.NewRecorder()
		Routes(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), false, "super-secret").ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "super-secret") {
			t.Fatalf("%s status=%d body=%q", method, w.Code, w.Body.String())
		}
	}
	wrong := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	wrong.Header.Set("Authorization", "Bearer wrong-super-secret")
	w := httptest.NewRecorder()
	Routes(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), false, "super-secret").ServeHTTP(w, wrong)
	if strings.Contains(w.Body.String(), "super-secret") || strings.Contains(w.Body.String(), "wrong-super-secret") {
		t.Fatalf("credential leaked in response %q", w.Body.String())
	}
}

func TestHTTPShutdownDrainsAnInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	response := make(chan error, 1)
	go func() {
		resp, err := server.Client().Get(server.URL)
		if resp != nil {
			resp.Body.Close()
		}
		response <- err
	}()
	<-started
	shutdown := make(chan error, 1)
	go func() { shutdown <- server.Config.Shutdown(context.Background()) }()
	select {
	case err := <-shutdown:
		if err == nil {
			t.Fatal("shutdown returned before in-flight request completed")
		}
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	if err := <-shutdown; err != nil {
		t.Fatal(err)
	}
}
