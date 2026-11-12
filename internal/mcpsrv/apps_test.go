package mcpsrv

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The rest-timer tool names its view the way MCP Apps 2026-01-26 asks, plus
// the deprecated flat key the official registerAppTool still sends.
func TestRenderRestTimerDeclaresItsView(t *testing.T) {
	session := mustConnect(t, New(only(training.NewService(testStore{}, liveClock))).Handler())
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool *mcp.Tool
	for _, candidate := range tools.Tools {
		if candidate.Name == "render_rest_timer" {
			tool = candidate
		}
	}
	if tool == nil {
		t.Fatal("render_rest_timer is not listed")
	}
	meta, _ := json.Marshal(tool.Meta)
	if string(meta) != `{"ui":{"resourceUri":"ui://vibetraining/rest-timer/v1"},"ui/resourceUri":"ui://vibetraining/rest-timer/v1"}` {
		t.Fatalf("_meta = %s", meta)
	}
	if props := tool.InputSchema.(map[string]any)["properties"]; props != nil && len(props.(map[string]any)) != 0 {
		t.Fatalf("render_rest_timer takes arguments (a user id would be a hole): %v", props)
	}
}

func TestRestTimerResourceIsTheEmbeddedSelfContainedView(t *testing.T) {
	session := mustConnect(t, New(only(training.NewService(testStore{}, liveClock))).Handler())
	ctx := context.Background()
	list, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Resources) != 1 || list.Resources[0].URI != restTimerURI || list.Resources[0].MIMEType != "text/html;profile=mcp-app" {
		t.Fatalf("resources = %+v", list.Resources)
	}
	read, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: restTimerURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 {
		t.Fatalf("contents = %d", len(read.Contents))
	}
	c := read.Contents[0]
	if c.URI != restTimerURI || c.MIMEType != "text/html;profile=mcp-app" || c.Text != restTimerHTML {
		t.Fatalf("content uri=%q mime=%q len=%d, want the embedded view (%d bytes)", c.URI, c.MIMEType, len(c.Text), len(restTimerHTML))
	}
	// Self-contained: no external script, stylesheet or frame, so the view
	// needs no CSP grant and the resource declares none.
	for _, external := range []string{"<script src", "<link ", "<iframe", "@import"} {
		if strings.Contains(c.Text, external) {
			t.Fatalf("the view loads something external: %q", external)
		}
	}
	if !strings.Contains(c.Text, `<script type="module">`) || !strings.Contains(c.Text, `role="timer"`) {
		t.Fatal("the view is not the built rest timer")
	}
	if c.Meta != nil {
		if ui, ok := c.Meta["ui"].(map[string]any); ok && ui["csp"] != nil {
			t.Fatalf("the view declares CSP domains: %v", ui["csp"])
		}
	}
	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://vibetraining/rest-timer/v0"}); err == nil {
		t.Fatal("an unknown view was served")
	}
}

// A render 35 s after the set reports the time actually left, and the text
// fallback says the same for hosts that cannot show the view.
func TestRenderRestTimerReportsTheRealRemainingTime(t *testing.T) {
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
	service := training.NewService(store.ForUser(1), liveClock)
	session := mustConnect(t, New(only(service)).Handler())

	var none RestTimerOut
	result := call(t, session, "render_rest_timer", map[string]any{})
	decode(t, result, &none)
	if none.Rest != (training.Rest{TargetSeconds: 120, State: training.RestNone}) || none.Now != "2026-08-07T10:00:00Z" ||
		none.IdleAfterSeconds != 1200 || none.LastSet != nil {
		t.Fatalf("no set yet = %+v", none)
	}
	if got := stringContent(result); got != "No set logged today yet. Rest target 2:00." {
		t.Fatalf("text = %q", got)
	}

	if err := service.SetRestTarget(ctx, 90); err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(ctx, training.Plan{Name: "empuje", Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.StartPlannedSession(ctx, "2026-08-07", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	set, _, err := service.AddSet(ctx, training.AddSetInput{SessionID: started.ID, Exercise: "banca", WeightKG: 80, Reps: 9, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE sets SET created_at='2026-08-07 09:59:25' WHERE id=?`, set.ID); err != nil {
		t.Fatal(err)
	}
	before := dumpTables(t, db, nil)

	var resting RestTimerOut
	result = call(t, session, "render_rest_timer", map[string]any{})
	decode(t, result, &resting)
	want := training.Rest{TargetSeconds: 90, State: training.RestResting, Since: "2026-08-07T09:59:25Z", ElapsedSeconds: 35, RemainingSeconds: 55}
	if resting.Rest != want {
		t.Fatalf("rest = %+v, want %+v", resting.Rest, want)
	}
	if resting.LastSet == nil || resting.LastSet.Exercise != "banca" || resting.LastSet.WeightKG != 80 || resting.LastSet.Reps != 9 ||
		resting.NextExercise != "banca" || resting.NextSuperset || resting.WeightUnit != "kg" {
		t.Fatalf("timer = %+v", resting)
	}
	if got := stringContent(result); got != "Resting: 0:55 left of 1:30 after banca. Next: banca." {
		t.Fatalf("text = %q", got)
	}
	if after := dumpTables(t, db, nil); after != before {
		t.Fatalf("rendering the timer changed rows:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// Two people with different targets each see their own countdown.
func TestRenderRestTimerUsesEachPersonsOwnTarget(t *testing.T) {
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
	alice := training.NewService(store.ForUser(1), liveClock)
	bob := training.NewService(store.ForUser(2), liveClock)
	if err := alice.SetRestTarget(ctx, 90); err != nil {
		t.Fatal(err)
	}
	if err := bob.SetRestTarget(ctx, 180); err != nil {
		t.Fatal(err)
	}
	for _, person := range []struct {
		service *training.Service
		target  int
	}{{alice, 90}, {bob, 180}} {
		if _, _, err := person.service.LogSet(ctx, "banca", 80, 8, 8, 1, "", ""); err != nil {
			t.Fatal(err)
		}
		var out RestTimerOut
		decode(t, call(t, mustConnect(t, New(only(person.service)).Handler()), "render_rest_timer", map[string]any{}), &out)
		if out.Rest.TargetSeconds != person.target || out.Rest.State != training.RestResting || out.Rest.RemainingSeconds != person.target {
			t.Fatalf("target %d: rest = %+v", person.target, out.Rest)
		}
	}
}

func TestRestTimerTextCoversEveryState(t *testing.T) {
	for _, tc := range []struct {
		out  RestTimerOut
		want string
	}{
		{RestTimerOut{Rest: training.Rest{TargetSeconds: 120, State: training.RestReady, OverBySeconds: 15}, LastSet: &RestTimerSet{Exercise: "remo"}},
			"Rest done: 2:00 target reached 0:15 ago after remo."},
		{RestTimerOut{Rest: training.Rest{TargetSeconds: 120, State: training.RestSuperset}, NextExercise: "dominadas", NextSuperset: true},
			"Superset: go straight to dominadas, no rest."},
		{RestTimerOut{Rest: training.Rest{TargetSeconds: 120, State: training.RestSuperset}}, "Superset: next exercise, no rest."},
		{RestTimerOut{Rest: training.Rest{TargetSeconds: 120, State: training.RestIdle}}, "No rest running: the last set was over 20 minutes ago."},
	} {
		if got := restTimerText(tc.out); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.out.Rest.State, got, tc.want)
		}
	}
}
