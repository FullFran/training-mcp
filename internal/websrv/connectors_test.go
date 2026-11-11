package websrv_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/engine/connector"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// fakeConnectors records who every call was made for.
type fakeConnectors struct {
	mu          sync.Mutex
	callers     map[int64]bool
	calls       []string
	callbackErr error
}

func (f *fakeConnectors) rec(name string, userID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	f.callers[userID] = true
}

func (f *fakeConnectors) List(_ context.Context, userID int64) ([]connector.View, error) {
	f.rec("list", userID)
	return []connector.View{{ID: 5, Name: "notion", Label: "Notion", URL: "https://mcp.notion.com/mcp", State: "connected"}}, nil
}

func (f *fakeConnectors) Add(_ context.Context, userID int64, rawURL, label, token string) (connector.View, error) {
	f.rec("add:"+rawURL, userID)
	return connector.View{ID: 5, Name: "notion", Label: label, URL: rawURL}, nil
}

func (f *fakeConnectors) Connect(_ context.Context, userID, id int64) (connector.ConnectResult, error) {
	f.rec("connect", userID)
	return connector.ConnectResult{State: "authorize", AuthorizeURL: "https://auth.example.test/authorize?x=1"}, nil
}

func (f *fakeConnectors) Callback(_ context.Context, userID int64, state, code string) (int64, error) {
	f.rec("callback:"+state+":"+code, userID)
	return 5, f.callbackErr
}

func (f *fakeConnectors) Tools(_ context.Context, userID, id int64) ([]connector.Tool, error) {
	f.rec("tools", userID)
	return []connector.Tool{{Name: "search", Model: "mcp__notion__search"}}, nil
}

func (f *fakeConnectors) Disconnect(_ context.Context, userID, id int64) error {
	f.rec("disconnect", userID)
	return nil
}

func (f *fakeConnectors) Descriptor(_ context.Context, userID, id int64) (connector.ServerJSON, error) {
	f.rec("descriptor", userID)
	return connector.ServerJSON{Name: "com.notion.mcp/notion"}, nil
}

func (f *fakeConnectors) Catalog(_ context.Context, userID int64) ([]connector.CatalogView, error) {
	f.rec("catalog", userID)
	return []connector.CatalogView{{CatalogEntry: connector.CatalogEntry{ID: "notion", Title: "Notion", Category: "notes", CategoryLabel: "Notas y diario", URL: "https://mcp.notion.com/mcp"}}}, nil
}

func (f *fakeConnectors) AddFromCatalog(_ context.Context, userID int64, entry string) (connector.View, error) {
	f.rec("add-catalog:"+entry, userID)
	if entry != "notion" {
		return connector.View{}, &connector.Error{Code: "not_found"}
	}
	return connector.View{ID: 5, Name: "notion", Label: "Notion", URL: "https://mcp.notion.com/mcp"}, nil
}

func (f *fakeConnectors) Remove(_ context.Context, userID, id int64) error {
	f.rec("remove", userID)
	return nil
}

// approvingCoach is the fake coach that also answers approvals.
type approvingCoach struct {
	*fakeCoach
	answers []string
}

func (c *approvingCoach) Answer(userID int64, id, answer string) error {
	c.record("answer:"+id+":"+answer, userID)
	switch {
	case answer != engine.AnswerAllowOnce && answer != engine.AnswerAllowAlways && answer != engine.AnswerRejectOnce && answer != engine.AnswerRejectAlways:
		return engine.ErrBadAnswer
	case id != "q1":
		return engine.ErrNoApproval
	}
	return nil
}

func (c *approvingCoach) Grants(userID int64) []engine.Grant {
	c.record("grants", userID)
	return []engine.Grant{{Tool: "mcp__notion__search", Server: "notion", Answer: engine.AnswerAllowAlways}}
}

func (c *approvingCoach) Revoke(userID int64, server string) { c.record("revoke:"+server, userID) }

func newConnectorWorld(t *testing.T) (*approvingCoach, *fakeConnectors, *websrv.Server, func(int64) http.Handler) {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	store.ProvisionUser(context.Background(), "user_second")
	services := training.NewServices(store, webClock)
	coach := &approvingCoach{fakeCoach: newFakeCoach()}
	conns := &fakeConnectors{callers: map[int64]bool{}}
	server, err := websrv.NewWithOptions(services.ForRequest, services.Snapshot, webClock, base, websrv.Options{Coach: coach, Connectors: conns})
	if err != nil {
		t.Fatal(err)
	}
	as := func(userID int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.Handler().ServeHTTP(w, r.WithContext(training.WithUser(r.Context(), userID)))
		})
	}
	return coach, conns, server, as
}

func TestEveryConnectorRouteActsAsTheRequestsPerson(t *testing.T) {
	coach, conns, server, as := newConnectorWorld(t)
	h := as(2)
	cases := map[string]func() *httptest.ResponseRecorder{
		"GET /coach/connectors": func() *httptest.ResponseRecorder { return coachRequest(t, h, "GET", "/coach/connectors", "", false) },
		"GET /coach/connectors/list": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "GET", "/coach/connectors/list", "", false)
		},
		"POST /coach/connectors": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "POST", "/coach/connectors", `{"url":"https://x.test/mcp","label":"X"}`, true)
		},
		"GET /coach/connectors/catalog": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "GET", "/coach/connectors/catalog", "", false)
		},
		"POST /coach/connectors/catalog": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "POST", "/coach/connectors/catalog", `{"entry":"notion"}`, true)
		},
		"GET /coach/connectors/callback": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "GET", "/coach/connectors/callback?state=s&code=c", "", false)
		},
		"POST /coach/connectors/{id}/connect": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "POST", "/coach/connectors/5/connect", "{}", true)
		},
		"GET /coach/connectors/{id}/tools": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "GET", "/coach/connectors/5/tools", "", false)
		},
		"GET /coach/connectors/{id}/server.json": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "GET", "/coach/connectors/5/server.json", "", false)
		},
		"POST /coach/connectors/{id}/disconnect": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "POST", "/coach/connectors/5/disconnect", "{}", true)
		},
		"POST /coach/connectors/{id}/remove": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "POST", "/coach/connectors/5/remove", "{}", true)
		},
		"POST /coach/connectors/{id}/revoke": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "POST", "/coach/connectors/5/revoke", "{}", true)
		},
		"POST /coach/approval": func() *httptest.ResponseRecorder {
			return coachRequest(t, h, "POST", "/coach/approval", `{"id":"q1","answer":"allow_once"}`, true)
		},
	}
	seen := 0
	for _, p := range server.Routes() {
		route := strings.TrimSpace(strings.Replace(p, base, "", 1))
		if !strings.Contains(route, "/coach/connectors") && !strings.Contains(route, "/coach/approval") {
			continue
		}
		seen++
		run, ok := cases[route]
		if !ok {
			t.Fatalf("no isolation case for %s", route)
		}
		if w := run(); w.Code >= 400 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body)
		}
	}
	if seen != len(cases) {
		t.Fatalf("%d connector routes registered, %d cases", seen, len(cases))
	}
	if len(conns.callers) != 1 || !conns.callers[2] {
		t.Fatalf("connectors called for %v", conns.callers)
	}
	if len(coach.callers) != 1 || !coach.callers[2] {
		t.Fatalf("coach called for %v", coach.callers)
	}
}

func TestConnectorRoutesNeedAPersonAndTheCoachHeader(t *testing.T) {
	_, conns, server, as := newConnectorWorld(t)
	for _, path := range []string{"/coach/connectors", "/coach/connectors/list", "/coach/connectors/5/tools"} {
		if w := coachRequest(t, server.Handler(), "GET", path, "", false); w.Code != http.StatusForbidden {
			t.Errorf("GET %s without a person = %d", path, w.Code)
		}
	}
	for _, path := range []string{"/coach/connectors", "/coach/connectors/5/connect", "/coach/connectors/5/remove", "/coach/approval"} {
		if w := coachRequest(t, as(2), "POST", path, "{}", false); w.Code != http.StatusForbidden {
			t.Errorf("POST %s without X-Coach = %d", path, w.Code)
		}
	}
	if len(conns.calls) != 0 {
		t.Fatalf("calls = %v", conns.calls)
	}
	if w := coachRequest(t, as(2), "POST", "/coach/connectors", `{"url":"https://x.test","extra":1}`, true); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", w.Code)
	}
	if w := coachRequest(t, as(2), "POST", "/coach/connectors/abc/connect", "{}", true); w.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d", w.Code)
	}
}

func TestConnectorCallbackRedirectsWithAFixedCode(t *testing.T) {
	_, conns, _, as := newConnectorWorld(t)
	w := coachRequest(t, as(2), "GET", "/coach/connectors/callback?state=s1&code=c1", "", false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/coach/connectors?connected=5" {
		t.Fatalf("success = %d %s", w.Code, w.Header().Get("Location"))
	}
	conns.callbackErr = connectorErr("state_invalid")
	w = coachRequest(t, as(2), "GET", "/coach/connectors/callback?state=s1&code=c1", "", false)
	if w.Header().Get("Location") != base+"/coach/connectors?error=state_invalid" {
		t.Fatalf("failure = %s", w.Header().Get("Location"))
	}
	w = coachRequest(t, as(2), "GET", "/coach/connectors/callback?error=access_denied&error_description=<script>leak</script>", "", false)
	if loc := w.Header().Get("Location"); loc != base+"/coach/connectors?error=denied" {
		t.Fatalf("provider error = %s", loc)
	}
}

func connectorErr(code string) error { return &connector.Error{Code: code} }

func TestApprovalAnswers(t *testing.T) {
	_, _, _, as := newConnectorWorld(t)
	for body, want := range map[string]int{
		`{"id":"q1","answer":"allow_always"}`:     http.StatusOK,
		`{"id":"q2","answer":"allow_once"}`:       http.StatusNotFound,
		`{"id":"q1","answer":"allow_forever"}`:    http.StatusBadRequest,
		`{"id":"q1","answer":"allow_once","x":1}`: http.StatusBadRequest,
	} {
		if w := coachRequest(t, as(2), "POST", "/coach/approval", body, true); w.Code != want {
			t.Errorf("%s = %d, want %d", body, w.Code, want)
		}
	}
}

func TestConnectorsPanelIsLinkedOnlyWhenOn(t *testing.T) {
	_, _, _, as := newConnectorWorld(t)
	body := get(t, as(1), base+"/").Body.String()
	if !strings.Contains(body, base+"/coach/connectors") || !strings.Contains(body, "coach-approvals.js") {
		t.Fatal("today page does not link the connectors")
	}
	page := get(t, as(1), base+"/coach/connectors").Body.String()
	if !strings.Contains(page, `id="cx-add"`) || !strings.Contains(page, "connectors.js") {
		t.Fatal("connectors page")
	}
	off := newCoachWorld(t, newFakeCoach())
	if b := get(t, off.as(1), base+"/").Body.String(); strings.Contains(b, "/coach/connectors") || strings.Contains(b, "coach-approvals.js") {
		t.Fatal("connectors linked without connectors")
	}
	for _, p := range off.server.Routes() {
		if strings.Contains(p, "/coach/connectors") || strings.Contains(p, "/coach/approval") {
			t.Fatalf("connector route without connectors: %s", p)
		}
	}
	for _, f := range []string{"/static/connectors.js", "/static/coach-approvals.js", "/static/connectors.css"} {
		if w := get(t, as(1), base+f); w.Code != http.StatusOK {
			t.Fatalf("%s = %d", f, w.Code)
		}
	}
}

func TestCatalogRoutes(t *testing.T) {
	_, conns, _, as := newConnectorWorld(t)
	w := coachRequest(t, as(2), "GET", "/coach/connectors/catalog", "", false)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"entries":[{"id":"notion"`) || !strings.Contains(w.Body.String(), `"category_label":"Notas y diario"`) {
		t.Fatalf("catalog = %d %s", w.Code, w.Body)
	}
	w = coachRequest(t, as(2), "POST", "/coach/connectors/catalog", `{"entry":"notion"}`, true)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"name":"notion"`) {
		t.Fatalf("one tap = %d %s", w.Code, w.Body)
	}
	if w := coachRequest(t, as(2), "POST", "/coach/connectors/catalog", `{"entry":"nope"}`, true); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("unknown entry = %d %s", w.Code, w.Body)
	}
	// Only an entry id: no URL, label or token can ride along.
	for _, body := range []string{`{"entry":"notion","url":"https://x.test"}`, `{"entry":"notion","token":"x"}`, `not json`} {
		if w := coachRequest(t, as(2), "POST", "/coach/connectors/catalog", body, true); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", body, w.Code)
		}
	}
	if w := coachRequest(t, as(2), "POST", "/coach/connectors/catalog", `{"entry":"notion"}`, false); w.Code < 400 {
		t.Fatalf("without X-Coach = %d", w.Code)
	}
	for _, c := range conns.calls {
		if strings.Contains(c, "x.test") {
			t.Fatalf("a URL reached the service: %v", conns.calls)
		}
	}
	page := get(t, as(1), base+"/coach/connectors").Body.String()
	if !strings.Contains(page, `id="cx-catalog"`) || !strings.Contains(page, `id="cx-add"`) || strings.Index(page, `id="cx-catalog"`) > strings.Index(page, `id="cx-add"`) {
		t.Fatal("the catalog must come first and the free URL field must stay")
	}
}
