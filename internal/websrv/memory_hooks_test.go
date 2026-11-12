package websrv_test

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

type hookWeb struct {
	handler http.Handler
	service *training.Service
	store   *sqlitestore.Store
	logs    *bytes.Buffer
	now     *time.Time
}

// newHookWeb serves a person who asks before agent writes, with a clock that
// follows the real one (sets are stamped with it) and can be moved.
func newHookWeb(t *testing.T) hookWeb {
	t.Helper()
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	services := training.NewServices(store, clock)
	id, err := store.ProvisionUser(context.Background(), "user_hooks")
	if err != nil {
		t.Fatal(err)
	}
	service := services.ForUser(id)
	serviceFor := func(context.Context) *training.Service { return service }
	logs := &bytes.Buffer{}
	server, err := websrv.NewWithOptions(serviceFor, services.Snapshot, clock, base, websrv.Options{Apply: mcpsrv.New(serviceFor).Apply, Logger: log.New(logs, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	return hookWeb{handler: server.Handler(), service: service, store: store, logs: logs, now: &now}
}

func (w hookWeb) workout(t *testing.T) training.Session {
	t.Helper()
	ctx := context.Background()
	s, err := w.service.StartSession(ctx, w.now.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []training.AddSetInput{
		{SessionID: s.ID, Exercise: "banca", WeightKG: 40, Reps: 10, RPE: 5, Category: training.SetWarmup},
		{SessionID: s.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 8},
	} {
		if _, _, err := w.service.AddSet(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (w hookWeb) pending(t *testing.T) []training.Proposal {
	t.Helper()
	got, err := w.service.ListProposals(context.Background(), training.ProposalFilter{Pending: true})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Terminar proposes the workout's summary as a memory note, once.
func TestFinishingAWorkoutProposesItsSummaryOnce(t *testing.T) {
	w := newHookWeb(t)
	s := w.workout(t)
	form := url.Values{"session_id": {itoa(s.ID)}}
	if r := post(t, w.handler, base+"/finish", form, true); r.Code != http.StatusNoContent {
		t.Fatalf("finish = %d: %s", r.Code, r.Body)
	}
	got := w.pending(t)
	if len(got) != 1 || got[0].Tool != "memory_save" || got[0].ActionType != "memory.save" || !strings.Contains(string(got[0].Payload), "80 kg × 8") || strings.Contains(string(got[0].Payload), "40 kg × 10") {
		t.Fatalf("pending = %+v", got)
	}
	if r := post(t, w.handler, base+"/finish", form, true); r.Code != http.StatusNoContent || len(w.pending(t)) != 1 {
		t.Fatalf("second finish = %d, pending %d", r.Code, len(w.pending(t)))
	}
	if r := post(t, w.handler, base+"/finish", url.Values{"session_id": {"x"}}, true); r.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", r.Code)
	}
}

// The panel's Terminar button sends the session it shows.
func TestTheFinishButtonPostsTheSession(t *testing.T) {
	w := newHookWeb(t)
	s := w.workout(t)
	body := get(t, w.handler, base+"/").Body.String()
	if !strings.Contains(body, `hx-post="`+base+`/finish"`) || !strings.Contains(body, `name="session_id" value="`+itoa(s.ID)+`"`) {
		t.Fatalf("no finish form in the page")
	}
}

// A failing summary never blocks finishing: the answer is the same, the
// failure is logged without the person's data.
func TestAFailingWorkoutSummaryNeverBlocksFinishing(t *testing.T) {
	w := newHookWeb(t)
	s := w.workout(t)
	w.store.Close()
	if r := post(t, w.handler, base+"/finish", url.Values{"session_id": {itoa(s.ID)}}, true); r.Code != http.StatusNoContent {
		t.Fatalf("finish = %d: %s", r.Code, r.Body)
	}
	if logs := w.logs.String(); !strings.Contains(logs, `"event":"memory.hook"`) || !strings.Contains(logs, `"hook":"workout_summary"`) || strings.Contains(logs, "banca") {
		t.Fatalf("logs = %q", logs)
	}
}

// Opening the app after the workout went idle proposes its summary; a pause
// shorter than that does not.
func TestOpeningTheAppAfterAnIdleWorkoutProposesItsSummary(t *testing.T) {
	w := newHookWeb(t)
	w.workout(t)
	*w.now = time.Now().UTC().Add(training.WorkoutIdleAfter / 2)
	if r := get(t, w.handler, base+"/"); r.Code != http.StatusOK || len(w.pending(t)) != 0 {
		t.Fatalf("before idle: %d, pending %d", r.Code, len(w.pending(t)))
	}
	*w.now = time.Now().UTC().Add(training.WorkoutIdleAfter + time.Minute)
	r := get(t, w.handler, base+"/")
	if r.Code != http.StatusOK || len(w.pending(t)) != 1 {
		t.Fatalf("after idle: %d, pending %d", r.Code, len(w.pending(t)))
	}
	if !strings.Contains(r.Body.String(), "Recordar (nota)") {
		t.Fatalf("the page does not show the proposal")
	}
}

func TestTheCoachIsToldAboutTheLearningsBlock(t *testing.T) {
	if !strings.Contains(engine.SystemPrompt, `"## Aprendizajes"`) {
		t.Fatal("the system prompt does not document the learnings block")
	}
}

// Finishing someone else's session proposes nothing for anyone.
func testFinishingAnotherUsersSessionProposesNothing(t *testing.T) {
	world := newWebWorld(t)
	before := dumpDatabase(t, world.db)
	if w := post(t, world.second, base+"/finish", url.Values{"session_id": {world.session}}, true); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}
