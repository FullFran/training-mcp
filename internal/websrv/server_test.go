package websrv_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

const base = "/s3cr3t"

func newServer(t *testing.T) (http.Handler, *training.Service) {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := training.NewService(store, func() time.Time {
		return time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	})
	server, err := websrv.New(service, func() time.Time {
		return time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), service
}

func post(t *testing.T, h http.Handler, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func setForm(exercise, weight, reps, rpe string) url.Values {
	return url.Values{
		"exercise":  {exercise},
		"weight_kg": {weight},
		"reps":      {reps},
		"rpe":       {rpe},
	}
}

func TestTodayPageRendersUnderBasePath(t *testing.T) {
	h, _ := newServer(t)
	w := get(t, h, base+"/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`hx-post="` + base + `/sets"`,
		base + "/static/app.css",
		base + "/manifest.webmanifest",
		"Sin series todavía",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("today page missing %q", want)
		}
	}
}

func TestAddSetCreatesTodaysSessionLazilyAndReturnsPanel(t *testing.T) {
	h, service := newServer(t)

	// Merely opening the app must not create a session.
	get(t, h, base+"/")
	sessions, err := service.ListSessions(t.Context(), training.ListFilter{Limit: 10})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions after page load = %#v, %v, want none", sessions, err)
	}

	w := post(t, h, base+"/sets", setForm(" Press Inclinado ", "36", "10", "9"), true)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.HasPrefix(strings.TrimSpace(body), `<section id="panel"`) {
		t.Fatalf("htmx response is not the panel fragment: %q", body[:min(120, len(body))])
	}
	// RPE 9 -> SI 1.2, and the exercise is normalized by the domain service.
	for _, want := range []string{"press inclinado", "36 kg × 10 @9", "SI 1.2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("panel missing %q, got %q", want, body)
		}
	}

	sessions, err = service.ListSessions(t.Context(), training.ListFilter{Limit: 10})
	if err != nil || len(sessions) != 1 || sessions[0].Date != "2026-08-07" {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
}

func TestAddSetReusesTodaysSessionInsteadOfCreatingMore(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("squat", "100", "5", "8"), true)
	post(t, h, base+"/sets", setForm("squat", "100", "5", "8.5"), true)

	sessions, err := service.ListSessions(t.Context(), training.ListFilter{Limit: 10})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, %v, want exactly one", sessions, err)
	}
	if sessions[0].SetCount != 2 {
		t.Fatalf("set count = %d, want 2", sessions[0].SetCount)
	}
}

func TestAddSetRejectsInvalidInputWithoutWriting(t *testing.T) {
	for _, tt := range []struct {
		name string
		form url.Values
	}{
		{"empty exercise", setForm("", "36", "10", "9")},
		{"rpe above range", setForm("squat", "36", "10", "11")},
		{"non numeric weight", setForm("squat", "heavy", "10", "9")},
		{"zero reps", setForm("squat", "36", "0", "9")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, service := newServer(t)
			w := post(t, h, base+"/sets", tt.form, true)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 with an inline error", w.Code)
			}
			if !strings.Contains(w.Body.String(), `class="alert"`) {
				t.Fatalf("expected an inline error, got %q", w.Body.String())
			}
			sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 10})
			for _, s := range sessions {
				if s.SetCount != 0 {
					t.Fatalf("invalid input wrote a set: %#v", s)
				}
			}
		})
	}
}

func TestDeleteSetRemovesItAndUpdatesTotal(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("squat", "100", "5", "9"), true)

	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	session, err := service.GetSession(t.Context(), sessions[0].ID)
	if err != nil || len(session.Sets) != 1 {
		t.Fatalf("session = %#v, %v", session, err)
	}

	w := post(t, h, base+"/sets/"+itoa(session.Sets[0].ID)+"/delete", nil, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SI 0") {
		t.Fatalf("panel should show a zero total, got %q", w.Body.String())
	}
}

func TestDeleteMissingSetReportsInsteadOfFailing(t *testing.T) {
	h, _ := newServer(t)
	w := post(t, h, base+"/sets/999/delete", nil, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `class="alert"`) {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
}

// Without htmx the browser performs a normal form post, so it must be redirected
// rather than shown a bare fragment.
func TestNonHtmxPostRedirectsToTheAppRoot(t *testing.T) {
	h, _ := newServer(t)
	w := post(t, h, base+"/sets", setForm("squat", "100", "5", "8"), false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if got := w.Header().Get("Location"); got != base+"/" {
		t.Fatalf("Location = %q, want %q", got, base+"/")
	}
}

func TestQuickPickChipsExposeTheLastValuesOfEachExercise(t *testing.T) {
	h, _ := newServer(t)
	post(t, h, base+"/sets", setForm("squat", "100", "5", "8"), true)
	post(t, h, base+"/sets", setForm("squat", "110", "3", "9"), true)

	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, `data-weight="110"`) || !strings.Contains(body, `data-reps="3"`) {
		t.Fatalf("chip should carry the latest values, got %q", body)
	}
	if strings.Count(body, `class="chip"`) != 1 {
		t.Fatalf("expected one chip per exercise, got %d", strings.Count(body, `class="chip"`))
	}
}

func TestHistoryAndSessionPages(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("deadlift", "120", "5", "7"), true)
	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})

	history := get(t, h, base+"/history")
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "SI 0.8") {
		t.Fatalf("history status = %d, body = %q", history.Code, history.Body.String())
	}

	detail := get(t, h, base+"/s/"+itoa(sessions[0].ID))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "deadlift") {
		t.Fatalf("detail status = %d", detail.Code)
	}
	// A past session is read-only: no delete controls.
	if strings.Contains(detail.Body.String(), `class="del"`) {
		t.Fatalf("read-only session must not expose delete buttons")
	}

	if got := get(t, h, base+"/s/4242").Code; got != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", got)
	}
}

func TestManifestAndServiceWorkerUseTheBasePath(t *testing.T) {
	h, _ := newServer(t)

	manifest := get(t, h, base+"/manifest.webmanifest")
	if manifest.Code != http.StatusOK {
		t.Fatalf("manifest status = %d", manifest.Code)
	}
	if ct := manifest.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Fatalf("manifest content type = %q", ct)
	}
	for _, want := range []string{`"start_url": "` + base + `/"`, `"scope": "` + base + `/"`} {
		if !strings.Contains(manifest.Body.String(), want) {
			t.Fatalf("manifest missing %q, got %s", want, manifest.Body.String())
		}
	}

	sw := get(t, h, base+"/sw.js")
	if sw.Code != http.StatusOK || !strings.Contains(sw.Body.String(), "const BASE = '"+base+"'") {
		t.Fatalf("sw status = %d, body = %q", sw.Code, sw.Body.String())
	}
	if cc := sw.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("service worker Cache-Control = %q, want no-cache", cc)
	}
}

func TestStaticAssetsAreServedAndCached(t *testing.T) {
	h, _ := newServer(t)
	for _, name := range []string{"app.css", "app.js", "htmx.min.js", "icon-192.png"} {
		w := get(t, h, base+"/static/"+name)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("%s status = %d, len = %d", name, w.Code, w.Body.Len())
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Fatalf("%s Cache-Control = %q", name, cc)
		}
	}
}

func TestBarePrefixRedirectsToAppRoot(t *testing.T) {
	h, _ := newServer(t)
	w := get(t, h, base)
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != base+"/" {
		t.Fatalf("status = %d, location = %q", w.Code, w.Header().Get("Location"))
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestPanelGroupsSetsByExerciseAndBadgesRecords(t *testing.T) {
	h, service := newServer(t)
	// An older, heavier session establishes the record to beat.
	if _, err := service.StartSession(t.Context(), "2026-08-01"); err != nil {
		t.Fatal(err)
	}
	old, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	if _, _, err := service.AddSet(t.Context(), training.AddSetInput{
		SessionID: old[0].ID, Exercise: "banca", WeightKG: 100, Reps: 5, RPE: 9,
	}); err != nil {
		t.Fatal(err)
	}

	post(t, h, base+"/sets", setForm("banca", "80", "5", "8"), true)
	post(t, h, base+"/sets", setForm("banca", "80", "6", "8"), true)
	post(t, h, base+"/sets", setForm("remo", "60", "8", "8"), true)

	body := get(t, h, base+"/").Body.String()
	// Two exercises means two blocks, not six flat rows.
	if n := strings.Count(body, `class="block"`); n != 2 {
		t.Fatalf("expected 2 exercise blocks, got %d", n)
	}
	if !strings.Contains(body, "2 ejercicios") {
		t.Fatalf("panel should report the exercise count: %q", body)
	}
	// Today's banca sets are lighter than the standing record, so that block
	// carries no badge. remo, done for the first time ever, is its own record.
	if strings.Contains(blockFor(t, body, "banca"), `class="pr"`) {
		t.Fatalf("no banca set today beats the record, should not be badged")
	}
	if !strings.Contains(blockFor(t, body, "remo"), `class="pr"`) {
		t.Fatalf("a first-ever set is by definition the record and should be badged")
	}

	// Beat it: 105x5 has a higher estimated 1RM than 100x5.
	post(t, h, base+"/sets", setForm("banca", "105", "5", "9"), true)
	body = get(t, h, base+"/").Body.String()
	if !strings.Contains(blockFor(t, body, "banca"), `class="pr"`) {
		t.Fatalf("a set matching the all-time best must be badged: %q", body)
	}
}

// blockFor returns just the exercise block for one exercise, so assertions do
// not accidentally match a badge belonging to a different exercise.
func blockFor(t *testing.T, body, exercise string) string {
	t.Helper()
	marker := `<span class="ex">` + exercise + `</span>`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no block for %q", exercise)
	}
	rest := body[i:]
	if end := strings.Index(rest, "</article>"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestEntryFormExposesPreviousPerformanceAndRecord(t *testing.T) {
	h, service := newServer(t)
	if _, err := service.StartSession(t.Context(), "2026-08-01"); err != nil {
		t.Fatal(err)
	}
	old, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	if _, _, err := service.AddSet(t.Context(), training.AddSetInput{
		SessionID: old[0].ID, Exercise: "banca", WeightKG: 100, Reps: 5, RPE: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetExerciseGroup(t.Context(), "banca", "pecho"); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, base+"/").Body.String()
	start := strings.Index(body, `id="exercise-info"`)
	if start < 0 {
		t.Fatalf("page must embed the exercise info map")
	}
	blob := body[start:]
	for _, want := range []string{`"banca"`, `"best_e1rm":116.7`, `"group":"pecho"`, `"date":"2026-08-01"`} {
		if !strings.Contains(blob, want) {
			t.Fatalf("exercise info missing %q", want)
		}
	}
}

func TestPreviousSkipsTodaySoItShowsTheSessionBefore(t *testing.T) {
	h, _ := newServer(t)
	post(t, h, base+"/sets", setForm("banca", "80", "5", "8"), true)

	body := get(t, h, base+"/").Body.String()
	start := strings.Index(body, `id="exercise-info"`)
	blob := body[start : strings.Index(body[start:], "</script>")+start]
	// The only set is today's, so there is no previous session to show.
	if strings.Contains(blob, `"last"`) {
		t.Fatalf("today's own set must not be offered as 'last time': %q", blob)
	}
	if !strings.Contains(blob, `"best_e1rm"`) {
		t.Fatalf("the record should still be present: %q", blob)
	}
}

func TestUpdateSetEditsInPlace(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("banca", "80", "5", "8"), true)
	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	session, _ := service.GetSession(t.Context(), sessions[0].ID)
	id := itoa(session.Sets[0].ID)

	w := post(t, h, base+"/sets/"+id+"/update", url.Values{
		"weight_kg": {"85"}, "reps": {"6"}, "rpe": {"9"},
	}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "85 kg × 6 @9") {
		t.Fatalf("panel should show the edited values: %q", w.Body.String())
	}
	// RPE 9 -> SI 1.2, so the session total follows the edit.
	if !strings.Contains(w.Body.String(), "SI 1.2") {
		t.Fatalf("total should be recalculated: %q", w.Body.String())
	}

	bad := post(t, h, base+"/sets/"+id+"/update", url.Values{"rpe": {"99"}}, true)
	if !strings.Contains(bad.Body.String(), `class="alert"`) {
		t.Fatalf("out-of-range RPE should be rejected inline")
	}
}

func TestHistoryShowsMuscleGroupVolume(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("banca", "80", "5", "8"), true)
	if err := service.SetExerciseGroup(t.Context(), "banca", "pecho"); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, base+"/history").Body.String()
	if !strings.Contains(body, `class="volume"`) || !strings.Contains(body, "pecho") {
		t.Fatalf("history should chart volume per muscle group: %q", body)
	}
}
