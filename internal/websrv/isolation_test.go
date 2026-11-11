package websrv_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

const (
	secretExercise = "press secreto"
	secretPlan     = "rutina secreta"
	secretNote     = "nota secreta"
	secretMemory   = "memoria secreta"
	missingWebID   = "999999"
)

func webClock() time.Time { return time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC) }

type webWorld struct {
	db            *sql.DB
	owner, second http.Handler
	set, session  string
	plan          string
	// proposal is the owner's pending proposal to create a plan named secretPlan+" 2".
	proposal string
	memory   string
	// rule is the owner's stored grant.
	rule string
	// conflict is the owner's judged conflict between two facts.
	conflict string
}

func webHandler(t *testing.T, services *training.Services, userID int64) http.Handler {
	t.Helper()
	service := services.ForUser(userID)
	serviceFor := func(context.Context) *training.Service { return service }
	server, err := websrv.NewWithOptions(serviceFor, services.Snapshot, webClock, base, websrv.Options{Export: true, Apply: mcpsrv.New(serviceFor).Apply})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func newWebWorld(t *testing.T) webWorld {
	t.Helper()
	ctx := context.Background()
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
	if _, err = db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	services := training.NewServices(store, webClock)
	owner := services.ForUser(1)
	plan, err := owner.CreatePlan(ctx, training.Plan{Name: secretPlan, Items: []training.PlanItem{
		{Exercise: secretExercise, TargetSets: 3}, {Exercise: "remo", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := owner.StartPlannedSession(ctx, "2026-08-06", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	set, _, err := owner.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: secretExercise, WeightKG: 80, Reps: 8, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.SetExerciseNote(ctx, secretExercise, secretNote); err != nil {
		t.Fatal(err)
	}
	if err = owner.SetExerciseGroup(ctx, secretExercise, "pecho"); err != nil {
		t.Fatal(err)
	}
	// The owner asks before agent writes and has one waiting; the second user
	// accepts everything, as the owner of an empty database does.
	if err = owner.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err = services.ForUser(2).SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"name":"` + secretPlan + ` 2","items":[{"exercise":"` + secretExercise + `","target_sets":1}]}`)
	proposal, err := owner.Propose(training.WithAgentSession(ctx, training.ExternalSession), training.ProposalInput{Tool: "create_plan", ActionType: "plan.create", Payload: payload},
		func(context.Context) (json.RawMessage, error) { return nil, errors.New("not applied while pending") })
	if err != nil {
		t.Fatal(err)
	}
	origin, err := store.ForUser(1).RecordDecision(ctx, training.DecisionRecord{Time: time.Unix(0, 0), Permission: "set.log", Outcome: training.OutcomeAllow,
		DeciderKind: training.DeciderSubject, DeciderID: training.AccountHolder, Channel: training.ChannelWeb})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.ForUser(1).AddRule(ctx, training.AnswerRule{ActionType: "set.log", Effect: training.EffectAllow, Session: training.ExternalSession,
		ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := owner.SaveMemory(ctx, training.MemoryInput{TopicKey: "lesion/hombro", Type: "limitation", Title: secretMemory, Body: "duele en " + secretExercise})
	if err != nil {
		t.Fatal(err)
	}
	rival, err := owner.SaveMemory(ctx, training.MemoryInput{TopicKey: "lesion/hombro-2", Type: "limitation", Title: secretMemory + " bis", Body: "no duele"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.JudgeMemory(ctx, rival.Candidates[0].RelationID, training.RelationConflictsWith, "opuestos"); err != nil {
		t.Fatal(err)
	}
	// With no review date, confirming visibly changes the fact.
	if _, err = db.Exec(`UPDATE coach_memory SET review_after=''`); err != nil {
		t.Fatal(err)
	}
	return webWorld{
		db: db, owner: webHandler(t, services, 1), second: webHandler(t, services, 2),
		set: itoa(set.ID), session: itoa(session.ID), plan: itoa(plan.ID), proposal: itoa(proposal.ID), rule: itoa(rule),
		memory: itoa(memory.Memory.ID), conflict: itoa(rival.Candidates[0].RelationID),
	}
}

func dumpDatabase(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, table := range []string{"users", "sessions", "sets", "plans", "plan_items", "session_items", "session_feedback",
		"exercise_notes", "exercise_groups", "volume_landmarks", "idempotency_keys",
		"proposals", "answer_rules", "decision_records", "agent_permissions", "coach_memory", "coach_memory_revisions", "coach_memory_relations"} {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&out, "%s %#v\n", table, values)
		}
		rows.Close()
	}
	return out.String()
}

// webRoutes says how each registered route keeps users apart. A route added later
// with no entry fails TestEveryWebRouteHasAnIsolationCase.
var webRoutes = map[string]func(*testing.T){
	"POST /sets/{id}/delete": idRoute(func(w webWorld) string { return w.set }, func(id string) (string, url.Values) {
		return "/sets/" + id + "/delete", url.Values{}
	}),
	"POST /sets/{id}/update": idRoute(func(w webWorld) string { return w.set }, func(id string) (string, url.Values) {
		return "/sets/" + id + "/update", url.Values{"reps": {"1"}}
	}),
	"POST /plans/update":      planRoute("/plans/update", url.Values{"name": {"robada"}, "notes": {"x"}}),
	"POST /plans/delete":      planRoute("/plans/delete", nil),
	"POST /plans/item":        planRoute("/plans/item", url.Values{"exercise": {secretExercise}, "target_sets": {"9"}}),
	"POST /plans/item/remove": planRoute("/plans/item/remove", url.Values{"exercise": {secretExercise}}),
	"POST /plans/move":        planRoute("/plans/move", url.Values{"exercise": {secretExercise}, "direction": {"down"}}),
	"POST /plan":              testChoosingAnotherUsersPlanKeepsTheDaysEmptySession,
	"GET /s/{id}":             testWebSessionPageTreatsAnotherUsersIDAsMissing,

	"GET /{$}":           pageRoute("/", secretExercise),
	"GET /history":       pageRoute("/history", ""),
	"GET /plans":         pageRoute("/plans", secretPlan),
	"GET /exercise-info": pageRoute("/exercise-info?name="+url.QueryEscape(secretExercise), secretNote),

	"POST /sets": writeRoute("/sets", setForm(secretExercise, "50", "5", "8"),
		`sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=2 AND st.exercise='`+secretExercise+`'`),
	"POST /note": writeRoute("/note", url.Values{"exercise": {secretExercise}, "note": {"suya"}},
		`exercise_notes WHERE user_id=2 AND note='suya'`),
	"POST /feedback": writeRoute("/feedback", url.Values{"muscle_group": {"pecho"}, "fatigue": {"1"}, "pump": {"1"}, "recovery": {"1"}},
		`session_feedback f JOIN sessions se ON se.id=f.session_id WHERE se.user_id=2 AND f.muscle_group='pecho'`),
	"POST /plans/create": writeRoute("/plans/create", url.Values{"name": {secretPlan}},
		`plans WHERE user_id=2 AND name='`+secretPlan+`'`),
	"POST /plan/item": writeRoute("/plan/item", url.Values{"action": {"adopt"}, "exercise": {secretExercise}, "done": {"1"}},
		`session_items i JOIN sessions se ON se.id=i.session_id WHERE se.user_id=2 AND i.exercise='`+secretExercise+`'`),

	"POST /rest":  testRestTargetBelongsToWhoSetsIt,
	"GET /review": pageRoute("/review", secretPlan+" 2"),
	"POST /review/{id}/accept": idRoute(func(w webWorld) string { return w.proposal }, func(id string) (string, url.Values) {
		return "/review/" + id + "/accept", url.Values{"answer": {"allow_always"}}
	}),
	"POST /review/{id}/reject": idRoute(func(w webWorld) string { return w.proposal }, func(id string) (string, url.Values) {
		return "/review/" + id + "/reject", url.Values{"answer": {"reject_always"}}
	}),
	"POST /review/accept-all": writeRoute("/review/accept-all", url.Values{"on": {"0"}},
		`agent_permissions WHERE user_id=2 AND accept_all=0`),
	"POST /review/forget": testForgettingTouchesOnlyTheUsersOwnRules,

	"GET /memory": pageRoute("/memory", secretMemory),
	"POST /memory/{id}/update": idRoute(func(w webWorld) string { return w.memory }, func(id string) (string, url.Values) {
		return "/memory/" + id + "/update", url.Values{"title": {"robada"}, "body": {"robada"}, "type": {"note"}}
	}),
	"POST /memory/{id}/delete": idRoute(func(w webWorld) string { return w.memory }, func(id string) (string, url.Values) {
		return "/memory/" + id + "/delete", url.Values{}
	}),
	"POST /memory/{id}/confirm": idRoute(func(w webWorld) string { return w.memory }, func(id string) (string, url.Values) {
		return "/memory/" + id + "/confirm", url.Values{}
	}),
	"POST /memory/conflicts/{id}/resolve": idRoute(func(w webWorld) string { return w.conflict }, func(id string) (string, url.Values) {
		return "/memory/conflicts/" + id + "/resolve", url.Values{"keep": {"new"}}
	}),
	"POST /memory/delete-all": testDeletingAllMemoryTouchesOnlyTheUsersOwn,

	"GET /manifest.webmanifest": sharedRoute("the same document for everyone"),
	"GET /sw.js":                sharedRoute("the same document for everyone"),
	"GET /static/":              sharedRoute("embedded assets, the same for everyone"),
	"GET":                       sharedRoute("a redirect to the app root"),
	"GET /export.db":            sharedRoute("copies every user's data; it leaves the web before a second user reaches it"),
}

func TestEveryWebRouteIsIsolated(t *testing.T) {
	for route, check := range webRoutes {
		t.Run(route, check)
	}
}

func TestEveryWebRouteHasAnIsolationCase(t *testing.T) {
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
	registered := []string{}
	for _, pattern := range server.Routes() {
		registered = append(registered, strings.TrimSpace(strings.Replace(pattern, base, "", 1)))
	}
	sort.Strings(registered)
	covered := []string{}
	for route := range webRoutes {
		covered = append(covered, route)
	}
	sort.Strings(covered)
	if got, want := strings.Join(covered, "\n"), strings.Join(registered, "\n"); got != want {
		t.Fatalf("isolation cases:\n%s\nregistered routes:\n%s", got, want)
	}
}

func testForgettingTouchesOnlyTheUsersOwnRules(t *testing.T) {
	world := newWebWorld(t)
	before := dumpDatabase(t, world.db)
	if w := post(t, world.second, base+"/review/forget", url.Values{"rule_id": {world.rule}}, true); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
	if w := post(t, world.owner, base+"/review/forget", url.Values{"rule_id": {world.rule}}, true); w.Code != http.StatusOK {
		t.Fatalf("owner status %d", w.Code)
	}
	var n int
	if err := world.db.QueryRow(`SELECT COUNT(*) FROM answer_rules WHERE user_id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("owner's rules = %d, %v", n, err)
	}
}

func testDeletingAllMemoryTouchesOnlyTheUsersOwn(t *testing.T) {
	world := newWebWorld(t)
	before := dumpDatabase(t, world.db)
	if w := post(t, world.second, base+"/memory/delete-all", url.Values{"confirm": {"borrar"}}, true); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
	if w := post(t, world.owner, base+"/memory/delete-all", url.Values{"confirm": {"borrar"}}, true); w.Code != http.StatusOK {
		t.Fatalf("owner status %d", w.Code)
	}
	var n int
	if err := world.db.QueryRow(`SELECT COUNT(*) FROM coach_memory WHERE user_id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("owner's memory = %d, %v", n, err)
	}
}

func sharedRoute(string) func(*testing.T) { return func(*testing.T) {} }

func planRoute(path string, fields url.Values) func(*testing.T) {
	return idRoute(func(w webWorld) string { return w.plan }, func(id string) (string, url.Values) {
		form := url.Values{"plan_id": {id}}
		for k, v := range fields {
			form[k] = v
		}
		return path, form
	})
}

// idRoute requires another user's id to get what a missing id gets, with the owner's
// own call succeeding so the refusal is known to be about the user.
func idRoute(pick func(webWorld) string, request func(id string) (string, url.Values)) func(*testing.T) {
	return func(t *testing.T) {
		world := newWebWorld(t)
		send := func(h http.Handler, id string) (int, string) {
			path, form := request(id)
			w := post(t, h, base+path, form, true)
			return w.Code, w.Body.String()
		}
		before := dumpDatabase(t, world.db)

		foreignCode, foreignBody := send(world.second, pick(world))
		missingCode, missingBody := send(world.second, missingWebID)

		if foreignCode != missingCode || foreignBody != missingBody {
			t.Fatalf("foreign id: %d\n%s\nmissing id: %d\n%s", foreignCode, foreignBody, missingCode, missingBody)
		}
		if strings.Contains(foreignBody, secretExercise) || strings.Contains(foreignBody, secretPlan) || strings.Contains(foreignBody, secretMemory) {
			t.Fatalf("response carries the owner's data:\n%s", foreignBody)
		}
		if after := dumpDatabase(t, world.db); after != before {
			t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
		}

		if code, _ := send(world.owner, pick(world)); code != http.StatusOK {
			t.Fatalf("owner status = %d", code)
		}
		if after := dumpDatabase(t, world.db); after == before {
			t.Fatal("the owner's call changed nothing, so the refusal above proves nothing")
		}
	}
}

func testWebSessionPageTreatsAnotherUsersIDAsMissing(t *testing.T) {
	world := newWebWorld(t)
	foreign := get(t, world.second, base+"/s/"+world.session)
	missing := get(t, world.second, base+"/s/"+missingWebID)
	if foreign.Code != http.StatusNotFound || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign: %d %q, missing: %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
	owner := get(t, world.owner, base+"/s/"+world.session)
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), secretExercise) {
		t.Fatalf("owner: %d %q", owner.Code, owner.Body)
	}
}

// Choosing a plan replaces the day's empty session, so a plan that is not the user's
// to start must be refused before that session is touched.
func testChoosingAnotherUsersPlanKeepsTheDaysEmptySession(t *testing.T) {
	world := newWebWorld(t)
	ownSession, err := world.db.Exec(`INSERT INTO sessions(user_id,date,started_at,plan_name) VALUES (2,'2026-08-07',CURRENT_TIMESTAMP,'')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ownSession.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	before := dumpDatabase(t, world.db)

	foreign := post(t, world.second, base+"/plan", url.Values{"plan_id": {world.plan}}, false)
	missing := post(t, world.second, base+"/plan", url.Values{"plan_id": {missingWebID}}, false)

	if foreign.Code != http.StatusInternalServerError || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign: %d %q, missing: %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("session %d or other rows changed:\nbefore\n%s\nafter\n%s", id, before, after)
	}

	// With sets already logged the same refusal applies, and the plan is not merged in.
	if _, err = world.db.Exec(`INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at) VALUES (?,1,'banca',1,1,8,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id); err != nil {
		t.Fatal(err)
	}
	before = dumpDatabase(t, world.db)
	if w := post(t, world.second, base+"/plan", url.Values{"plan_id": {world.plan}}, false); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}

	// The owner's own plan still replaces the owner's empty session.
	if _, err = world.db.Exec(`INSERT INTO sessions(user_id,date,started_at,plan_name) VALUES (1,'2026-08-07',CURRENT_TIMESTAMP,'')`); err != nil {
		t.Fatal(err)
	}
	if w := post(t, world.owner, base+"/plan", url.Values{"plan_id": {world.plan}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("owner status = %d", w.Code)
	}
	var planned int
	if err = world.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=1 AND date='2026-08-07' AND plan_id=?`, world.plan).Scan(&planned); err != nil {
		t.Fatal(err)
	}
	var empties int
	if err = world.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=1 AND date='2026-08-07' AND plan_id IS NULL`).Scan(&empties); err != nil {
		t.Fatal(err)
	}
	if planned != 1 || empties != 0 {
		t.Fatalf("owner has %d planned and %d empty sessions today", planned, empties)
	}
}

// pageRoute requires a user beside the owner to get what an empty database shows.
func pageRoute(path, marker string) func(*testing.T) {
	return func(t *testing.T) {
		world := newWebWorld(t)
		store, err := sqlitestore.Open(t.TempDir() + "/empty.db")
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		empty := webHandler(t, training.NewServices(store, webClock), 1)
		got := get(t, world.second, base+path)
		want := get(t, empty, base+path)
		if got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Fatalf("beside the owner: %d\n%s\non an empty database: %d\n%s", got.Code, got.Body, want.Code, want.Body)
		}
		owner := get(t, world.owner, base+path).Body.String()
		if owner == got.Body.String() {
			t.Fatalf("the owner sees the same as a user with no data, so this proves nothing:\n%s", owner)
		}
		if marker != "" && !strings.Contains(owner, marker) {
			t.Fatalf("the owner's page lacks %q:\n%s", marker, owner)
		}
	}
}

// writeRoute requires the write to land under user 2 and leave user 1's sessions alone.
func writeRoute(path string, form url.Values, count string) func(*testing.T) {
	return func(t *testing.T) {
		world := newWebWorld(t)
		if w := post(t, world.second, base+path, form, true); w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		var n int
		if err := world.db.QueryRow(`SELECT COUNT(*) FROM ` + count).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s found %d rows", count, n)
		}
		var touched int
		if err := world.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=1`).Scan(&touched); err != nil {
			t.Fatal(err)
		}
		if touched != 1 {
			t.Fatalf("user 1 now has %d sessions", touched)
		}
	}
}

// The rest target lives on the user's own row: setting it never reaches the owner's.
func testRestTargetBelongsToWhoSetsIt(t *testing.T) {
	world := newWebWorld(t)
	if w := post(t, world.owner, base+"/rest", url.Values{"seconds": {"150"}}, true); w.Code != http.StatusOK {
		t.Fatalf("owner: %d %s", w.Code, w.Body)
	}
	if w := post(t, world.second, base+"/rest", url.Values{"seconds": {"75"}}, true); w.Code != http.StatusOK {
		t.Fatalf("second user: %d %s", w.Code, w.Body)
	}
	for id, want := range map[int]int{1: 150, 2: 75} {
		var got int
		if err := world.db.QueryRow(`SELECT rest_seconds FROM users WHERE id=?`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("user %d rest = %d, %v, want %d", id, got, err, want)
		}
	}
	if body := get(t, world.second, base+"/").Body.String(); !strings.Contains(body, `data-rest-target="75"`) {
		t.Fatalf("second user's page should show their own target")
	}
}
