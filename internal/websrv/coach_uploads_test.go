package websrv_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// slowUpload is a prompt whose body is still arriving: its handler holds an
// upload slot until finish is called.
type slowUpload struct {
	body *io.PipeWriter
	done chan *httptest.ResponseRecorder
}

func startSlowUpload(t *testing.T, h http.Handler) *slowUpload {
	t.Helper()
	pr, pw := io.Pipe()
	r := httptest.NewRequest("POST", base+"/coach/prompt", pr)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Coach", "1")
	u := &slowUpload{body: pw, done: make(chan *httptest.ResponseRecorder, 1)}
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		u.done <- w
	}()
	// A pipe write returns once the handler reads it: it is past the slot.
	if _, err := pw.Write([]byte(`{"text":"`)); err != nil {
		t.Fatal(err)
	}
	return u
}

func (u *slowUpload) finish(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	u.body.Write([]byte(`hola"}`))
	u.body.Close()
	return <-u.done
}

func refusedBusy(w *httptest.ResponseRecorder) bool {
	return w.Code == http.StatusTooManyRequests && strings.Contains(w.Body.String(), `"error":"busy"`)
}

// #64: photo uploads are bounded across people, and one person cannot take
// every slot.
func TestCoachUploadsAreCappedAcrossPeople(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	held := make([]*slowUpload, websrv.MaxCoachUploads)
	for i := range held {
		held[i] = startSlowUpload(t, world.as(int64(i+1)))
	}
	// Every slot is taken: another person is refused at once, and nothing of
	// theirs reaches the coach.
	late := int64(websrv.MaxCoachUploads + 1)
	if w := coachRequest(t, world.as(late), "POST", "/coach/prompt", `{"text":"hola"}`, true); !refusedBusy(w) {
		t.Fatalf("upload past the cap: %d %s", w.Code, w.Body)
	}
	if coach.callers[late] {
		t.Fatal("refused upload reached the coach")
	}
	// A slot comes back when its upload ends.
	if w := held[0].finish(t); w.Code != http.StatusAccepted {
		t.Fatalf("held upload: %d %s", w.Code, w.Body)
	}
	if w := coachRequest(t, world.as(late), "POST", "/coach/prompt", `{"text":"hola"}`, true); w.Code != http.StatusAccepted {
		t.Fatalf("upload after a slot freed: %d %s", w.Code, w.Body)
	}
	for _, u := range held[1:] {
		if w := u.finish(t); w.Code != http.StatusAccepted {
			t.Fatalf("held upload: %d %s", w.Code, w.Body)
		}
	}
}

func TestCoachUploadsOneAtATimePerPerson(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	first := startSlowUpload(t, world.as(1))
	if w := coachRequest(t, world.as(1), "POST", "/coach/prompt", `{"text":"otra"}`, true); !refusedBusy(w) {
		t.Fatalf("second upload of one person: %d %s", w.Code, w.Body)
	}
	// Another person is not held up by it.
	if w := coachRequest(t, world.as(2), "POST", "/coach/prompt", `{"text":"hola"}`, true); w.Code != http.StatusAccepted {
		t.Fatalf("other person's upload: %d %s", w.Code, w.Body)
	}
	if w := first.finish(t); w.Code != http.StatusAccepted {
		t.Fatalf("first upload: %d %s", w.Code, w.Body)
	}
	if w := coachRequest(t, world.as(1), "POST", "/coach/prompt", `{"text":"otra"}`, true); w.Code != http.StatusAccepted {
		t.Fatalf("upload after the first ended: %d %s", w.Code, w.Body)
	}
}

// #58: a turn during a pending ChatGPT login answers with its own code.
func TestCoachPromptDuringAPendingLogin(t *testing.T) {
	coach := newFakeCoach()
	coach.fail = &engine.Error{Code: engine.CodeLoginPending}
	world := newCoachWorld(t, coach)
	w := coachRequest(t, world.as(1), "POST", "/coach/prompt", `{"text":"hola"}`, true)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"error":"login_pending"`) {
		t.Fatalf("prompt during a pending login: %d %s", w.Code, w.Body)
	}
}

// The person reads why: the chat has a Spanish line for login_pending and,
// as for not_connected, refreshes the connect panel.
func TestCoachScriptExplainsAPendingLogin(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	js := get(t, world.as(1), base+"/static/coach.js").Body.String()
	if !strings.Contains(js, "login_pending: 'Termina de conectar ChatGPT antes de escribir al coach.'") {
		t.Error("coach.js has no message for login_pending")
	}
	if !strings.Contains(js, "code === 'login_pending'") {
		t.Error("coach.js does not refresh the connect panel on login_pending")
	}
}
