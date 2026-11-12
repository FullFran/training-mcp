package websrv_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// cancelWorld serves the app on a database the test can also read directly, so
// it can compare every row before and after a refused cancel.
type cancelWorld struct {
	h    http.Handler
	svc  *training.Service
	db   *sql.DB
	push training.Plan // started in the tests
	pull training.Plan // the other routine, offered again after cancelling
}

func newCancelWorld(t *testing.T, csrf string) cancelWorld {
	t.Helper()
	path := t.TempDir() + "/training.db"
	store, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	services := training.NewServices(store, webClock)
	svc := services.ForUser(1)
	opts := websrv.Options{}
	if csrf != "" {
		opts.CSRFToken = func(*http.Request) string { return csrf }
	}
	server, err := websrv.NewWithOptions(func(context.Context) *training.Service { return svc }, services.Snapshot, webClock, base, opts)
	if err != nil {
		t.Fatal(err)
	}
	world := cancelWorld{h: server.Handler(), svc: svc, db: db}
	if world.push, err = svc.CreatePlan(context.Background(), training.Plan{Name: "Empuje A", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9},
		{Exercise: "laterales polea", TargetSets: 2, RepMin: 12, RepMax: 15, TargetRPE: 9},
	}}); err != nil {
		t.Fatal(err)
	}
	if world.pull, err = svc.CreatePlan(context.Background(), training.Plan{Name: "Tirón A", Items: []training.PlanItem{
		{Exercise: "remo", TargetSets: 3},
	}}); err != nil {
		t.Fatal(err)
	}
	return world
}

// start begins today's workout from Empuje A and returns its session ID.
func (w cancelWorld) start(t *testing.T) string {
	t.Helper()
	if res := post(t, w.h, base+"/plan", url.Values{"plan_id": {itoa(w.push.ID)}}, false); res.Code != http.StatusSeeOther {
		t.Fatalf("starting the plan: status %d", res.Code)
	}
	live, err := w.svc.LiveWorkout(context.Background())
	if err != nil || live.Session == nil {
		t.Fatalf("no workout today: %v", err)
	}
	return strconv.FormatInt(live.Session.ID, 10)
}

// cancelForm returns the workout's cancel form as rendered on the page.
func cancelForm(t *testing.T, body, id string) string {
	t.Helper()
	start := strings.Index(body, `action="`+base+`/s/`+id+`/cancel"`)
	if start < 0 {
		t.Fatalf("the workout offers no way to cancel:\n%s", body)
	}
	form := body[start:]
	return form[:strings.Index(form, "</form>")]
}

func TestCancellingAWorkoutWithNoSetsRemovesItAndOffersEveryRoutineAgain(t *testing.T) {
	world := newCancelWorld(t, "")
	id := world.start(t)

	body := get(t, world.h, base+"/?modo=entrenar").Body.String()
	form := cancelForm(t, body, id)
	if !strings.Contains(form, "Cancelar entreno") || strings.Contains(form, "Se borran") {
		t.Fatalf("with nothing logged the cancel needs no confirmation:\n%s", form)
	}

	res := post(t, world.h, base+"/s/"+id+"/cancel", url.Values{"sets": {"0"}}, false)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != base+"/" {
		t.Fatalf("status %d, location %q", res.Code, res.Header().Get("Location"))
	}
	session, _ := strconv.ParseInt(id, 10, 64)
	if _, err := world.svc.GetSession(context.Background(), session); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("today's session is still there: %v", err)
	}

	// Entrenar is idle again, so any routine can be chosen.
	home := get(t, world.h, base+"/").Body.String()
	for _, want := range []string{`id="next-routine"`, `value="` + itoa(world.push.ID) + `"`, `value="` + itoa(world.pull.ID) + `"`} {
		if !strings.Contains(home, want) {
			t.Fatalf("Entrenar does not offer the routines again, missing %q", want)
		}
	}
	if res := post(t, world.h, base+"/plan", url.Values{"plan_id": {itoa(world.pull.ID)}}, false); res.Code != http.StatusSeeOther {
		t.Fatalf("choosing another routine: status %d", res.Code)
	}
	live, err := world.svc.LiveWorkout(context.Background())
	if err != nil || live.Session == nil || live.Session.PlanName != "Tirón A" {
		t.Fatalf("the other routine did not start: %+v, %v", live.Session, err)
	}

	// The routine is a template: cancelling its workout leaves it whole.
	plan, err := world.svc.GetPlan(context.Background(), world.push.ID)
	if err != nil || len(plan.Items) != 2 || plan.TotalSets != 5 {
		t.Fatalf("routine changed: %+v, %v", plan, err)
	}
}

func TestCancellingAWorkoutWithSetsAsksFirstAndDeletesThem(t *testing.T) {
	world := newCancelWorld(t, "")
	id := world.start(t)
	post(t, world.h, base+"/sets", setForm("banca", "80", "9", "9"), true)
	post(t, world.h, base+"/sets", setForm("banca", "80", "8", "9"), true)

	body := get(t, world.h, base+"/?modo=entrenar").Body.String()
	form := cancelForm(t, body, id)
	if !strings.Contains(form, "Se borran el entreno de hoy y sus 2 series") || !strings.Contains(form, `name="sets" value="2"`) {
		t.Fatalf("the cancel must state the sets it deletes:\n%s", form)
	}
	confirm := body[strings.LastIndex(body[:strings.Index(body, `action="`+base+`/s/`+id+`/cancel"`)], "<details"):]
	if !strings.HasPrefix(confirm, `<details class="danger-zone cancel-workout">`) {
		t.Fatalf("with sets logged the cancel must sit behind a confirmation:\n%.200s", confirm)
	}

	// Backing out at the confirmation sends nothing; a stale confirmation that
	// names fewer sets than are logged now deletes nothing either.
	before := dumpDatabase(t, world.db)
	get(t, world.h, base+"/?modo=entrenar")
	post(t, world.h, base+"/sets", setForm("laterales polea", "10", "15", "9"), true)
	afterSet := dumpDatabase(t, world.db)
	if afterSet == before {
		t.Fatal("logging a set changed nothing, so the comparison below proves nothing")
	}
	stale := post(t, world.h, base+"/s/"+id+"/cancel", url.Values{"sets": {"2"}}, false)
	if stale.Code != http.StatusSeeOther || stale.Header().Get("Location") != base+"/?modo=entrenar" {
		t.Fatalf("stale cancel: status %d, location %q", stale.Code, stale.Header().Get("Location"))
	}
	if after := dumpDatabase(t, world.db); after != afterSet {
		t.Fatalf("a stale cancel changed rows:\nbefore\n%s\nafter\n%s", afterSet, after)
	}

	res := post(t, world.h, base+"/s/"+id+"/cancel", url.Values{"sets": {"3"}}, false)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != base+"/" {
		t.Fatalf("status %d, location %q", res.Code, res.Header().Get("Location"))
	}
	session, _ := strconv.ParseInt(id, 10, 64)
	if _, err := world.svc.GetSession(context.Background(), session); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("today's session is still there: %v", err)
	}
	if hist, err := world.svc.ExerciseHistory(context.Background(), "banca", 10); err != nil || len(hist.Sets) != 0 {
		t.Fatalf("sets left = %+v, %v", hist, err)
	}
	plan, err := world.svc.GetPlan(context.Background(), world.push.ID)
	if err != nil || plan.TotalSets != 5 {
		t.Fatalf("routine changed: %+v, %v", plan, err)
	}

	// Cancelling again is a missing session, not a server error.
	if again := post(t, world.h, base+"/s/"+id+"/cancel", url.Values{"sets": {"3"}}, false); again.Code != http.StatusNotFound {
		t.Fatalf("second cancel status %d", again.Code)
	}
}

// Behind the web login the cancel is a plain form, so it carries the session's
// CSRF token itself or the middleware refuses it.
func TestTheCancelWorkoutFormCarriesTheCSRFToken(t *testing.T) {
	world := newCancelWorld(t, "tok-cancel")
	id := world.start(t)
	form := cancelForm(t, get(t, world.h, base+"/?modo=entrenar").Body.String(), id)
	if !strings.Contains(form, `<input type="hidden" name="csrf_token" value="tok-cancel">`) {
		t.Fatalf("cancel form lacks the CSRF field:\n%s", form)
	}
}
