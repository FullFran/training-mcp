package websrv_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// fakeCoach records who every call was made for.
type fakeCoach struct {
	mu      sync.Mutex
	calls   []string
	callers map[int64]bool
	events  []engine.WorkoutEvent
	fail    error
	stream  chan engine.Event
	images  []engine.Image // the photos of the last prompt
}

func newFakeCoach() *fakeCoach { return &fakeCoach{callers: map[int64]bool{}} }

func (f *fakeCoach) record(name string, userID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	f.callers[userID] = true
}

func (f *fakeCoach) Status(userID int64) engine.Status {
	f.record("status", userID)
	return engine.Status{State: "disconnected"}
}

func (f *fakeCoach) StartLogin(userID int64) (engine.LoginInfo, error) {
	f.record("login", userID)
	if f.fail != nil {
		return engine.LoginInfo{}, f.fail
	}
	return engine.LoginInfo{UserCode: "ABCD-1234", VerificationURI: engine.DeviceVerificationURI, ExpiresIn: 900}, nil
}

func (f *fakeCoach) Disconnect(userID int64) error { f.record("disconnect", userID); return nil }

func (f *fakeCoach) Subscribe(userID int64) (<-chan engine.Event, func()) {
	f.record("stream", userID)
	ch := f.stream
	if ch == nil {
		ch = make(chan engine.Event)
		close(ch)
	}
	return ch, func() {}
}

func (f *fakeCoach) Prompt(userID int64, text string, images ...engine.Image) error {
	f.record("prompt:"+text, userID)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images = images
	return f.fail
}

func (f *fakeCoach) Abort(userID int64) bool { f.record("abort", userID); return false }

func (f *fakeCoach) Notify(userID int64, ev engine.WorkoutEvent) {
	f.record("notify", userID)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

type coachWorld struct {
	coach  *fakeCoach
	server *websrv.Server
	// as serves the app as one person, the way the login middleware does.
	as func(userID int64) http.Handler
}

func newCoachWorld(t *testing.T, coach websrv.Coach) coachWorld {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.ProvisionUser(context.Background(), "user_second"); err != nil {
		t.Fatal(err)
	}
	services := training.NewServices(store, webClock)
	server, err := websrv.NewWithOptions(services.ForRequest, services.Snapshot, webClock, base, websrv.Options{Coach: coach})
	if err != nil {
		t.Fatal(err)
	}
	fc, _ := coach.(*fakeCoach)
	return coachWorld{coach: fc, server: server, as: func(userID int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.Handler().ServeHTTP(w, r.WithContext(training.WithUser(r.Context(), userID)))
		})
	}}
}

func coachRequest(t *testing.T, h http.Handler, method, path, body string, header bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, base+path, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	if header {
		r.Header.Set("X-Coach", "1")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCoachOffServesNothingNew(t *testing.T) {
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	services := training.NewServices(store, webClock)
	server, err := websrv.New(func(context.Context) *training.Service { return services.ForUser(1) }, services.Snapshot, webClock, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range server.Routes() {
		if strings.Contains(p, "/coach") {
			t.Fatalf("coach route registered with the coach off: %s", p)
		}
	}
	if w := get(t, server.Handler(), base+"/coach/status"); w.Code != http.StatusNotFound {
		t.Fatalf("status with the coach off: %d", w.Code)
	}
	if w := get(t, server.Handler(), base+"/"); strings.Contains(w.Body.String(), `id="coach-form"`) || strings.Contains(w.Body.String(), "coach.js") {
		t.Fatal("today page shows the coach with the coach off")
	}
}

func TestCoachOnShowsTheChat(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	body := get(t, world.as(1), base+"/").Body.String()
	if !strings.Contains(body, `id="coach-form"`) || !strings.Contains(body, base+"/static/coach.js") {
		t.Fatal("today page has no coach")
	}
	if w := get(t, world.as(1), base+"/static/coach.js"); w.Code != http.StatusOK {
		t.Fatalf("coach.js: %d", w.Code)
	}
}

// Every coach route, called by the second person, reaches the coach as the
// second person and never as the owner.
func TestEveryCoachRouteActsAsTheRequestsPerson(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	cases := map[string]func() *httptest.ResponseRecorder{
		"GET /coach/status": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "GET", "/coach/status", "", false)
		},
		"POST /coach/login": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/login", "{}", true)
		},
		"POST /coach/disconnect": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/disconnect", "{}", true)
		},
		"GET /coach/stream": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "GET", "/coach/stream", "", false)
		},
		"POST /coach/prompt": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/prompt", `{"text":"hola"}`, true)
		},
		"POST /coach/abort": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/abort", "{}", true)
		},
	}
	for _, p := range world.server.Routes() {
		route := strings.TrimSpace(strings.Replace(p, base, "", 1))
		if !strings.Contains(route, "/coach/") {
			continue
		}
		run, ok := cases[route]
		if !ok {
			t.Fatalf("no isolation case for %s", route)
		}
		if w := run(); w.Code >= 400 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body)
		}
	}
	if len(coach.callers) != 1 || !coach.callers[2] {
		t.Fatalf("coach called for %v, want only person 2", coach.callers)
	}
	if len(coach.calls) != len(cases) {
		t.Fatalf("calls = %v", coach.calls)
	}
}

func TestCoachWritesNeedTheCoachHeader(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	for _, path := range []string{"/coach/login", "/coach/disconnect", "/coach/prompt", "/coach/abort"} {
		if w := coachRequest(t, world.as(1), "POST", path, `{"text":"x"}`, false); w.Code != http.StatusForbidden {
			t.Errorf("%s without the header: %d", path, w.Code)
		}
	}
	if len(coach.calls) != 0 {
		t.Fatalf("coach reached: %v", coach.calls)
	}
}

func TestCoachRefusesARequestWithoutAnIdentity(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	if w := coachRequest(t, world.server.Handler(), "GET", "/coach/status", "", false); w.Code != http.StatusForbidden {
		t.Fatalf("status without identity: %d", w.Code)
	}
	if len(coach.calls) != 0 {
		t.Fatalf("coach reached: %v", coach.calls)
	}
}

func TestCoachAnswersWithFixedCodes(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	coach.fail = &engine.Error{Code: engine.CodeBusy}
	w := coachRequest(t, world.as(1), "POST", "/coach/login", "{}", true)
	if w.Code != http.StatusTooManyRequests || strings.TrimSpace(w.Body.String()) != `{"error":"busy"}` {
		t.Fatalf("busy login: %d %s", w.Code, w.Body)
	}
	coach.fail = &engine.Error{Code: engine.CodeNotConnected}
	if w := coachRequest(t, world.as(1), "POST", "/coach/prompt", `{"text":"hola"}`, true); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "not_connected") {
		t.Fatalf("not connected prompt: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"text":"   "}`, `{"text":"` + strings.Repeat("a", 4001) + `"}`, `{"text":"x","system":"evil"}`, `nope`} {
		if w := coachRequest(t, world.as(1), "POST", "/coach/prompt", body, true); w.Code != http.StatusBadRequest {
			t.Errorf("prompt %.40q: %d", body, w.Code)
		}
	}
}

func TestLoggingASetTellsThatPersonsCoach(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	form := url.Values{"exercise": {"Press banca"}, "weight_kg": {"80"}, "reps": {"8"}, "rpe": {"8.5"}}
	if w := post(t, world.as(2), base+"/sets", form, true); w.Code != http.StatusOK {
		t.Fatalf("add set: %d", w.Code)
	}
	if len(coach.events) != 1 || !coach.callers[2] || coach.callers[1] {
		t.Fatalf("events %v for %v", coach.events, coach.callers)
	}
	ev := coach.events[0]
	if ev.Kind != "set_logged" || ev.WeightKG != 80 || ev.Reps != 8 || ev.RPE != 8.5 || ev.Exercise == "" || !ev.At.Equal(webClock()) {
		t.Fatalf("event = %+v", ev)
	}
	// A refused set is not an event.
	bad := url.Values{"exercise": {"Press banca"}, "weight_kg": {"80"}, "reps": {"8"}, "rpe": {"11"}}
	post(t, world.as(2), base+"/sets", bad, true)
	if len(coach.events) != 1 {
		t.Fatalf("invalid set notified: %v", coach.events)
	}
}

func TestCoachStreamSendsServerEvents(t *testing.T) {
	coach := newFakeCoach()
	coach.stream = make(chan engine.Event, 4)
	world := newCoachWorld(t, coach)
	srv := httptest.NewServer(world.as(1))
	defer srv.Close()
	coach.stream <- engine.Event{Name: "turn", Data: map[string]any{"origin": "reaction"}}
	coach.stream <- engine.Event{Name: "delta", Data: map[string]any{"text": "<b>bien</b>"}}
	close(coach.stream)
	res, err := http.Get(srv.URL + base + "/coach/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", res.Header)
	}
	var lines []string
	sc := bufio.NewScanner(res.Body)
	deadline := time.After(5 * time.Second)
	done := make(chan struct{})
	go func() {
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-deadline:
		t.Fatal("stream did not end")
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "event: turn\ndata: {\"origin\":\"reaction\"}") || !strings.Contains(got, `event: delta`) || !strings.Contains(got, "\\u003cb\\u003ebien") {
		t.Fatalf("stream = %s", got)
	}
}

func TestServiceWorkerNeverCachesTheCoach(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	body := get(t, world.as(1), base+"/sw.js").Body.String()
	if !strings.Contains(body, "url.pathname.startsWith(BASE + '/coach/')) return;") {
		t.Fatal("service worker handles coach requests")
	}
}
