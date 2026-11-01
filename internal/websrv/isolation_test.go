package websrv_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

const (
	secretExercise = "press secreto"
	secretPlan     = "rutina secreta"
	secretNote     = "nota secreta"
	missingWebID   = "999999"
)

func webClock() time.Time { return time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC) }

type webWorld struct {
	db            *sql.DB
	owner, second http.Handler
	set, session  string
	plan          string
}

func webHandler(t *testing.T, services *training.Services, userID int64) http.Handler {
	t.Helper()
	service := services.ForUser(userID)
	server, err := websrv.New(func(context.Context) *training.Service { return service }, services.Snapshot, webClock, base)
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
	return webWorld{
		db: db, owner: webHandler(t, services, 1), second: webHandler(t, services, 2),
		set: itoa(set.ID), session: itoa(session.ID), plan: itoa(plan.ID),
	}
}

func dumpDatabase(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, table := range []string{"users", "sessions", "sets", "plans", "plan_items", "session_items", "session_feedback",
		"exercise_notes", "exercise_groups", "volume_landmarks", "idempotency_keys"} {
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

func postID(path func(string) string, form func(string) url.Values) func(*testing.T, http.Handler, string) (int, string) {
	return func(t *testing.T, h http.Handler, id string) (int, string) {
		w := post(t, h, base+path(id), form(id), true)
		return w.Code, w.Body.String()
	}
}

func planRoute(path string, fields url.Values) func(*testing.T, http.Handler, string) (int, string) {
	return postID(func(string) string { return path }, func(id string) url.Values {
		form := url.Values{"plan_id": {id}}
		for k, v := range fields {
			form[k] = v
		}
		return form
	})
}

func TestWebRoutesTreatAnotherUsersIDAsMissing(t *testing.T) {
	noForm := func(string) url.Values { return url.Values{} }
	exercise := url.Values{"exercise": {secretExercise}}
	routes := []struct {
		name string
		call func(*testing.T, http.Handler, string) (int, string)
		pick func(webWorld) string
	}{
		{"delete set", postID(func(id string) string { return "/sets/" + id + "/delete" }, noForm), func(w webWorld) string { return w.set }},
		{"update set", postID(func(id string) string { return "/sets/" + id + "/update" }, func(string) url.Values { return url.Values{"reps": {"1"}} }), func(w webWorld) string { return w.set }},
		{"plan update", planRoute("/plans/update", url.Values{"name": {"robada"}, "notes": {"x"}}), func(w webWorld) string { return w.plan }},
		{"plan delete", planRoute("/plans/delete", nil), func(w webWorld) string { return w.plan }},
		{"plan item", planRoute("/plans/item", url.Values{"exercise": {secretExercise}, "target_sets": {"9"}}), func(w webWorld) string { return w.plan }},
		{"plan item remove", planRoute("/plans/item/remove", exercise), func(w webWorld) string { return w.plan }},
		{"plan move", planRoute("/plans/move", url.Values{"exercise": {secretExercise}, "direction": {"down"}}), func(w webWorld) string { return w.plan }},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			world := newWebWorld(t)
			before := dumpDatabase(t, world.db)

			foreignCode, foreignBody := route.call(t, world.second, route.pick(world))
			missingCode, missingBody := route.call(t, world.second, missingWebID)

			if foreignCode != missingCode || foreignBody != missingBody {
				t.Fatalf("foreign id: %d\n%s\nmissing id: %d\n%s", foreignCode, foreignBody, missingCode, missingBody)
			}
			if strings.Contains(foreignBody, secretExercise) || strings.Contains(foreignBody, secretPlan) {
				t.Fatalf("response carries the owner's data:\n%s", foreignBody)
			}
			if after := dumpDatabase(t, world.db); after != before {
				t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
			}

			if code, _ := route.call(t, world.owner, route.pick(world)); code != http.StatusOK {
				t.Fatalf("owner status = %d", code)
			}
			if after := dumpDatabase(t, world.db); after == before {
				t.Fatal("the owner's call changed nothing, so the refusal above proves nothing")
			}
		})
	}
}

func TestWebSessionPageTreatsAnotherUsersIDAsMissing(t *testing.T) {
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
func TestChoosingAnotherUsersPlanKeepsTheDaysEmptySession(t *testing.T) {
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

func TestWebPagesShowNothingOfAnotherUser(t *testing.T) {
	world := newWebWorld(t)
	store, err := sqlitestore.Open(t.TempDir() + "/empty.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	empty := webHandler(t, training.NewServices(store, webClock), 1)
	for _, tc := range []struct{ path, marker string }{
		{"/", secretExercise},
		{"/history", ""},
		{"/plans", secretPlan},
		{"/exercise-info?name=" + url.QueryEscape(secretExercise), secretNote},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := get(t, world.second, base+tc.path)
			want := get(t, empty, base+tc.path)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Fatalf("beside the owner: %d\n%s\non an empty database: %d\n%s", got.Code, got.Body, want.Code, want.Body)
			}
			owner := get(t, world.owner, base+tc.path).Body.String()
			if owner == got.Body.String() {
				t.Fatalf("the owner sees the same as a user with no data, so this proves nothing:\n%s", owner)
			}
			if tc.marker != "" && !strings.Contains(owner, tc.marker) {
				t.Fatalf("the owner's page lacks %q:\n%s", tc.marker, owner)
			}
		})
	}
}

func TestWebWritesBelongToTheUserWhoMadeThem(t *testing.T) {
	cases := []struct {
		name, path string
		form       url.Values
		count      string
	}{
		{"set", "/sets", setForm(secretExercise, "50", "5", "8"),
			`sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=2 AND st.exercise='` + secretExercise + `'`},
		{"note", "/note", url.Values{"exercise": {secretExercise}, "note": {"suya"}},
			`exercise_notes WHERE user_id=2 AND note='suya'`},
		{"feedback", "/feedback", url.Values{"muscle_group": {"pecho"}, "fatigue": {"1"}, "pump": {"1"}, "recovery": {"1"}},
			`session_feedback f JOIN sessions se ON se.id=f.session_id WHERE se.user_id=2 AND f.muscle_group='pecho'`},
		{"plan", "/plans/create", url.Values{"name": {secretPlan}},
			`plans WHERE user_id=2 AND name='` + secretPlan + `'`},
		{"session item", "/plan/item", url.Values{"action": {"adopt"}, "exercise": {secretExercise}, "done": {"1"}},
			`session_items i JOIN sessions se ON se.id=i.session_id WHERE se.user_id=2 AND i.exercise='` + secretExercise + `'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			world := newWebWorld(t)
			if w := post(t, world.second, base+tc.path, tc.form, true); w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			var n int
			if err := world.db.QueryRow(`SELECT COUNT(*) FROM ` + tc.count).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("%s found %d rows", tc.count, n)
			}
			var touched int
			if err := world.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=1`).Scan(&touched); err != nil {
				t.Fatal(err)
			}
			if touched != 1 {
				t.Fatalf("user 1 now has %d sessions", touched)
			}
		})
	}
}
