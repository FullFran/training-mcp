package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const (
	missingID int64 = 999999
	sharedDay       = "2026-08-07"
)

var userTables = []string{
	"users", "sessions", "sets", "plans", "plan_items", "session_items", "session_feedback",
	"exercise_notes", "exercise_groups", "volume_landmarks", "idempotency_keys",
	"proposals", "answer_rules", "decision_records", "agent_permissions", "coach_memory", "coach_memory_revisions", "coach_memory_relations",
	"imports", "imported_sets", "import_exercise_map",
}

// ownedRows selects one user's rows from each table, children through their parent.
var ownedRows = map[string]string{
	"sessions":               `SELECT * FROM sessions WHERE user_id=? ORDER BY rowid`,
	"plans":                  `SELECT * FROM plans WHERE user_id=? ORDER BY rowid`,
	"sets":                   `SELECT * FROM sets WHERE session_id IN (SELECT id FROM sessions WHERE user_id=?) ORDER BY rowid`,
	"session_items":          `SELECT * FROM session_items WHERE session_id IN (SELECT id FROM sessions WHERE user_id=?) ORDER BY rowid`,
	"session_feedback":       `SELECT * FROM session_feedback WHERE session_id IN (SELECT id FROM sessions WHERE user_id=?) ORDER BY rowid`,
	"plan_items":             `SELECT * FROM plan_items WHERE plan_id IN (SELECT id FROM plans WHERE user_id=?) ORDER BY rowid`,
	"exercise_notes":         `SELECT * FROM exercise_notes WHERE user_id=? ORDER BY rowid`,
	"exercise_groups":        `SELECT * FROM exercise_groups WHERE user_id=? ORDER BY rowid`,
	"volume_landmarks":       `SELECT * FROM volume_landmarks WHERE user_id=? ORDER BY rowid`,
	"idempotency_keys":       `SELECT * FROM idempotency_keys WHERE user_id=? ORDER BY rowid`,
	"proposals":              `SELECT * FROM proposals WHERE user_id=? ORDER BY rowid`,
	"answer_rules":           `SELECT * FROM answer_rules WHERE user_id=? ORDER BY rowid`,
	"decision_records":       `SELECT * FROM decision_records WHERE user_id=? ORDER BY rowid`,
	"agent_permissions":      `SELECT * FROM agent_permissions WHERE user_id=? ORDER BY rowid`,
	"coach_memory":           `SELECT * FROM coach_memory WHERE user_id=? ORDER BY rowid`,
	"coach_memory_revisions": `SELECT * FROM coach_memory_revisions WHERE memory_id IN (SELECT id FROM coach_memory WHERE user_id=?) ORDER BY rowid`,
	"coach_memory_relations": `SELECT * FROM coach_memory_relations WHERE user_id=? ORDER BY rowid`,
	"imports":                `SELECT * FROM imports WHERE user_id=? ORDER BY rowid`,
	"imported_sets":          `SELECT * FROM imported_sets WHERE user_id=? ORDER BY rowid`,
	"import_exercise_map":    `SELECT * FROM import_exercise_map WHERE user_id=? ORDER BY rowid`,
}

func openTwoUsers(t *testing.T) *Store {
	t.Helper()
	store, err := Open(freshDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err = store.db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return store
}

func dumpQuery(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%#v\n", values)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// dumpUser is every row one user owns, so a comparison survives another user adding theirs.
func dumpUser(t *testing.T, store *Store, userID int64) string {
	t.Helper()
	var out strings.Builder
	for _, table := range userTables[1:] {
		fmt.Fprintf(&out, "%s:\n%s", table, dumpQuery(t, store.db, ownedRows[table], userID))
	}
	return out.String()
}

// dumpAll is every row in the database, for operations that must change nothing at all.
func dumpAll(t *testing.T, store *Store) string {
	t.Helper()
	var out strings.Builder
	for _, table := range userTables {
		fmt.Fprintf(&out, "%s:\n%s", table, dumpQuery(t, store.db, `SELECT * FROM `+table+` ORDER BY rowid`))
	}
	return out.String()
}

type seeded struct {
	session, set, plan int64
	// proposal is pending; claimed is being applied; rule is a stored grant.
	proposal, claimed, rule int64
	memory                  int64
	// sibling is a second fact like memory; relation is pending between them,
	// conflict is a judged conflict with a third.
	sibling, relation, conflict int64
}

// seedUser gives a user a row of every kind, using the same names for whoever calls it.
func seedUser(t *testing.T, user training.UserStore, group string) seeded {
	t.Helper()
	ctx := context.Background()
	plan, err := user.CreatePlan(ctx, training.Plan{Name: "empuje", Notes: "notas", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 8, Superset: "A"},
		{Exercise: "remo", TargetSets: 2},
		{Exercise: "curl", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := user.Start(ctx, training.Session{Date: sharedDay, PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	set, _, err := user.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 8, Superset: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = user.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 82, Reps: 6, RPE: 9}); err != nil {
		t.Fatal(err)
	}
	if err = user.SetFeedback(ctx, session.ID, training.Feedback{MuscleGroup: group, Fatigue: 1, Pump: 2, Recovery: 3}); err != nil {
		t.Fatal(err)
	}
	if err = user.SetExerciseNote(ctx, training.ExerciseNote{Exercise: "banca", Note: "agarre de " + group}); err != nil {
		t.Fatal(err)
	}
	if err = user.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "banca", MuscleGroup: group}); err != nil {
		t.Fatal(err)
	}
	if err = user.SetLandmarks(ctx, training.Landmarks{MuscleGroup: group, MEV: 4, MRV: 12}); err != nil {
		t.Fatal(err)
	}
	logged := training.LogSetsRecord{Key: "clave", Fingerprint: "huella", Date: "2026-08-08", Exercise: "banca",
		Sets: []training.LoggedSet{{WeightKG: 70, Reps: 10, RPE: 7}}}
	if _, err = user.LogSets(ctx, logged); err != nil {
		t.Fatal(err)
	}
	if err = user.SetRestTarget(ctx, 90); err != nil {
		t.Fatal(err)
	}
	if err = user.SetWeightUnit(ctx, "lb"); err != nil {
		t.Fatal(err)
	}
	pending, err := user.CreateProposal(ctx, training.Proposal{Tool: "log_sets", ActionType: "set.log", Payload: []byte(`{"exercise":"banca"}`), CreatedAt: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := user.CreateProposal(ctx, training.Proposal{Tool: "delete_set", ActionType: "set.delete", Payload: []byte(`{"set_id":1}`), CreatedAt: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err = user.ClaimProposal(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	origin, err := user.RecordDecision(ctx, training.DecisionRecord{Time: time.Unix(0, 0), Permission: "set.log", Outcome: training.OutcomeAllow,
		DeciderKind: training.DeciderSubject, DeciderID: training.AccountHolder, Channel: training.ChannelWeb})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := user.AddRule(ctx, training.AnswerRule{ActionType: "set.log", Effect: training.EffectAllow, Session: "coach:a", ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	if err = user.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	fact := func(key, title, body string) training.MemorySaved {
		saved, err := user.SaveMemory(ctx, training.Memory{TopicKey: key, Type: training.MemoryLimitation, Title: title, Body: body,
			SourceChatID: "chat-1", ReviewAfter: time.Unix(10, 0), CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	memory := fact("lesion/hombro", "hombro de "+group, "duele")
	fact("lesion/hombro", "hombro de "+group, "duele en press de "+group)
	sibling := fact("lesion/hombro-2", "hombro de "+group+" otra vez", "sigue")
	third := fact("lesion/hombro-3", "hombro de "+group+" tercera", "mejor")
	if _, err = user.JudgeMemory(ctx, third.Candidates[0].RelationID, training.RelationConflictsWith, "opuestos", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	return seeded{session: session.ID, set: set.ID, plan: plan.ID, proposal: pending.ID, claimed: claimed.ID, rule: rule,
		memory: memory.Memory.ID, sibling: sibling.Memory.ID, relation: sibling.Candidates[0].RelationID, conflict: third.Candidates[0].RelationID}
}

// idCases are the data methods addressed by an id. Each runs with an id that may
// belong to someone else or to nobody, and must not be able to tell which.
var idCases = map[string]func(context.Context, training.UserStore, seeded) (any, error){
	"Start": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.Start(c, training.Session{Date: sharedDay, PlanID: id.plan})
	},
	"AddSet": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		set, total, err := u.AddSet(c, training.AddSetInput{SessionID: id.session, Exercise: "banca", WeightKG: 1, Reps: 1, RPE: 8})
		return []any{set, total}, err
	},
	"UpdateSet": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		reps := 1
		set, total, err := u.UpdateSet(c, id.set, training.SetPatch{Reps: &reps})
		return []any{set, total}, err
	},
	"DeleteSet": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		session, total, remaining, err := u.DeleteSet(c, id.set)
		return []any{session, total, remaining}, err
	},
	"GetSession": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.GetSession(c, id.session)
	},
	"DeleteSession": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.DeleteSession(c, id.session)
	},
	"GetPlan": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.GetPlan(c, id.plan)
	},
	"UpdatePlan": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		name := "robado"
		return nil, u.UpdatePlan(c, id.plan, training.PlanPatch{Name: &name})
	},
	"SetPlanItem": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.SetPlanItem(c, id.plan, training.PlanItem{Exercise: "banca", TargetSets: 9})
	},
	"RemovePlanItem": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.RemovePlanItem(c, id.plan, "banca")
	},
	"MovePlanItem": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.MovePlanItem(c, id.plan, "banca", 1)
	},
	"DeletePlan": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.DeletePlan(c, id.plan)
	},
	"SessionProgress": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.SessionProgress(c, id.session)
	},
	"SetSessionItem": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.SetSessionItem(c, id.session, training.PlanItem{Exercise: "banca", TargetSets: 9})
	},
	"PatchSessionItem": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		sets := 9
		return nil, u.PatchSessionItem(c, id.session, "banca", training.SessionItemPatch{TargetSets: &sets})
	},
	"SwapSessionItem": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.SwapSessionItem(c, id.session, "banca", "press")
	},
	"RemoveSessionItem": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.RemoveSessionItem(c, id.session, "banca")
	},
	"SetFeedback": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.SetFeedback(c, id.session, training.Feedback{MuscleGroup: "espalda", Fatigue: 3})
	},
	"SessionFeedback": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.SessionFeedback(c, id.session)
	},
	"SupersetFor": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.SupersetFor(c, id.session, "banca")
	},
	"ClaimWorkoutSummary": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.ClaimWorkoutSummary(c, id.session, time.Unix(0, 0))
	},
	"LastSetAt": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		set, at, err := u.LastSetAt(c, id.session)
		return []any{set, at}, err
	},
	"GetProposal": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.GetProposal(c, id.proposal)
	},
	"ClaimProposal": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.ClaimProposal(c, id.proposal)
	},
	"RejectProposal": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.RejectProposal(c, id.proposal, training.DecidedByPerson, time.Unix(0, 0))
	},
	"FinishProposal": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.FinishProposal(c, id.claimed, training.ProposalOutcome{Status: training.ProposalAccepted, At: time.Unix(0, 0)})
	},
	"ForgetRule": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.ForgetRule(c, id.rule)
	},
	// An account created after migration 14 has no setting of its own, unlike the
	// owner of an empty database, so this is compared with a missing id instead.
	"Permissions": func(c context.Context, u training.UserStore, _ seeded) (any, error) {
		return u.Permissions(c)
	},
	"GetMemory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.GetMemory(c, id.memory)
	},
	"UpdateMemory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		body := "robado"
		return u.UpdateMemory(c, id.memory, training.MemoryPatch{Body: &body, UpdatedAt: time.Unix(1, 0)})
	},
	"DeleteMemory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.DeleteMemory(c, id.memory)
	},
	"MemoryHistory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.MemoryHistory(c, id.memory)
	},
	"ConfirmMemory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.ConfirmMemory(c, id.memory, time.Unix(99, 0))
	},
	"ForgetMemory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.ForgetMemory(c, id.memory, training.MemoryForgotten, time.Unix(99, 0))
	},
	"JudgeMemory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.JudgeMemory(c, id.relation, training.RelationSupersedes, "", time.Unix(99, 0))
	},
	"ResolveMemoryConflict": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return nil, u.ResolveMemoryConflict(c, id.conflict, training.KeepNew, time.Unix(99, 0))
	},
	"ConsolidateMemory": func(c context.Context, u training.UserStore, id seeded) (any, error) {
		return u.ConsolidateMemory(c, training.MemoryConsolidation{Sources: map[int64]int{id.memory: 2, id.sibling: 1},
			Survivor: training.Memory{TopicKey: "fusion", Type: training.MemoryNote, Title: "fusion", Body: "fusion", CreatedAt: time.Unix(99, 0), UpdatedAt: time.Unix(99, 0)},
			At:       time.Unix(99, 0)})
	},
	// Deleting everything as someone else must leave every row of the owner.
	"DeleteAllMemory": func(c context.Context, u training.UserStore, _ seeded) (any, error) { return u.DeleteAllMemory(c) },
}

func TestAnotherUsersIDBehavesExactlyLikeAMissingOne(t *testing.T) {
	missing := seeded{session: missingID, set: missingID, plan: missingID, proposal: missingID, claimed: missingID, rule: missingID, memory: missingID, sibling: missingID, relation: missingID, conflict: missingID}
	for name, run := range idCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := openTwoUsers(t)
			owner := store.ForUser(1)
			ids := seedUser(t, owner, "pecho")
			before := dumpAll(t, store)
			stranger := store.ForUser(2)

			foreignOut, foreignErr := run(ctx, stranger, ids)
			missingOut, missingErr := run(ctx, stranger, missing)

			if fmt.Sprint(foreignErr) != fmt.Sprint(missingErr) || !errors.Is(foreignErr, missingErr) {
				t.Fatalf("foreign id error = %v, missing id error = %v", foreignErr, missingErr)
			}
			if !reflect.DeepEqual(foreignOut, missingOut) {
				t.Fatalf("foreign id output = %#v, missing id output = %#v", foreignOut, missingOut)
			}
			if after := dumpAll(t, store); after != before {
				t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
			}

			// The same call by the owner works, so the refusal above was about the user.
			if _, err := run(ctx, owner, ids); err != nil {
				t.Fatalf("owner: %v", err)
			}
		})
	}
}

// readCases are the data methods that take no id, reading everything the user has.
var readCases = map[string]func(context.Context, training.UserStore) (any, error){
	"ListSessions": func(c context.Context, u training.UserStore) (any, error) {
		return u.ListSessions(c, training.ListFilter{Limit: 50})
	},
	"RecentExercises": func(c context.Context, u training.UserStore) (any, error) { return u.RecentExercises(c, 50) },
	"ListPlans":       func(c context.Context, u training.UserStore) (any, error) { return u.ListPlans(c) },
	"LatestFeedback":  func(c context.Context, u training.UserStore) (any, error) { return u.LatestFeedback(c) },
	"RecentFeedback":  func(c context.Context, u training.UserStore) (any, error) { return u.RecentFeedback(c, 5) },
	"AllLandmarks":    func(c context.Context, u training.UserStore) (any, error) { return u.AllLandmarks(c) },
	"ExerciseHistory": func(c context.Context, u training.UserStore) (any, error) { return u.ExerciseHistory(c, "banca", 50) },
	"WeeklyVolume": func(c context.Context, u training.UserStore) (any, error) {
		return u.WeeklyVolume(c, training.ListFilter{})
	},
	"ExerciseGroups": func(c context.Context, u training.UserStore) (any, error) { return u.ExerciseGroups(c) },
	"VolumeByGroup": func(c context.Context, u training.UserStore) (any, error) {
		return u.VolumeByGroup(c, training.ListFilter{})
	},
	"ExerciseNotes": func(c context.Context, u training.UserStore) (any, error) { return u.ExerciseNotes(c) },
	"RestTarget":    func(c context.Context, u training.UserStore) (any, error) { return u.RestTarget(c) },
	"WeightUnit":    func(c context.Context, u training.UserStore) (any, error) { return u.WeightUnit(c) },
	"Decisions":     func(c context.Context, u training.UserStore) (any, error) { return u.Decisions(c, 50) },
	"ListMemory": func(c context.Context, u training.UserStore) (any, error) {
		listed, err := u.ListMemory(c, training.MemoryQuery{Limit: 50})
		if err != nil {
			return nil, err
		}
		found, err := u.ListMemory(c, training.MemoryQuery{Text: "hombro press", Limit: 50})
		if err != nil {
			return nil, err
		}
		out := []any{listed, found}
		for _, view := range training.MemoryViews {
			got, err := u.ListMemory(c, training.MemoryQuery{View: view, Limit: 50, Now: time.Unix(50, 0)})
			if err != nil {
				return nil, err
			}
			out = append(out, got)
		}
		return out, nil
	},
	"ListMemoryConflicts": func(c context.Context, u training.UserStore) (any, error) { return u.ListMemoryConflicts(c) },
	"ListProposals": func(c context.Context, u training.UserStore) (any, error) {
		pending, err := u.ListProposals(c, training.ProposalFilter{Pending: true, Limit: 50})
		if err != nil {
			return nil, err
		}
		answered, err := u.ListProposals(c, training.ProposalFilter{Limit: 50})
		return []any{pending, answered}, err
	},
}

func TestReadsSeeNothingOfAnotherUser(t *testing.T) {
	for name, run := range readCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := openTwoUsers(t)
			seedUser(t, store.ForUser(1), "pecho")

			alongside, err := run(ctx, store.ForUser(2))
			if err != nil {
				t.Fatal(err)
			}
			alone, err := Open(freshDB(t))
			if err != nil {
				t.Fatal(err)
			}
			defer alone.Close()
			want, err := run(ctx, alone.ForUser(1))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(alongside, want) {
				t.Fatalf("a user with no data sees %#v beside the owner, %#v on an empty database", alongside, want)
			}
		})
	}
}

// writeCases are the data methods that create rows without taking an existing id.
// Each returns the query counting what it should have written for user 2.
var writeCases = map[string]struct {
	run   func(context.Context, training.UserStore) error
	count string
}{
	"CreatePlan": {
		func(c context.Context, u training.UserStore) error {
			_, err := u.CreatePlan(c, training.Plan{Name: "suya", Items: []training.PlanItem{{Exercise: "sentadilla", TargetSets: 3}}})
			return err
		},
		`SELECT COUNT(*) FROM plans WHERE user_id=2 AND name='suya'`,
	},
	"LogSets": {
		func(c context.Context, u training.UserStore) error {
			_, err := u.LogSets(c, training.LogSetsRecord{Key: "suya", Fingerprint: "f", Date: "2026-09-01", Exercise: "sentadilla",
				Sets: []training.LoggedSet{{WeightKG: 100, Reps: 5, RPE: 8}}})
			return err
		},
		`SELECT COUNT(*) FROM sessions se JOIN sets st ON st.session_id=se.id JOIN idempotency_keys k ON k.user_id=se.user_id AND k.key='suya' WHERE se.user_id=2 AND st.exercise='sentadilla'`,
	},
	"SetLandmarks": {
		func(c context.Context, u training.UserStore) error {
			return u.SetLandmarks(c, training.Landmarks{MuscleGroup: "pierna", MEV: 1, MRV: 2})
		},
		`SELECT COUNT(*) FROM volume_landmarks WHERE user_id=2 AND muscle_group='pierna'`,
	},
	"SetExerciseGroup": {
		func(c context.Context, u training.UserStore) error {
			return u.SetExerciseGroup(c, training.ExerciseGroup{Exercise: "sentadilla", MuscleGroup: "pierna"})
		},
		`SELECT COUNT(*) FROM exercise_groups WHERE user_id=2 AND exercise='sentadilla'`,
	},
	"CreateProposal": {
		func(c context.Context, u training.UserStore) error {
			_, err := u.CreateProposal(c, training.Proposal{Tool: "add_set", ActionType: "set.log", Payload: []byte(`{}`), CreatedAt: time.Unix(0, 0)})
			return err
		},
		`SELECT COUNT(*) FROM proposals WHERE user_id=2 AND tool='add_set'`,
	},
	"AddRule": {
		func(c context.Context, u training.UserStore) error {
			origin, err := u.RecordDecision(c, training.DecisionRecord{Time: time.Unix(0, 0), Permission: "plan.delete", Outcome: training.OutcomeDeny,
				DeciderKind: training.DeciderSubject, DeciderID: training.AccountHolder, Channel: training.ChannelWeb})
			if err != nil {
				return err
			}
			_, err = u.AddRule(c, training.AnswerRule{ActionType: "plan.delete", Effect: training.EffectDeny, Session: "coach:b",
				ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Origin: origin})
			return err
		},
		`SELECT COUNT(*) FROM answer_rules WHERE user_id=2 AND action_type='plan.delete'`,
	},
	"RecordDecision": {
		func(c context.Context, u training.UserStore) error {
			_, err := u.RecordDecision(c, training.DecisionRecord{Time: time.Unix(0, 0), Permission: "plan.delete", Outcome: training.OutcomeDeny,
				DeciderKind: training.DeciderSubject, DeciderID: training.AccountHolder, Channel: training.ChannelWeb})
			return err
		},
		`SELECT COUNT(*) FROM decision_records WHERE user_id=2 AND permission='plan.delete'`,
	},
	"SetAcceptAll": {
		func(c context.Context, u training.UserStore) error { return u.SetAcceptAll(c, true) },
		`SELECT COUNT(*) FROM agent_permissions WHERE user_id=2 AND accept_all=1`,
	},
	"SetExerciseNote": {
		func(c context.Context, u training.UserStore) error {
			return u.SetExerciseNote(c, training.ExerciseNote{Exercise: "sentadilla", Note: "barra baja"})
		},
		`SELECT COUNT(*) FROM exercise_notes WHERE user_id=2 AND exercise='sentadilla'`,
	},
	"SetWeightUnit": {
		func(c context.Context, u training.UserStore) error { return u.SetWeightUnit(c, "kg") },
		`SELECT COUNT(*) FROM users WHERE id=2 AND weight_unit='kg'`,
	},
	"SetRestTarget": {
		func(c context.Context, u training.UserStore) error { return u.SetRestTarget(c, 75) },
		`SELECT COUNT(*) FROM users WHERE id=2 AND rest_seconds=75`,
	},
	// The owner already has a fact under this topic key: user 2 gets their own.
	"SaveMemory": {
		func(c context.Context, u training.UserStore) error {
			_, err := u.SaveMemory(c, training.Memory{TopicKey: "lesion/hombro", Type: training.MemoryNote, Title: "suya", Body: "suya",
				CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil)
			return err
		},
		`SELECT COUNT(*) FROM coach_memory WHERE user_id=2 AND topic_key='lesion/hombro' AND title='suya'`,
	},
}

func TestWritesBelongToTheUserWhoMadeThem(t *testing.T) {
	for name, tc := range writeCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := openTwoUsers(t)
			seedUser(t, store.ForUser(1), "pecho")
			before := dumpUser(t, store, 1)

			if err := tc.run(ctx, store.ForUser(2)); err != nil {
				t.Fatal(err)
			}

			var n int
			if err := store.db.QueryRow(tc.count).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("%s found %d rows", tc.count, n)
			}
			if after := dumpUser(t, store, 1); after != before {
				t.Fatalf("user 1's rows changed:\nbefore\n%s\nafter\n%s", before, after)
			}
		})
	}
}

func TestEverythingSeededByASecondUserIsTheirs(t *testing.T) {
	store := openTwoUsers(t)
	first := seedUser(t, store.ForUser(1), "pecho")
	before := dumpUser(t, store, 1)

	second := seedUser(t, store.ForUser(2), "espalda")

	if second.session == first.session || second.plan == first.plan {
		t.Fatalf("second user reused the first user's rows: %+v, %+v", first, second)
	}
	for _, c := range []struct {
		table string
		want  int
	}{{"sessions", 2}, {"plans", 1}, {"exercise_notes", 1}, {"exercise_groups", 1}, {"volume_landmarks", 1}, {"idempotency_keys", 1}} {
		var n int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + c.table + ` WHERE user_id=2`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != c.want {
			t.Errorf("%s has %d rows for user 2, want %d", c.table, n, c.want)
		}
	}
	if after := dumpUser(t, store, 1); after != before {
		t.Fatalf("user 1's rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// Every statement of the same name must resolve to the user asking, including the
// joins from a set to its muscle group.
func TestSameNamesStaySeparate(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	one, two := store.ForUser(1), store.ForUser(2)
	idsOne := seedUser(t, one, "pecho")
	idsTwo := seedUser(t, two, "espalda")

	history, err := two.ExerciseHistory(ctx, "banca", 50)
	if err != nil {
		t.Fatal(err)
	}
	if history.MuscleGroup != "espalda" || len(history.Sets) != 3 || history.Best == nil || history.Best.SessionID == idsOne.session {
		t.Fatalf("history = %#v", history)
	}
	for _, set := range history.Sets {
		if set.SessionID == idsOne.session {
			t.Fatalf("history carries user 1's session: %#v", set)
		}
	}

	progress, err := two.SessionProgress(ctx, idsTwo.session)
	if err != nil {
		t.Fatal(err)
	}
	if progress[0].Exercise != "banca" || progress[0].MuscleGroup != "espalda" {
		t.Fatalf("planned progress = %#v", progress)
	}
	offPlan, err := two.Start(ctx, training.Session{Date: "2026-09-02"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = two.AddSet(ctx, training.AddSetInput{SessionID: offPlan.ID, Exercise: "banca", WeightKG: 1, Reps: 1, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	progress, err = two.SessionProgress(ctx, offPlan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 1 || progress[0].MuscleGroup != "espalda" {
		t.Fatalf("off-plan progress = %#v", progress)
	}

	volume, err := two.VolumeByGroup(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(volume) != 1 || volume[0].MuscleGroup != "espalda" || volume[0].Sets != 4 {
		t.Fatalf("volume = %#v", volume)
	}
	weeks, err := two.WeeklyVolume(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range weeks {
		if w.MuscleGroup != "espalda" {
			t.Fatalf("weeks = %#v", weeks)
		}
	}

	// Removing a note, like every other write, reaches only the user removing it.
	if err = two.SetExerciseNote(ctx, training.ExerciseNote{Exercise: "banca", Note: ""}); err != nil {
		t.Fatal(err)
	}
	notes, err := one.ExerciseNotes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Note != "agarre de pecho" {
		t.Fatalf("user 1's notes = %#v", notes)
	}
	if notes, err = two.ExerciseNotes(ctx); err != nil || len(notes) != 0 {
		t.Fatalf("user 2's notes = %#v, %v", notes, err)
	}
	if err = two.SetExerciseGroup(ctx, training.ExerciseGroup{Exercise: "banca", MuscleGroup: "pierna"}); err != nil {
		t.Fatal(err)
	}
	groups, err := one.ExerciseGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].MuscleGroup != "pecho" {
		t.Fatalf("user 1's groups = %#v", groups)
	}
	if err = two.SetLandmarks(ctx, training.Landmarks{MuscleGroup: "pecho", MEV: 7, MRV: 9}); err != nil {
		t.Fatal(err)
	}
	marks, err := one.AllLandmarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if marks["pecho"].MEV != 4 {
		t.Fatalf("user 1's landmarks = %#v", marks)
	}
}

func TestIdempotencyKeysArePerUser(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	one, two := store.ForUser(1), store.ForUser(2)
	record := func(weight float64, fingerprint string) training.LogSetsRecord {
		return training.LogSetsRecord{Key: "misma", Fingerprint: fingerprint, Date: sharedDay, Exercise: "banca",
			Sets: []training.LoggedSet{{WeightKG: weight, Reps: 5, RPE: 8}}}
	}
	first, err := one.LogSets(ctx, record(80, "uno"))
	if err != nil {
		t.Fatal(err)
	}

	// The same key from another user is new work: not a replay, not a conflict.
	second, err := two.LogSets(ctx, record(50, "dos"))
	if err != nil {
		t.Fatalf("second user with the same key: %v", err)
	}
	if second.Replayed || second.Session.ID == first.Session.ID || second.Sets[0].WeightKG != 50 {
		t.Fatalf("second = %#v, first = %#v", second, first)
	}

	// Each user's retry gets their own stored answer back.
	replayOne, err := one.LogSets(ctx, record(80, "uno"))
	if err != nil {
		t.Fatal(err)
	}
	if !replayOne.Replayed || replayOne.Sets[0].ID != first.Sets[0].ID || replayOne.Sets[0].WeightKG != 80 {
		t.Fatalf("user 1 replay = %#v", replayOne)
	}
	replayTwo, err := two.LogSets(ctx, record(50, "dos"))
	if err != nil {
		t.Fatal(err)
	}
	if !replayTwo.Replayed || replayTwo.Sets[0].ID != second.Sets[0].ID || replayTwo.Sets[0].WeightKG != 50 {
		t.Fatalf("user 2 replay = %#v", replayTwo)
	}

	// A conflict is judged against the asking user's own key.
	if _, err = two.LogSets(ctx, record(50, "tres")); !errors.Is(err, training.ErrIdempotencyConflict) {
		t.Fatalf("conflict = %v", err)
	}
	var keys, stored int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM idempotency_keys WHERE key='misma'`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM sets`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if keys != 2 || stored != 2 {
		t.Fatalf("keys = %d, sets = %d", keys, stored)
	}
}

func TestLogSetsFindsOnlyTheUsersOwnSessionForTheDay(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	mine, err := store.ForUser(1).Start(ctx, training.Session{Date: sharedDay})
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.ForUser(2).LogSets(ctx, training.LogSetsRecord{Key: "k", Fingerprint: "f", Date: sharedDay, Exercise: "banca",
		Sets: []training.LoggedSet{{WeightKG: 50, Reps: 5, RPE: 8}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Session.ID == mine.ID {
		t.Fatalf("user 2 logged into user 1's session %d", mine.ID)
	}
	var owner int64
	if err = store.db.QueryRow(`SELECT user_id FROM sessions WHERE id=?`, out.Session.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != 2 {
		t.Fatalf("session owner = %d", owner)
	}
}

func TestStartingFromAPlanCopiesOnlyTheUsersOwnPlan(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	ids := seedUser(t, store.ForUser(1), "pecho")
	before := dumpAll(t, store)

	// Naming the plan does not skip the check that it is the user's to start from.
	_, err := store.ForUser(2).Start(ctx, training.Session{Date: sharedDay, PlanID: ids.plan, PlanName: "empuje"})
	if !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("Start = %v, want not found", err)
	}
	if after := dumpAll(t, store); after != before {
		t.Fatalf("rows changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// A method added to the port later must say how it is isolated, or this fails.
func TestEveryDataMethodHasATwoUserCase(t *testing.T) {
	covered := map[string]bool{}
	for name := range idCases {
		covered[name] = true
	}
	for name := range readCases {
		covered[name] = true
	}
	for name := range writeCases {
		covered[name] = true
	}
	port := reflect.TypeFor[training.UserStore]()
	for i := range port.NumMethod() {
		if name := port.Method(i).Name; !covered[name] {
			t.Errorf("%s has no two-user case", name)
		}
	}
	for name := range covered {
		if _, ok := port.MethodByName(name); !ok {
			t.Errorf("case %s names no method of UserStore", name)
		}
	}
}
