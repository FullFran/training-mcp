package mcpsrv

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	isolationDay      = "2026-08-07"
	isolationExercise = "banca"
	missingToolID     = 999999
)

type toolIDs struct {
	session, set, plan, memory int64
	// sibling is a fact like memory; relation is pending between them, and
	// fingerprint confirms merging the two.
	sibling, relation int64
	fingerprint       string
}

// isolationCase says how one tool is called. Tools addressed by an id must answer
// another user's id as they answer a missing one. Other reads must show a user
// beside the owner what they would show on an empty database; other writes must
// leave a row the count query finds under user 2.
type isolationCase struct {
	args  func(toolIDs) map[string]any
	byID  bool
	count string
}

func noArgs(toolIDs) map[string]any { return map[string]any{} }

var isolationCases = map[string]isolationCase{
	"memory_get":    {byID: true, args: func(id toolIDs) map[string]any { return map[string]any{"memory_id": id.memory} }},
	"memory_forget": {byID: true, args: func(id toolIDs) map[string]any { return map[string]any{"memory_id": id.memory} }},
	"memory_save": {count: `coach_memory WHERE user_id=2 AND topic_key='lesion/hombro' AND title='suya'`, args: func(toolIDs) map[string]any {
		return map[string]any{"topic_key": "lesion/hombro", "title": "suya", "body": "suya"}
	}},
	"memory_search":  {args: func(toolIDs) map[string]any { return map[string]any{"query": "hombro"} }},
	"memory_confirm": {byID: true, args: func(id toolIDs) map[string]any { return map[string]any{"memory_id": id.memory} }},
	"memory_judge": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"relation_id": id.relation, "relation": "supersedes"}
	}},
	"memory_consolidate_preview": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"memory_ids": []int64{id.memory, id.sibling}, "title": "fusion", "body": "fusion"}
	}},
	"memory_consolidate": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"memory_ids": []int64{id.memory, id.sibling}, "title": "fusion", "body": "fusion", "fingerprint": id.fingerprint}
	}},
	"memory_context": {args: noArgs},
	"start_session": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"date": isolationDay, "plan_id": id.plan}
	}},
	"add_set": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session, "exercise": isolationExercise, "weight_kg": 1, "reps": 1, "rpe": 8}
	}},
	"update_set": {byID: true, args: func(id toolIDs) map[string]any { return map[string]any{"set_id": id.set, "reps": 1} }},
	"delete_set": {byID: true, args: func(id toolIDs) map[string]any { return map[string]any{"set_id": id.set} }},
	"delete_session": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session}
	}},
	"get_session": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session}
	}},
	"session_progress": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session}
	}},
	"get_plan":    {byID: true, args: func(id toolIDs) map[string]any { return map[string]any{"plan_id": id.plan} }},
	"delete_plan": {byID: true, args: func(id toolIDs) map[string]any { return map[string]any{"plan_id": id.plan} }},
	"update_plan": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"plan_id": id.plan, "name": "robado"}
	}},
	"set_plan_exercise": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"plan_id": id.plan, "exercise": isolationExercise, "target_sets": 9}
	}},
	"remove_plan_exercise": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"plan_id": id.plan, "exercise": isolationExercise}
	}},
	"move_plan_exercise": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"plan_id": id.plan, "exercise": isolationExercise, "direction": "down"}
	}},
	"add_session_exercise": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session, "exercise": "press", "target_sets": 9}
	}},
	"adjust_session_exercise": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session, "exercise": isolationExercise, "target_sets": 9}
	}},
	"swap_session_exercise": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session, "exercise": isolationExercise, "replacement": "press"}
	}},
	"remove_session_exercise": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session, "exercise": isolationExercise}
	}},
	"save_session_as_plan": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session, "name": "copiada"}
	}},
	"record_feedback": {byID: true, args: func(id toolIDs) map[string]any {
		return map[string]any{"session_id": id.session, "muscle_group": "espalda", "fatigue": 3, "pump": 3, "recovery": 3}
	}},

	"list_sessions":         {args: noArgs},
	"list_plans":            {args: noArgs},
	"list_exercise_groups":  {args: noArgs},
	"list_exercise_notes":   {args: noArgs},
	"list_volume_landmarks": {args: noArgs},
	"volume_recommendation": {args: noArgs},
	"weekly_volume":         {args: noArgs},
	"volume_by_muscle":      {args: noArgs},
	"live_workout":          {args: noArgs},
	"exercise_history": {args: func(toolIDs) map[string]any {
		return map[string]any{"exercise": isolationExercise}
	}},
	"suggest_load": {args: func(toolIDs) map[string]any {
		return map[string]any{"exercise": isolationExercise}
	}},

	"log_sets": {count: `sessions WHERE user_id=2 AND date='` + isolationDay + `'`, args: func(toolIDs) map[string]any {
		return map[string]any{"idempotency_key": "clave", "exercise": isolationExercise, "sets": []map[string]any{{"weight_kg": 1, "reps": 1}}}
	}},
	"log_set": {count: `sessions WHERE user_id=2 AND date='` + isolationDay + `'`, args: func(toolIDs) map[string]any {
		return map[string]any{"exercise": isolationExercise, "weight_kg": 1, "reps": 1, "rpe": 8}
	}},
	"create_plan": {count: `plans WHERE user_id=2 AND name='empuje'`, args: func(toolIDs) map[string]any {
		return map[string]any{"name": "empuje", "items": []map[string]any{{"exercise": isolationExercise, "target_sets": 1}}}
	}},
	"set_exercise_group": {count: `exercise_groups WHERE user_id=2 AND exercise='` + isolationExercise + `' AND muscle_group='espalda'`, args: func(toolIDs) map[string]any {
		return map[string]any{"exercise": isolationExercise, "muscle_group": "espalda"}
	}},
	"set_exercise_note": {count: `exercise_notes WHERE user_id=2 AND exercise='` + isolationExercise + `' AND note='suya'`, args: func(toolIDs) map[string]any {
		return map[string]any{"exercise": isolationExercise, "note": "suya"}
	}},
	"set_rest_target": {count: `users WHERE id=2 AND rest_seconds=75`, args: func(toolIDs) map[string]any {
		return map[string]any{"rest_seconds": 75}
	}},
	"set_volume_landmarks": {count: `volume_landmarks WHERE user_id=2 AND muscle_group='pecho' AND mev=1`, args: func(toolIDs) map[string]any {
		return map[string]any{"muscle_group": "pecho", "mev": 1, "mrv": 2}
	}},
}

type isolationWorld struct {
	db            *sql.DB
	owner, second *mcp.ClientSession
	ids           toolIDs
	// ownerServer and ownerService answer the owner's calls, for accepting proposals.
	ownerServer  *Server
	ownerService *training.Service
	services     *training.Services
}

func isolationClock() time.Time { return time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC) }

func newIsolationWorld(t *testing.T) isolationWorld {
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

	services := training.NewServices(store, isolationClock)
	owner := services.ForUser(1)
	plan, err := owner.CreatePlan(ctx, training.Plan{Name: "empuje", Items: []training.PlanItem{
		{Exercise: isolationExercise, TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 8},
		{Exercise: "remo", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := owner.StartPlannedSession(ctx, isolationDay, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	set, _, err := owner.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: isolationExercise, WeightKG: 80, Reps: 8, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.RecordFeedback(ctx, session.ID, training.Feedback{MuscleGroup: "pecho", Fatigue: 1, Pump: 2, Recovery: 1}); err != nil {
		t.Fatal(err)
	}
	if err = owner.SetExerciseNote(ctx, isolationExercise, "agarre ancho"); err != nil {
		t.Fatal(err)
	}
	if err = owner.SetExerciseGroup(ctx, isolationExercise, "pecho"); err != nil {
		t.Fatal(err)
	}
	if err = owner.SetLandmarks(ctx, training.Landmarks{MuscleGroup: "pecho", MEV: 4, MRV: 12}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.LogSets(ctx, training.LogSetsInput{Key: "clave", Exercise: isolationExercise, Sets: []training.LoggedSet{{WeightKG: 70, Reps: 10, RPE: 7}}}); err != nil {
		t.Fatal(err)
	}
	memory, err := owner.SaveMemory(ctx, training.MemoryInput{TopicKey: "lesion/hombro", Type: "limitation", Title: "hombro", Body: "duele en press"})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := owner.SaveMemory(ctx, training.MemoryInput{TopicKey: "lesion/hombro-2", Type: "limitation", Title: "hombro derecho", Body: "sigue"})
	if err != nil {
		t.Fatal(err)
	}
	merge, err := owner.PreviewConsolidation(ctx, training.ConsolidationInput{MemoryIDs: []int64{memory.Memory.ID, sibling.Memory.ID}, Title: "fusion", Body: "fusion"})
	if err != nil {
		t.Fatal(err)
	}
	// User 2 accepts every proposal, so each write tool reaches its data path. The
	// pending path is isolated by TestPendingProposalsAreIsolatedBetweenUsers.
	if err = services.ForUser(2).SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	ownerServer := New(only(owner))
	return isolationWorld{
		db:           db,
		ownerServer:  ownerServer,
		services:     services,
		ownerService: owner,
		owner:        mustConnect(t, ownerServer.Handler()),
		second:       mustConnect(t, New(only(services.ForUser(2))).Handler()),
		ids: toolIDs{session: session.ID, set: set.ID, plan: plan.ID, memory: memory.Memory.ID,
			sibling: sibling.Memory.ID, relation: sibling.Candidates[0].RelationID, fingerprint: merge.Fingerprint},
	}
}

func dumpTables(t *testing.T, db *sql.DB, where map[string]string) string {
	t.Helper()
	tables := []string{"users", "sessions", "sets", "plans", "plan_items", "session_items", "session_feedback",
		"exercise_notes", "exercise_groups", "volume_landmarks", "idempotency_keys", "coach_memory", "coach_memory_revisions", "coach_memory_relations"}
	var out strings.Builder
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ` + where[table] + ` ORDER BY rowid`)
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

var ownersRows = map[string]string{
	"users":                  `WHERE id=1`,
	"sessions":               `WHERE user_id=1`,
	"plans":                  `WHERE user_id=1`,
	"sets":                   `WHERE session_id IN (SELECT id FROM sessions WHERE user_id=1)`,
	"session_items":          `WHERE session_id IN (SELECT id FROM sessions WHERE user_id=1)`,
	"session_feedback":       `WHERE session_id IN (SELECT id FROM sessions WHERE user_id=1)`,
	"plan_items":             `WHERE plan_id IN (SELECT id FROM plans WHERE user_id=1)`,
	"exercise_notes":         `WHERE user_id=1`,
	"exercise_groups":        `WHERE user_id=1`,
	"volume_landmarks":       `WHERE user_id=1`,
	"idempotency_keys":       `WHERE user_id=1`,
	"coach_memory":           `WHERE user_id=1`,
	"coach_memory_revisions": `WHERE memory_id IN (SELECT id FROM coach_memory WHERE user_id=1)`,
	"coach_memory_relations": `WHERE user_id=1`,
}

func answer(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("error=%v content=%s structured=%s", result.IsError, stringContent(result), data)
}

func TestEveryToolIsIsolatedBetweenUsers(t *testing.T) {
	missing := toolIDs{session: missingToolID, set: missingToolID, plan: missingToolID, memory: missingToolID, sibling: missingToolID + 1, relation: missingToolID, fingerprint: "absent"}
	for name, tc := range isolationCases {
		t.Run(name, func(t *testing.T) {
			world := newIsolationWorld(t)
			args := tc.args(world.ids)

			switch {
			case tc.byID:
				before := dumpTables(t, world.db, nil)
				foreign := call(t, world.second, name, args)
				absent := call(t, world.second, name, tc.args(missing))
				if !foreign.IsError || answer(t, foreign) != answer(t, absent) {
					t.Fatalf("foreign id:\n%s\nmissing id:\n%s", answer(t, foreign), answer(t, absent))
				}
				if after := dumpTables(t, world.db, nil); after != before {
					t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
				}
				if result := call(t, world.owner, name, args); result.IsError {
					t.Fatalf("owner: %s", answer(t, result))
				}

			case tc.count != "":
				before := dumpTables(t, world.db, ownersRows)
				if result := call(t, world.second, name, args); result.IsError {
					t.Fatalf("second user: %s", answer(t, result))
				}
				var n int
				if err := world.db.QueryRow(`SELECT COUNT(*) FROM ` + tc.count).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("%s found %d rows", tc.count, n)
				}
				if after := dumpTables(t, world.db, ownersRows); after != before {
					t.Fatalf("owner's rows changed:\nbefore\n%s\nafter\n%s", before, after)
				}

			default:
				before := dumpTables(t, world.db, nil)
				got := answer(t, call(t, world.second, name, args))
				store, err := sqlitestore.Open(t.TempDir() + "/empty.db")
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				empty := mustConnect(t, New(only(training.NewServices(store, isolationClock).ForUser(1))).Handler())
				if want := answer(t, call(t, empty, name, args)); got != want {
					t.Fatalf("beside the owner:\n%s\non an empty database:\n%s", got, want)
				}
				if owner := answer(t, call(t, world.owner, name, args)); owner == got {
					t.Fatalf("the owner sees the same as a user with no data, so this proves nothing:\n%s", owner)
				}
				if after := dumpTables(t, world.db, nil); after != before {
					t.Fatalf("a read changed rows:\nbefore\n%s\nafter\n%s", before, after)
				}
			}
		})
	}
}

// A tool added later without saying how it is isolated fails here.
func TestEveryToolHasAnIsolationCase(t *testing.T) {
	session := mustConnect(t, New(only(training.NewService(testStore{}, time.Now))).Handler())
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	covered := make([]string, 0, len(isolationCases))
	for name := range isolationCases {
		covered = append(covered, name)
	}
	sort.Strings(covered)
	if got, want := strings.Join(covered, " "), strings.Join(sortedToolNames(tools), " "); got != want {
		t.Fatalf("isolation cases:\n%s\ntools:\n%s", got, want)
	}
}
