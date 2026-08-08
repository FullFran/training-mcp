package websrv_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

func TestPlanPickerAppearsAndStartsAPlannedSession(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{
		Name: "Empuje A",
		Items: []training.PlanItem{
			{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9},
			{Exercise: "laterales polea", TargetSets: 4, RepMin: 12, RepMax: 15, TargetRPE: 9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, "Empuje A · 7 series") {
		t.Fatalf("plan picker should list the plan with its set count: %q", body)
	}

	w := post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	body = get(t, h, base+"/").Body.String()
	for _, want := range []string{"Empuje A", "0/2 ejercicios", "8-10 reps @9", `class="planned"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("planned checklist missing %q", want)
		}
	}

	// Logging planned sets advances the checklist and ticks it off.
	for range 3 {
		post(t, h, base+"/sets", setForm("banca", "80", "9", "9"), true)
	}
	body = get(t, h, base+"/").Body.String()
	if !strings.Contains(body, "1/2 ejercicios") {
		t.Fatalf("checklist should show one exercise complete: %q", body)
	}
	if !strings.Contains(body, `class="ok"`) {
		t.Fatalf("completed plan item should be marked done")
	}
}

func TestOffPlanWorkIsShownRatherThanHidden(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{
		Name:  "Empuje A",
		Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	post(t, h, base+"/sets", setForm("curl bayesian", "12", "12", "8"), true)

	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, "curl bayesian") || !strings.Contains(body, "fuera de plan") {
		t.Fatalf("off-plan exercise should be listed as such: %q", body)
	}
}

// Switching plans after work is logged would silently reinterpret it, so the
// session is left alone once it has sets.
func TestChoosingAPlanIsRefusedOnceSetsExist(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{
		Name:  "Empuje A",
		Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/sets", setForm("sentadilla", "100", "5", "8"), true)
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)

	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 10})
	if len(sessions) != 1 {
		t.Fatalf("expected exactly one session, got %#v", sessions)
	}
	if sessions[0].PlanName != "" || sessions[0].SetCount != 1 {
		t.Fatalf("existing work must be left alone: %#v", sessions[0])
	}
}

func startPlan(t *testing.T, h http.Handler, service *training.Service, items ...training.PlanItem) training.Plan {
	t.Helper()
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "Empuje A", Items: items})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	return plan
}

func TestSessionPlanCanBeAdjustedWithoutTouchingTheTemplate(t *testing.T) {
	h, service := newServer(t)
	plan := startPlan(t, h, service,
		training.PlanItem{Exercise: "banca", TargetSets: 4, RepMin: 8, RepMax: 10, TargetRPE: 9})

	// Fewer sets today because the session is not going well.
	post(t, h, base+"/plan/item", url.Values{
		"action": {"sets"}, "delta": {"-1"}, "current": {"4"}, "exercise": {"banca"},
	}, true)
	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, `<span class="count">0/3`) {
		t.Fatalf("target should have dropped to 3: %q", body)
	}

	// The saved plan is untouched: today's change is today's only.
	saved, err := service.GetPlan(t.Context(), plan.ID)
	if err != nil || saved.Items[0].TargetSets != 4 {
		t.Fatalf("template was modified by an in-session change: %#v, %v", saved, err)
	}
}

func TestSessionExerciseCanBeSkippedAndResumed(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service, training.PlanItem{Exercise: "banca", TargetSets: 3})

	post(t, h, base+"/plan/item", url.Values{"action": {"skip"}, "exercise": {"banca"}}, true)
	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, "skipped") {
		t.Fatalf("skipped exercise should be marked: %q", body)
	}
	post(t, h, base+"/plan/item", url.Values{"action": {"unskip"}, "exercise": {"banca"}}, true)
	if strings.Contains(get(t, h, base+"/").Body.String(), "skipped") {
		t.Fatalf("exercise should be resumable")
	}
}

// The occupied-machine case: substitute the movement, keep the prescription.
func TestSessionExerciseCanBeSwappedKeepingItsPrescription(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service,
		training.PlanItem{Exercise: "banca máquina", TargetSets: 4, RepMin: 8, RepMax: 12, TargetRPE: 9})

	post(t, h, base+"/plan/item", url.Values{
		"action": {"swap"}, "exercise": {"banca máquina"}, "replacement": {" Banca Libre "},
	}, true)

	body := get(t, h, base+"/").Body.String()
	if strings.Contains(body, "banca máquina") {
		t.Fatalf("swapped-out exercise should be gone: %q", body)
	}
	// Normalized like any other exercise name, and the prescription survives.
	if !strings.Contains(body, "banca libre") || !strings.Contains(body, "8-12 reps @9") {
		t.Fatalf("replacement should keep the prescription: %q", body)
	}
}

func TestOffPlanExerciseCanBeAdoptedIntoTodaysPlan(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service, training.PlanItem{Exercise: "banca", TargetSets: 3})
	post(t, h, base+"/sets", setForm("curl bayesian", "12", "12", "8"), true)
	post(t, h, base+"/sets", setForm("curl bayesian", "12", "10", "9"), true)

	post(t, h, base+"/plan/item", url.Values{
		"action": {"adopt"}, "done": {"2"}, "exercise": {"curl bayesian"},
	}, true)

	body := get(t, h, base+"/").Body.String()
	if strings.Contains(body, "fuera de plan") {
		t.Fatalf("adopted exercise should no longer read as off-plan: %q", body)
	}
	if !strings.Contains(body, `<span class="count">2/2`) {
		t.Fatalf("adopted at the volume already performed: %q", body)
	}
}

func TestRemovingAPlannedExerciseKeepsItsLoggedSets(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service, training.PlanItem{Exercise: "banca", TargetSets: 3})
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)

	post(t, h, base+"/plan/item", url.Values{"action": {"remove"}, "exercise": {"banca"}}, true)

	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	if sessions[0].SetCount != 1 {
		t.Fatalf("logged sets must survive removal from the plan: %#v", sessions[0])
	}
	// It reappears as off-plan work rather than vanishing.
	if !strings.Contains(get(t, h, base+"/").Body.String(), "fuera de plan") {
		t.Fatalf("removed exercise with logged sets should show as off-plan")
	}
}

// Adopting another plan mid-session is safe now that the plan is a per-session
// snapshot: its exercises are added and no logged set is touched.
func TestAdoptingAnotherPlanMidSessionKeepsLoggedWork(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service, training.PlanItem{Exercise: "banca", TargetSets: 3})
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)

	other, err := service.CreatePlan(t.Context(), training.Plan{
		Name:  "Tirón A",
		Items: []training.PlanItem{{Exercise: "remo máquina", TargetSets: 4, RepMin: 8, RepMax: 12}},
	})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(other.ID)}}, false)

	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, "remo máquina") || !strings.Contains(body, "banca") {
		t.Fatalf("both the new plan and prior work should be present: %q", body)
	}
	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 10})
	if len(sessions) != 1 || sessions[0].SetCount != 1 {
		t.Fatalf("no session or set should be lost: %#v", sessions)
	}
}

func TestSaveSessionAsPlanCapturesTheAdjustedSession(t *testing.T) {
	h, service := newServer(t)
	startPlan(t, h, service,
		training.PlanItem{Exercise: "banca", TargetSets: 4, RepMin: 8, RepMax: 10, TargetRPE: 9},
		training.PlanItem{Exercise: "aperturas", TargetSets: 3})
	post(t, h, base+"/plan/item", url.Values{"action": {"sets"}, "delta": {"-1"}, "current": {"4"}, "exercise": {"banca"}}, true)
	post(t, h, base+"/plan/item", url.Values{"action": {"skip"}, "exercise": {"aperturas"}}, true)

	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	saved, err := service.SaveSessionAsPlan(t.Context(), sessions[0].ID, "Empuje B")
	if err != nil {
		t.Fatalf("SaveSessionAsPlan() error = %v", err)
	}
	if len(saved.Items) != 1 || saved.Items[0].Exercise != "banca" || saved.Items[0].TargetSets != 3 {
		t.Fatalf("new plan should capture the adjustment and drop the skip: %#v", saved.Items)
	}
	if saved.Items[0].RepMin != 8 || saved.Items[0].TargetRPE != 9 {
		t.Fatalf("prescription should carry over: %#v", saved.Items[0])
	}
}

func TestFeedbackOnlyAsksAboutMusclesTrainedToday(t *testing.T) {
	h, service := newServer(t)
	if err := service.SetExerciseGroup(t.Context(), "banca", "pecho"); err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)

	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, `class="feedback"`) || !strings.Contains(body, "0/1") {
		t.Fatalf("feedback should offer exactly the trained groups: %q", body)
	}
	// Fifteen groups exist; only the trained one is asked about.
	if strings.Contains(body, "isquios") {
		t.Fatalf("untrained groups must not be asked about")
	}

	w := post(t, h, base+"/feedback", url.Values{
		"muscle_group": {"pecho"}, "fatigue": {"2"}, "pump": {"3"}, "recovery": {"1"},
	}, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "magnitud 6") {
		t.Fatalf("feedback not recorded: %d %q", w.Code, w.Body.String())
	}
	if !strings.Contains(get(t, h, base+"/").Body.String(), "1/1") {
		t.Fatalf("feedback count should advance")
	}
}

func TestHistoryShowsNextWeekSetRecommendation(t *testing.T) {
	h, service := newServer(t)
	if err := service.SetExerciseGroup(t.Context(), "banca", "pecho"); err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)
	// Magnitude 0 is the spreadsheet's "sube 3 series" anchor.
	post(t, h, base+"/feedback", url.Values{
		"muscle_group": {"pecho"}, "fatigue": {"0"}, "pump": {"0"}, "recovery": {"0"},
	}, true)

	body := get(t, h, base+"/history").Body.String()
	if !strings.Contains(body, `class="recommend"`) || !strings.Contains(body, "sube 3 series") {
		t.Fatalf("history should recommend next week's volume: %q", body)
	}
	if !strings.Contains(body, "+3") {
		t.Fatalf("delta should be shown: %q", body)
	}
}

func TestExerciseFieldOffersTheKnownCatalogue(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("remo máquina", "60", "10", "8"), true)
	if err := service.SetExerciseGroup(t.Context(), "jalón máquina", "espalda"); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, `list="catalogue"`) {
		t.Fatalf("exercise field should autocomplete")
	}
	// Both a logged exercise and one only present in the catalogue are offered,
	// which is what stops near-duplicate names being typed in.
	for _, want := range []string{`<option value="remo máquina">`, `<option value="jalón máquina">`} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalogue missing %q", want)
		}
	}
}

func TestSetCanCarryAnIntensityTechnique(t *testing.T) {
	h, _ := newServer(t)
	form := setForm("banca", "80", "8", "9")
	form.Set("technique", " Drop Set ")
	w := post(t, h, base+"/sets", form, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// Normalized like exercise names, so the vocabulary cannot fragment.
	if !strings.Contains(w.Body.String(), `class="tech-tag">drop set<`) {
		t.Fatalf("technique should be shown on the set: %q", w.Body.String())
	}
	if !strings.Contains(get(t, h, base+"/").Body.String(), `list="techniques"`) {
		t.Fatalf("entry form should suggest known techniques")
	}
}

// A drop set is not comparable to a straight set, so it must not become the
// exercise's record.
func TestTechniqueSetsDoNotBecomeTheRecord(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/sets", setForm("banca", "80", "5", "9"), true)
	heavy := setForm("banca", "120", "5", "9")
	heavy.Set("technique", "asistida")
	post(t, h, base+"/sets", heavy, true)

	history, err := service.ExerciseHistory(t.Context(), "banca", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Sets) != 2 {
		t.Fatalf("both sets should be in the history: %#v", history.Sets)
	}
	if history.Best == nil || history.Best.WeightKG != 80 {
		t.Fatalf("record should ignore the technique set: %#v", history.Best)
	}
}

func TestTechniqueCanBeEditedAndCleared(t *testing.T) {
	h, service := newServer(t)
	form := setForm("banca", "80", "8", "9")
	form.Set("technique", "drop set")
	post(t, h, base+"/sets", form, true)

	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	session, _ := service.GetSession(t.Context(), sessions[0].ID)
	id := itoa(session.Sets[0].ID)

	w := post(t, h, base+"/sets/"+id+"/update", url.Values{"technique": {"rest-pause"}}, true)
	if !strings.Contains(w.Body.String(), "rest-pause") {
		t.Fatalf("technique not updated: %q", w.Body.String())
	}
	// An empty value clears it back to a normal set.
	w = post(t, h, base+"/sets/"+id+"/update", url.Values{"technique": {""}}, true)
	if strings.Contains(w.Body.String(), "tech-tag") {
		t.Fatalf("technique should be cleared: %q", w.Body.String())
	}
}

func TestExportStreamsARestorableDatabase(t *testing.T) {
	h, _ := newServer(t)
	post(t, h, base+"/sets", setForm("banca", "80", "8", "9"), true)

	w := get(t, h, base+"/export.db")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.sqlite3" {
		t.Fatalf("content type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "training-2026-08-07.db") {
		t.Fatalf("filename should carry the date: %q", cd)
	}
	body := w.Body.Bytes()
	// A real SQLite file, not a rendered page: the format's magic header.
	if len(body) < 16 || string(body[:15]) != "SQLite format 3" {
		t.Fatalf("export is not a SQLite database, got %d bytes", len(body))
	}

	// The snapshot must be openable and contain the data, or it is not a backup.
	path := t.TempDir() + "/restored.db"
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("restored copy does not open: %v", err)
	}
	defer restored.Close()
	sessions, err := restored.ListSessions(t.Context(), training.ListFilter{Limit: 10})
	if err != nil || len(sessions) != 1 || sessions[0].SetCount != 1 {
		t.Fatalf("restored data = %#v, %v", sessions, err)
	}
}

func TestSupersetIsShownAndStampedOntoLoggedSets(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{
		Name: "Tirón A",
		Items: []training.PlanItem{
			{Exercise: "pull over", TargetSets: 3, Superset: "A"},
			{Exercise: "remo normal", TargetSets: 3, Superset: "A"},
			{Exercise: "curl", TargetSets: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)

	body := get(t, h, base+"/").Body.String()
	if strings.Count(body, `data-superset="A"`) != 2 {
		t.Fatalf("both members of the round should carry the label: %q", body)
	}
	if !strings.Contains(body, `class="ss">A<`) {
		t.Fatalf("superset label should be visible: %q", body)
	}

	// Logging never states the round; it is derived from the session's plan.
	post(t, h, base+"/sets", setForm("pull over", "40", "10", "8"), true)
	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	session, _ := service.GetSession(t.Context(), sessions[0].ID)
	if session.Sets[0].Superset != "A" {
		t.Fatalf("set should be stamped with its round: %#v", session.Sets[0])
	}

	// An exercise outside any round stays unstamped.
	post(t, h, base+"/sets", setForm("curl", "10", "12", "8"), true)
	session, _ = service.GetSession(t.Context(), sessions[0].ID)
	if session.Sets[1].Superset != "" {
		t.Fatalf("standalone exercise must not be stamped: %#v", session.Sets[1])
	}
}

func TestExerciseSetupNoteIsStoredAndOffered(t *testing.T) {
	h, _ := newServer(t)
	post(t, h, base+"/sets", setForm("prensa", "100", "10", "8"), true)

	w := post(t, h, base+"/note", url.Values{
		"exercise": {"prensa"}, "note": {"asiento en 4, pies altos"},
	}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, "asiento en 4, pies altos") {
		t.Fatalf("note should be offered with the exercise: %q", body)
	}
	// Empty clears it.
	post(t, h, base+"/note", url.Values{"exercise": {"prensa"}, "note": {""}}, true)
	if strings.Contains(get(t, h, base+"/").Body.String(), "asiento en 4") {
		t.Fatalf("empty note should remove it")
	}
}

// The page preloads only the exercises in play; anything else must still be
// answerable, or picking a rarely used exercise shows nothing useful.
func TestExerciseInfoIsAvailableOnDemandForAnyExercise(t *testing.T) {
	h, service := newServer(t)
	if _, err := service.StartSession(t.Context(), "2026-08-01"); err != nil {
		t.Fatal(err)
	}
	old, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	if _, _, err := service.AddSet(t.Context(), training.AddSetInput{
		SessionID: old[0].ID, Exercise: "prensa", WeightKG: 100, Reps: 10, RPE: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetExerciseNote(t.Context(), "prensa", "asiento en 4"); err != nil {
		t.Fatal(err)
	}
	// Push prensa out of the recent window so it is not preloaded.
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		post(t, h, base+"/sets", setForm(name, "20", "10", "8"), true)
	}
	if strings.Contains(get(t, h, base+"/").Body.String(), "asiento en 4") {
		t.Fatalf("precondition: prensa should not be preloaded")
	}

	w := get(t, h, base+"/exercise-info?name=%20Prensa%20")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`"note":"asiento en 4"`, `"best_e1rm":133.3`, `"date":"2026-08-01"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("on-demand info missing %q, got %s", want, body)
		}
	}
	if got := get(t, h, base+"/exercise-info?name=").Body.String(); strings.TrimSpace(got) != "{}" {
		t.Fatalf("empty name should answer empty, got %q", got)
	}
}

func TestRoutineAndExerciseNotesTravelIntoTheSession(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{
		Name:  "Empuje A",
		Notes: "Semana 3 del bloque: acumulación, no llegar al fallo salvo la última serie.",
		Items: []training.PlanItem{
			{Exercise: "banca", TargetSets: 3, Notes: "excéntrica 3s, pausa en el pecho"},
			{Exercise: "laterales", TargetSets: 4},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)

	body := get(t, h, base+"/").Body.String()
	if !strings.Contains(body, "acumulación, no llegar al fallo") {
		t.Fatalf("routine notes should be shown: %q", body)
	}
	if !strings.Contains(body, "excéntrica 3s, pausa en el pecho") {
		t.Fatalf("per-exercise notes should be shown: %q", body)
	}

	// They must also be readable from the session, not only from the template.
	sessions, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	progress, err := service.SessionProgress(t.Context(), sessions[0].ID)
	if err != nil || len(progress) != 2 {
		t.Fatalf("progress = %#v, %v", progress, err)
	}
	if progress[0].Notes != "excéntrica 3s, pausa en el pecho" {
		t.Fatalf("session item lost its note: %#v", progress[0])
	}
	if progress[1].Notes != "" {
		t.Fatalf("an item without a note should carry none: %#v", progress[1])
	}
}

func TestLoadSuggestionIsOfferedWithItsReasoning(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{
		Name:  "Empuje A",
		Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 12, TargetRPE: 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A previous session at the top of the range, comfortably under target RPE.
	if _, err := service.StartPlannedSession(t.Context(), "2026-08-01", plan.ID); err != nil {
		t.Fatal(err)
	}
	old, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	if _, _, err := service.AddSet(t.Context(), training.AddSetInput{
		SessionID: old[0].ID, Exercise: "banca", WeightKG: 80, Reps: 12, RPE: 8,
	}); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, base+"/exercise-info?name=banca").Body.String()
	if !strings.Contains(body, `"suggested_kg":82.5`) || !strings.Contains(body, `"delta_kg":2.5`) {
		t.Fatalf("should advise adding weight: %s", body)
	}
	if !strings.Contains(body, "sube el peso") {
		t.Fatalf("advice must carry its reasoning: %s", body)
	}
}

// Holding is the default and needs no prompt; only a change is worth showing.
func TestNoLoadSuggestionWhenTheWeightIsRight(t *testing.T) {
	h, service := newServer(t)
	plan, _ := service.CreatePlan(t.Context(), training.Plan{
		Name:  "Empuje A",
		Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 12, TargetRPE: 9}},
	})
	if _, err := service.StartPlannedSession(t.Context(), "2026-08-01", plan.ID); err != nil {
		t.Fatal(err)
	}
	old, _ := service.ListSessions(t.Context(), training.ListFilter{Limit: 1})
	if _, _, err := service.AddSet(t.Context(), training.AddSetInput{
		SessionID: old[0].ID, Exercise: "banca", WeightKG: 80, Reps: 10, RPE: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get(t, h, base+"/exercise-info?name=banca").Body.String(), `"suggest"`) {
		t.Fatalf("a correct weight should not prompt a change")
	}
}

func TestPlansPageCreatesEditsReordersAndRemoves(t *testing.T) {
	h, service := newServer(t)

	// A new routine starts as a placeholder so it can be filled in on the page.
	post(t, h, base+"/plans/create", url.Values{"name": {"Empuje A"}}, true)
	plans, err := service.ListPlans(t.Context())
	if err != nil || len(plans) != 1 || plans[0].Name != "Empuje A" {
		t.Fatalf("plans = %#v, %v", plans, err)
	}
	id := plans[0].ID

	for _, ex := range []string{"banca", "aperturas", "laterales"} {
		post(t, h, base+"/plans/item", url.Values{
			"plan_id": {itoa(id)}, "exercise": {ex}, "target_sets": {"3"},
			"rep_min": {"8"}, "rep_max": {"12"}, "target_rpe": {"9"},
		}, true)
	}
	post(t, h, base+"/plans/item/remove", url.Values{"plan_id": {itoa(id)}, "exercise": {"por definir"}}, true)

	plan, err := service.GetPlan(t.Context(), id)
	if err != nil || len(plan.Items) != 3 {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	if plan.Items[0].Exercise != "banca" || plan.Items[2].Exercise != "laterales" {
		t.Fatalf("order = %#v", plan.Items)
	}

	// Order is the order the session is performed in, so it must be editable.
	post(t, h, base+"/plans/move", url.Values{
		"plan_id": {itoa(id)}, "exercise": {"laterales"}, "direction": {"up"},
	}, true)
	plan, _ = service.GetPlan(t.Context(), id)
	if plan.Items[1].Exercise != "laterales" || plan.Items[2].Exercise != "aperturas" {
		t.Fatalf("move up did not swap: %#v", plan.Items)
	}

	// Re-adding an exercise replaces its prescription instead of duplicating.
	post(t, h, base+"/plans/item", url.Values{
		"plan_id": {itoa(id)}, "exercise": {"banca"}, "target_sets": {"5"}, "superset": {"a"},
	}, true)
	plan, _ = service.GetPlan(t.Context(), id)
	if len(plan.Items) != 3 || plan.Items[0].TargetSets != 5 {
		t.Fatalf("re-add should replace: %#v", plan.Items)
	}
	// Round labels read as uppercase wherever they are shown.
	if plan.Items[0].Superset != "A" {
		t.Fatalf("superset = %q, want uppercase", plan.Items[0].Superset)
	}

	post(t, h, base+"/plans/update", url.Values{
		"plan_id": {itoa(id)}, "name": {"Empuje B"}, "notes": {"bloque de acumulación"},
	}, true)
	plan, _ = service.GetPlan(t.Context(), id)
	if plan.Name != "Empuje B" || plan.Notes != "bloque de acumulación" || len(plan.Items) != 3 {
		t.Fatalf("update touched the exercises: %#v", plan)
	}

	body := get(t, h, base+"/plans").Body.String()
	for _, want := range []string{"Empuje B", "bloque de acumulación", "banca", `list="catalogue"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("plans page missing %q", want)
		}
	}

	post(t, h, base+"/plans/delete", url.Values{"plan_id": {itoa(id)}}, true)
	if plans, _ := service.ListPlans(t.Context()); len(plans) != 0 {
		t.Fatalf("plan should be gone: %#v", plans)
	}
}

func TestPlanEditsReportInvalidInputInline(t *testing.T) {
	h, service := newServer(t)
	post(t, h, base+"/plans/create", url.Values{"name": {"Empuje A"}}, true)
	plans, _ := service.ListPlans(t.Context())

	w := post(t, h, base+"/plans/item", url.Values{
		"plan_id": {itoa(plans[0].ID)}, "exercise": {"banca"},
		"target_sets": {"3"}, "rep_min": {"12"}, "rep_max": {"8"},
	}, true)
	if !strings.Contains(w.Body.String(), `class="alert"`) {
		t.Fatalf("inverted rep range should be reported: %q", w.Body.String())
	}
	w = post(t, h, base+"/plans/item/remove", url.Values{
		"plan_id": {itoa(plans[0].ID)}, "exercise": {"no existe"},
	}, true)
	if !strings.Contains(w.Body.String(), `class="alert"`) {
		t.Fatalf("removing a missing exercise should be reported")
	}
}
