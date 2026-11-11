package websrv_test

import (
	"bufio"
	"context"
	"encoding/json"
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
	chosen  [2]string      // the last model and thinking level set
	conv    int64          // the conversation of the last prompt
	opened  int64          // the last conversation opened
	title   string         // the last title given
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

func (f *fakeCoach) Prompt(userID, conv int64, text string, images ...engine.Image) (int64, error) {
	f.record("prompt:"+text, userID)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images = images
	f.conv = conv
	if conv == 0 {
		conv = 7
	}
	return conv, f.fail
}

func (f *fakeCoach) Conversations(userID int64) ([]engine.Conversation, error) {
	f.record("conversations", userID)
	return []engine.Conversation{{ID: 7, Title: "Pierna"}}, f.fail
}

func (f *fakeCoach) Conversation(userID, conv int64) (engine.Conversation, []engine.ConversationMessage, error) {
	f.record("conversation", userID)
	if conv != 7 {
		return engine.Conversation{}, nil, &engine.Error{Code: engine.CodeNotFound}
	}
	return engine.Conversation{ID: 7, Title: "Pierna"}, []engine.ConversationMessage{{Role: "user", Text: "<b>hola</b>"}}, nil
}

func (f *fakeCoach) OpenConversation(userID, conv int64) error {
	f.record("open", userID)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = conv
	return nil
}

func (f *fakeCoach) RenameConversation(userID, conv int64, title string) error {
	f.record("rename", userID)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.title = title
	return nil
}

func (f *fakeCoach) DeleteConversation(userID, conv int64) error {
	f.record("delete", userID)
	return nil
}

func (f *fakeCoach) DeleteConversations(userID int64) error {
	f.record("delete-all", userID)
	return nil
}

func (f *fakeCoach) Abort(userID int64) bool { f.record("abort", userID); return false }

func (f *fakeCoach) Models(userID int64) (engine.ModelList, error) {
	f.record("models", userID)
	if f.fail != nil {
		return engine.ModelList{}, f.fail
	}
	return engine.ModelList{Models: []engine.ModelInfo{{ID: "gpt-a", Name: "GPT A", Thinking: []string{"off", "high"}}}, Model: "gpt-a", Thinking: "off"}, nil
}

func (f *fakeCoach) SetPreferences(userID int64, model, thinking string) error {
	f.record("settings", userID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if model != "gpt-a" {
		return &engine.Error{Code: engine.CodeBadRequest}
	}
	f.chosen = [2]string{model, thinking}
	return nil
}

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
		"GET /coach/models": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "GET", "/coach/models", "", false)
		},
		"POST /coach/settings": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/settings", `{"model":"gpt-a","thinking":"high"}`, true)
		},
		"GET /coach/conversations": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "GET", "/coach/conversations", "", false)
		},
		"GET /coach/conversations/{id}": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "GET", "/coach/conversations/7", "", false)
		},
		"POST /coach/conversations/{id}/rename": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/conversations/7/rename", `{"title":"Pierna"}`, true)
		},
		"POST /coach/conversations/{id}/delete": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/conversations/7/delete", "{}", true)
		},
		"POST /coach/conversations/delete-all": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/conversations/delete-all", "{}", true)
		},
		"POST /coach/open": func() *httptest.ResponseRecorder {
			return coachRequest(t, world.as(2), "POST", "/coach/open", `{"conversation":7}`, true)
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
	for _, path := range []string{"/coach/login", "/coach/disconnect", "/coach/prompt", "/coach/abort", "/coach/settings",
		"/coach/open", "/coach/conversations/7/rename", "/coach/conversations/7/delete", "/coach/conversations/delete-all"} {
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

// The model list and the choice: the list as the person's engine reports it;
// a choice outside it is refused with a fixed code and changes nothing.
func TestCoachModelsAndSettings(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	w := coachRequest(t, world.as(1), "GET", "/coach/models", "", false)
	var list engine.ModelList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Models) != 1 || list.Model != "gpt-a" {
		t.Fatalf("models: %d %s", w.Code, w.Body)
	}
	if w := coachRequest(t, world.as(1), "POST", "/coach/settings", `{"model":"gpt-a","thinking":"high"}`, true); w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	if coach.chosen != [2]string{"gpt-a", "high"} {
		t.Fatalf("chosen = %v", coach.chosen)
	}
	for body, want := range map[string]int{
		`{"model":"other","thinking":"off"}`:           http.StatusBadRequest,
		`{"model":"gpt-a","thinking":"off","extra":1}`: http.StatusBadRequest,
		`not json`: http.StatusBadRequest,
	} {
		w := coachRequest(t, world.as(1), "POST", "/coach/settings", body, true)
		if w.Code != want || !strings.Contains(w.Body.String(), `"bad_request"`) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if coach.chosen != [2]string{"gpt-a", "high"} {
		t.Fatalf("a refused choice changed it: %v", coach.chosen)
	}
	coach.fail = &engine.Error{Code: engine.CodeNotConnected}
	if w := coachRequest(t, world.as(1), "GET", "/coach/models", "", false); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "not_connected") {
		t.Fatalf("models while not connected: %d %s", w.Code, w.Body)
	}
}

// Conversations: list, read (text as JSON, never HTML), continue, open, rename,
// delete one, delete all. Unknown ids are 404 with a fixed code.
func TestCoachConversationRoutes(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	h := world.as(1)
	if w := coachRequest(t, h, "GET", "/coach/conversations", "", false); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"title":"Pierna"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w := coachRequest(t, h, "GET", "/coach/conversations/7", "", false)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `\u003cb\u003ehola`) || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("read: %d %s %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if w := coachRequest(t, h, "GET", "/coach/conversations/8", "", false); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("unknown: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{"/coach/conversations/0", "/coach/conversations/-1", "/coach/conversations/x"} {
		if w := coachRequest(t, h, "GET", bad, "", false); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	w = coachRequest(t, h, "POST", "/coach/prompt", `{"text":"sigo","conversation":7}`, true)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"conversation":7`) || coach.conv != 7 {
		t.Fatalf("continue: %d %s conv=%d", w.Code, w.Body, coach.conv)
	}
	w = coachRequest(t, h, "POST", "/coach/prompt", `{"text":"nueva"}`, true)
	if w.Code != http.StatusAccepted || coach.conv != 0 || !strings.Contains(w.Body.String(), `"conversation":7`) {
		t.Fatalf("new: %d %s", w.Code, w.Body)
	}
	if w := coachRequest(t, h, "POST", "/coach/open", `{"conversation":7}`, true); w.Code != http.StatusOK || coach.opened != 7 {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
	if w := coachRequest(t, h, "POST", "/coach/conversations/7/rename", `{"title":"Pierna A"}`, true); w.Code != http.StatusOK || coach.title != "Pierna A" {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if w := coachRequest(t, h, "POST", "/coach/conversations/7/rename", `{"title":"x","extra":1}`, true); w.Code != http.StatusBadRequest {
		t.Fatalf("rename with an unknown field: %d", w.Code)
	}
}
