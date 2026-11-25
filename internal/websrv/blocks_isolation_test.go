package websrv_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
)

const (
	secretGoal  = "objetivo secreto"
	secretBlock = "bloque secreto"
)

// pzWorld is two accounts on one database: the owner with a goal, an active
// block, a routine and a session placed in the block; the second user with a
// session of their own and nothing planned.
type pzWorld struct {
	db            *sql.DB
	owner, second http.Handler
	ids           pzIDs
	secondSession string
}

// pzIDs are the ids a blocks form names.
type pzIDs struct{ goal, block, microcycle, session, plan string }

var missingPZ = pzIDs{goal: missingWebID, block: missingWebID, microcycle: missingWebID, session: missingWebID, plan: missingWebID}

func newPZWorld(t *testing.T) pzWorld {
	t.Helper()
	ctx := context.Background()
	path := sqlitestoretest.Path(t)
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
	plan, err := owner.CreatePlan(ctx, training.Plan{Name: secretPlan, Items: []training.PlanItem{{Exercise: secretExercise, TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	text := secretGoal
	goal, err := owner.SetGoal(ctx, training.GoalInput{Text: &text})
	if err != nil {
		t.Fatal(err)
	}
	intent, count, rotation := secretBlock, 3, []int64{plan.ID}
	block, err := owner.SaveBlock(ctx, training.BlockInput{GoalID: &goal.ID, Intent: &intent, MicrocycleCount: &count, Rotation: &rotation})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.AdvanceBlock(ctx, training.AdvanceInput{BlockID: block.ID, Action: training.BlockActivate, StartDate: "2026-08-01"}); err != nil {
		t.Fatal(err)
	}
	session, err := owner.StartPlannedSession(ctx, "2026-08-02", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	own, err := services.ForUser(2).StartSession(ctx, "2026-08-03")
	if err != nil {
		t.Fatal(err)
	}
	return pzWorld{
		db: db, owner: webHandler(t, services, 1), second: webHandler(t, services, 2),
		ids:           pzIDs{goal: itoa(goal.ID), block: itoa(block.ID), microcycle: itoa(block.Microcycles[1].ID), session: itoa(session.ID), plan: itoa(plan.ID)},
		secondSession: itoa(own.ID),
	}
}

// dumpPeriodization is every row a blocks form could change.
func dumpPeriodization(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, table := range []string{"goals", "blocks", "block_microcycles", "block_rotations", "sessions"} {
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

// pzRoute requires the second user posting the owner's ids to get exactly what
// missing ids get, with no row changed, and the owner's own post to succeed
// and change rows, so the refusal is known to be about the user.
func pzRoute(path string, form func(pzIDs) url.Values) func(*testing.T) {
	return func(t *testing.T) {
		world := newPZWorld(t)
		before := dumpPeriodization(t, world.db)
		foreign := post(t, world.second, base+path, form(world.ids), false)
		missing := post(t, world.second, base+path, form(missingPZ), false)
		if foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() || foreign.Code == http.StatusSeeOther {
			t.Fatalf("foreign ids: %d\n%s\nmissing ids: %d\n%s", foreign.Code, foreign.Body, missing.Code, missing.Body)
		}
		for _, secret := range []string{secretGoal, secretBlock, secretPlan} {
			if strings.Contains(foreign.Body.String(), secret) {
				t.Fatalf("the answer carries the owner's %q:\n%s", secret, foreign.Body)
			}
		}
		if after := dumpPeriodization(t, world.db); after != before {
			t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
		}
		if r := post(t, world.owner, base+path, form(world.ids), false); r.Code != http.StatusSeeOther {
			t.Fatalf("owner: %d\n%s", r.Code, r.Body)
		}
		if after := dumpPeriodization(t, world.db); after == before {
			t.Fatal("the owner's post changed nothing, so the refusal above proves nothing")
		}
	}
}

// The blocks screen of a user beside the owner is what an empty database
// shows, and the owner's block page is a missing one.
func testBlocksPageIsThePersonsOwn(t *testing.T) {
	world := newPZWorld(t)
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	emptyServices := training.NewServices(store, webClock)
	if _, err = emptyServices.ForUser(1).StartSession(context.Background(), "2026-08-03"); err != nil {
		t.Fatal(err)
	}
	empty := webHandler(t, emptyServices, 1)
	got, want := get(t, world.second, base+"/blocks"), get(t, empty, base+"/blocks")
	if got.Code != http.StatusOK || got.Code != want.Code || got.Body.String() != want.Body.String() {
		t.Fatalf("beside the owner: %d\n%s\non an empty database: %d\n%s", got.Code, got.Body, want.Code, want.Body)
	}
	owner := get(t, world.owner, base+"/blocks").Body.String()
	if !strings.Contains(owner, secretGoal) || !strings.Contains(owner, secretBlock) {
		t.Fatalf("the owner's page lacks their goal and block:\n%s", owner)
	}
	foreign, missing := get(t, world.second, base+"/blocks?bloque="+world.ids.block), get(t, world.second, base+"/blocks?bloque="+missingWebID)
	if foreign.Code != http.StatusNotFound || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign block: %d %q, missing: %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
}

var blocksRoutes = map[string]func(*testing.T){
	"GET /blocks": testBlocksPageIsThePersonsOwn,
	"POST /blocks/goal": func(t *testing.T) {
		pzRoute("/blocks/goal", func(ids pzIDs) url.Values {
			return url.Values{"goal_id": {ids.goal}, "text": {"robado"}}
		})(t)
		// Creating a goal writes the second user's own row.
		world := newPZWorld(t)
		if r := post(t, world.second, base+"/blocks/goal", url.Values{"text": {"mío"}}, false); r.Code != http.StatusSeeOther {
			t.Fatalf("own goal: %d", r.Code)
		}
		var n int
		if err := world.db.QueryRow(`SELECT COUNT(*) FROM goals WHERE user_id=2 AND text='mío'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("second user's goals = %d, %v", n, err)
		}
	},
	"POST /blocks/save": func(t *testing.T) {
		pzRoute("/blocks/save", func(ids pzIDs) url.Values {
			return url.Values{"block_id": {ids.block}, "intent": {"robado"}, "microcycle_count": {"3"}}
		})(t)
		// A new block may not point at the owner's goal or rotate the owner's
		// routine: both answer as missing ones.
		pzRoute("/blocks/save", func(ids pzIDs) url.Values {
			return url.Values{"intent": {"nuevo"}, "microcycle_count": {"2"}, "goal_id": {ids.goal}}
		})(t)
		pzRoute("/blocks/save", func(ids pzIDs) url.Values {
			return url.Values{"intent": {"nuevo"}, "microcycle_count": {"2"}, "rotation": {ids.plan}}
		})(t)
	},
	"POST /blocks/microcycle": pzRoute("/blocks/microcycle", func(ids pzIDs) url.Values {
		return url.Values{"block_id": {ids.block}, "position": {"2"}, "label": {"robado"}, "rotation_mode": {"block"}}
	}),
	"POST /blocks/advance": pzRoute("/blocks/advance", func(ids pzIDs) url.Values {
		return url.Values{"block_id": {ids.block}, "action": {"close"}}
	}),
	"POST /blocks/assign": func(t *testing.T) {
		pzRoute("/blocks/assign", func(ids pzIDs) url.Values {
			return url.Values{"session_id": {ids.session}, "microcycle_id": {"off_plan"}}
		})(t)
		// The second user's own session into the owner's microcycle answers as
		// a missing microcycle and stays unplaced.
		world := newPZWorld(t)
		before := dumpPeriodization(t, world.db)
		foreign := post(t, world.second, base+"/blocks/assign", url.Values{"session_id": {world.secondSession}, "microcycle_id": {world.ids.microcycle}}, false)
		missing := post(t, world.second, base+"/blocks/assign", url.Values{"session_id": {world.secondSession}, "microcycle_id": {missingWebID}}, false)
		if foreign.Code != http.StatusNotFound || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
			t.Fatalf("foreign microcycle: %d\n%s\nmissing: %d\n%s", foreign.Code, foreign.Body, missing.Code, missing.Body)
		}
		if after := dumpPeriodization(t, world.db); after != before {
			t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
		}
	},
}

// The blocks routes join the registry every registered route must be in.
func init() {
	for route, check := range blocksRoutes {
		webRoutes[route] = check
	}
}
