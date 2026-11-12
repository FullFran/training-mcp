package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// The test clock's today is 2026-08-07.

func onlySession(t *testing.T, svc *training.Service) training.SessionSummary {
	t.Helper()
	sessions, err := svc.ListSessions(context.Background(), training.ListFilter{Limit: 10})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	return sessions[0]
}

// Progreso offers a past session, and creating one opens it on its own page
// ready for sets, through the same call start_session makes.
func TestAPastSessionCanBeCreatedFromTheWeb(t *testing.T) {
	h, svc := newServer(t)
	plan, err := svc.CreatePlan(context.Background(), training.Plan{Name: "torso", Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}

	form := get(t, h, base+"/sessions/past").Body.String()
	for _, want := range []string{`action="` + base + `/sessions/past"`, `type="date"`, `max="2026-08-06"`, ">torso</option>"} {
		if !strings.Contains(form, want) {
			t.Fatalf("past session form lacks %q:\n%s", want, form)
		}
	}

	w := post(t, h, base+"/sessions/past", url.Values{"date": {"2026-08-01"}, "plan_id": {strconv.FormatInt(plan.ID, 10)}}, false)
	got := onlySession(t, svc)
	if got.Date != "2026-08-01" || got.PlanName != "torso" {
		t.Fatalf("session = %+v", got)
	}
	if want := base + "/s/" + strconv.FormatInt(got.ID, 10) + "?editar=1"; w.Code != http.StatusSeeOther || w.Header().Get("Location") != want {
		t.Fatalf("status %d, location %q, want %q", w.Code, w.Header().Get("Location"), want)
	}
	page := get(t, h, w.Header().Get("Location")).Body.String()
	if !strings.Contains(page, `action="`+base+`/s/`+strconv.FormatInt(got.ID, 10)+`/sets"`) {
		t.Fatalf("the new session's page offers no set entry:\n%s", page)
	}
}

// Progreso's history links to the past session form.
func TestProgresoLinksToAddingAPastSession(t *testing.T) {
	h, svc := newServer(t)
	if _, err := svc.StartSession(context.Background(), "2026-08-05"); err != nil {
		t.Fatal(err)
	}
	if body := get(t, h, base+"/progress").Body.String(); !strings.Contains(body, `href="`+base+`/sessions/past"`) || !strings.Contains(body, "Añadir sesión pasada") {
		t.Fatalf("Progreso has no past session link:\n%s", body)
	}
}

// A date after today is refused, and so is today, which is the live workout's
// day: a past session must never become it.
func TestAPastSessionRefusesTodayAndFutureDates(t *testing.T) {
	h, svc := newServer(t)
	for date, message := range map[string]string{
		"2026-08-08": "No puedes registrar una sesión en una fecha futura.",
		"2026-08-07": "Para hoy, registra el entreno desde Entrenar.",
		"ayer":       "Elige una fecha válida.",
	} {
		w := post(t, h, base+"/sessions/past", url.Values{"date": {date}}, false)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), message) {
			t.Fatalf("%s: status %d\n%s", date, w.Code, w.Body)
		}
	}
	if sessions, err := svc.ListSessions(context.Background(), training.ListFilter{Limit: 10}); err != nil || len(sessions) != 0 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
}

// Sets logged on a past session's page land on that date, can be corrected and
// deleted there, and never open today's workout.
func TestSetsLoggedIntoAPastSessionLandOnItsDate(t *testing.T) {
	h, svc := newServer(t)
	post(t, h, base+"/sessions/past", url.Values{"date": {"2026-08-01"}}, false)
	session := onlySession(t, svc)
	id := strconv.FormatInt(session.ID, 10)

	w := post(t, h, base+"/s/"+id+"/sets", url.Values{"exercise": {" Sentadilla "}, "weight_kg": {"100"}, "reps": {"5"}, "rpe": {"8"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/s/"+id+"?editar=1" {
		t.Fatalf("status %d, location %q\n%s", w.Code, w.Header().Get("Location"), w.Body)
	}
	hist, err := svc.ExerciseHistory(context.Background(), "sentadilla", 10)
	if err != nil || len(hist.Sets) != 1 || hist.Sets[0].Date != "2026-08-01" || hist.Sets[0].WeightKG != 100 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	stored, err := svc.GetSession(context.Background(), session.ID)
	if err != nil || len(stored.Sets) != 1 {
		t.Fatalf("session sets = %+v, %v", stored.Sets, err)
	}
	setID := strconv.FormatInt(stored.Sets[0].ID, 10)

	if w := post(t, h, base+"/s/"+id+"/sets/"+setID+"/update", url.Values{"weight_kg": {"105"}, "reps": {"4"}, "rpe": {"9"}, "category": {"working"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("update status %d\n%s", w.Code, w.Body)
	}
	got, err := svc.GetSession(context.Background(), session.ID)
	if err != nil || len(got.Sets) != 1 || got.Sets[0].WeightKG != 105 || got.Sets[0].Reps != 4 {
		t.Fatalf("after update = %+v, %v", got.Sets, err)
	}

	live, err := svc.LiveWorkout(context.Background())
	if err != nil || live.Session != nil {
		t.Fatalf("a past session became today's workout: %+v, %v", live.Session, err)
	}
	if home := get(t, h, base+"/").Body.String(); strings.Contains(home, "sentadilla") && strings.Contains(home, `data-active="1"`) {
		t.Fatalf("home shows the past session as active:\n%s", home)
	}

	if w := post(t, h, base+"/s/"+id+"/sets/"+setID+"/delete", url.Values{}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("delete status %d\n%s", w.Code, w.Body)
	}
	if got, err = svc.GetSession(context.Background(), session.ID); err != nil || len(got.Sets) != 0 {
		t.Fatalf("after delete = %+v, %v", got.Sets, err)
	}
}

// A bad set is refused on the session's page with the reason, and nothing is stored.
func TestABadSetOnAPastSessionIsRefusedInline(t *testing.T) {
	h, svc := newServer(t)
	session, err := svc.StartSession(context.Background(), "2026-08-01")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(session.ID, 10)
	w := post(t, h, base+"/s/"+id+"/sets", url.Values{"exercise": {"banca"}, "weight_kg": {"60"}, "reps": {"8"}, "rpe": {"11"}}, false)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Datos inválidos") {
		t.Fatalf("status %d\n%s", w.Code, w.Body)
	}
	if got, err := svc.GetSession(context.Background(), session.ID); err != nil || len(got.Sets) != 0 {
		t.Fatalf("sets = %+v, %v", got.Sets, err)
	}
}

// A set from another session cannot be edited through this session's URL.
func TestASetIsOnlyEditableThroughItsOwnSession(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	a, _ := svc.StartSession(ctx, "2026-08-01")
	b, _ := svc.StartSession(ctx, "2026-08-02")
	// A has sets of its own, so only the set's id can tell them apart.
	if _, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: a.ID, Exercise: "remo", WeightKG: 50, Reps: 10, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	set, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: b.ID, Exercise: "banca", WeightKG: 60, Reps: 8, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	path := base + "/s/" + strconv.FormatInt(a.ID, 10) + "/sets/" + strconv.FormatInt(set.ID, 10)
	if w := post(t, h, path+"/update", url.Values{"weight_kg": {"1"}}, false); w.Code != http.StatusNotFound {
		t.Fatalf("update status %d", w.Code)
	}
	if w := post(t, h, path+"/delete", url.Values{}, false); w.Code != http.StatusNotFound {
		t.Fatalf("delete status %d", w.Code)
	}
	if got, _ := svc.GetSession(ctx, b.ID); len(got.Sets) != 1 || got.Sets[0].WeightKG != 60 {
		t.Fatalf("set changed: %+v", got.Sets)
	}
}

// Behind the web login every new form is a plain form, so each must carry the
// session's CSRF token itself or the middleware refuses it.
func TestThePastSessionFormsCarryTheCSRFToken(t *testing.T) {
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	services := training.NewServices(store, webClock)
	svc := services.ForUser(1)
	server, err := websrv.NewWithOptions(func(context.Context) *training.Service { return svc }, services.Snapshot, webClock, "",
		websrv.Options{CSRFToken: func(*http.Request) string { return "tok-past" }})
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.StartSession(context.Background(), "2026-08-05")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.AddSet(context.Background(), training.AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 60, Reps: 8, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	const field = `<input type="hidden" name="csrf_token" value="tok-past">`
	pages := map[string]string{"/sessions/past": "/sessions/past", "/s/" + strconv.FormatInt(session.ID, 10) + "?editar=1": "/s/"}
	for page, action := range pages {
		body := get(t, server.Handler(), page).Body.String()
		forms := strings.Split(body, "<form")[1:]
		seen := 0
		for _, form := range forms {
			form = form[:strings.Index(form, "</form>")]
			if !strings.Contains(form, `action="`+action) {
				continue
			}
			seen++
			if !strings.Contains(form, field) {
				t.Fatalf("%s: form lacks the CSRF field:\n%s", page, form)
			}
		}
		if seen == 0 {
			t.Fatalf("%s has no form posting to %s:\n%s", page, action, body)
		}
	}
}

// The web "Añadir ejercicio" sends the superset and notes add_session_exercise accepts.
func TestAddingAnExerciseTakesSupersetAndNotes(t *testing.T) {
	h, svc := newServer(t)
	body := post(t, h, base+"/plan/item", url.Values{
		"action": {"add"}, "exercise": {"curl"}, "target_sets": {"3"},
		"superset": {" b "}, "notes": {" lento en la bajada "},
	}, true).Body.String()
	live, err := svc.LiveWorkout(context.Background())
	if err != nil || live.Session == nil {
		t.Fatalf("live = %+v, %v\n%s", live, err, body)
	}
	progress, err := svc.SessionProgress(context.Background(), live.Session.ID)
	if err != nil || len(progress) != 1 || progress[0].Superset != "B" || progress[0].Notes != "lento en la bajada" {
		t.Fatalf("progress = %+v, %v", progress, err)
	}

	home := get(t, h, base+"/").Body.String()
	// With JavaScript «+ Añadir ejercicio» opens the picker, so it asks too.
	if !strings.Contains(home, `id="picker-superset"`) || !strings.Contains(home, `id="picker-notes"`) {
		t.Fatalf("the exercise picker lacks superset or notes")
	}
	start := strings.Index(home, `<details class="add-item card">`)
	if start < 0 {
		t.Fatalf("no add-exercise form:\n%s", home)
	}
	form := home[start:]
	form = form[:strings.Index(form, "</form>")]
	if !strings.Contains(form, `name="superset"`) || !strings.Contains(form, `name="notes"`) {
		t.Fatalf("add-exercise form lacks superset or notes:\n%s", form)
	}
}
