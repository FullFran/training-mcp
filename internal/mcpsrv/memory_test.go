package mcpsrv

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

type memoryWorld struct {
	db       *sql.DB
	server   *Server
	services *training.Services
}

// newMemoryWorld has user 1, who asks before every agent write, and user 2.
func newMemoryWorld(t *testing.T) memoryWorld {
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
	if _, err = db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	services := training.NewServices(store, isolationClock)
	if err = services.ForUser(1).SetAcceptAll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return memoryWorld{db: db, services: services, server: New(func(context.Context) *training.Service { return services.ForUser(1) })}
}

func (w memoryWorld) count(t *testing.T, where string) int {
	t.Helper()
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM coach_memory WHERE ` + where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMemorySaveIsAProposalOfItsOwnTypeUntilAccepted(t *testing.T) {
	ctx := context.Background()
	world := newMemoryWorld(t)
	session := mustConnect(t, world.server.Handler())
	args := map[string]any{"topic_key": "lesion/hombro", "type": "limitation", "title": "Hombro", "body": "Duele en press militar"}

	out := structured(t, call(t, session, "memory_save", args))
	if out["status"] != "pending" || world.count(t, "1") != 0 {
		t.Fatalf("out = %v, rows = %d", out, world.count(t, "1"))
	}
	svc := world.services.ForUser(1)
	p, err := svc.GetProposal(ctx, int64(out["proposal_id"].(float64)))
	if err != nil || p.ActionType != "memory.save" || p.Tool != "memory_save" {
		t.Fatalf("proposal = %#v, %v", p, err)
	}

	// Accepting it with "always" stores it and lets later saves through.
	if _, err = svc.AcceptProposal(ctx, p.ID, training.AnswerAllowAlways, world.server.Apply); err != nil {
		t.Fatal(err)
	}
	if world.count(t, "user_id=1 AND topic_key='lesion/hombro' AND body='Duele en press militar'") != 1 {
		t.Fatal("accepted memory not stored")
	}
	args["body"] = "Ya no duele"
	out = structured(t, call(t, session, "memory_save", args))
	if out["status"] != "accepted" {
		t.Fatalf("second save = %v", out)
	}
	memory := out["memory"].(map[string]any)
	if memory["body"] != "Ya no duele" || world.count(t, "user_id=1") != 1 {
		t.Fatalf("memory = %v, rows = %d", memory, world.count(t, "user_id=1"))
	}
}

func TestMemoryForgetIsAProposalAndForgetsOnlyWhenAccepted(t *testing.T) {
	ctx := context.Background()
	world := newMemoryWorld(t)
	svc := world.services.ForUser(1)
	saved, err := svc.SaveMemory(ctx, training.MemoryInput{Title: "Rodilla", Body: "dolor"})
	if err != nil {
		t.Fatal(err)
	}
	m := saved.Memory
	session := mustConnect(t, world.server.Handler())

	out := structured(t, call(t, session, "memory_forget", map[string]any{"memory_id": m.ID}))
	if out["status"] != "pending" || world.count(t, "1") != 1 {
		t.Fatalf("out = %v", out)
	}
	p, err := svc.GetProposal(ctx, int64(out["proposal_id"].(float64)))
	if err != nil || p.ActionType != "memory.forget" {
		t.Fatalf("proposal = %#v, %v", p, err)
	}
	if _, err = svc.AcceptProposal(ctx, p.ID, training.AnswerAllowOnce, world.server.Apply); err != nil {
		t.Fatal(err)
	}
	// Forgetting is soft: the fact stays, out of context and search, for the person to see.
	if world.count(t, "deleted_at<>'' AND forget_reason='forgotten'") != 1 {
		t.Fatal("accepted forget did not forget softly")
	}
	if found := structured(t, call(t, session, "memory_search", map[string]any{"query": "rodilla"}))["memories"].([]any); len(found) != 0 {
		t.Fatalf("search still finds it: %v", found)
	}
}

func TestMemoryReadsFindGetAndSummarise(t *testing.T) {
	ctx := context.Background()
	world := newMemoryWorld(t)
	svc := world.services.ForUser(1)
	pinned := true
	saved, err := svc.SaveMemory(ctx, training.MemoryInput{Type: "limitation", Title: "Rodilla", Body: strings.Repeat("dolor ", 100), Pinned: &pinned})
	if err != nil {
		t.Fatal(err)
	}
	knee := saved.Memory
	if _, err = svc.SaveMemory(ctx, training.MemoryInput{Type: "goal", Title: "Sentadilla 140", Body: "para marzo"}); err != nil {
		t.Fatal(err)
	}
	session := mustConnect(t, world.server.Handler())

	found := structured(t, call(t, session, "memory_search", map[string]any{"query": "sentadilla"}))["memories"].([]any)
	if len(found) != 1 || found[0].(map[string]any)["title"] != "Sentadilla 140" {
		t.Fatalf("search = %v", found)
	}
	typed := structured(t, call(t, session, "memory_search", map[string]any{"type": "limitation"}))["memories"].([]any)
	if len(typed) != 1 {
		t.Fatalf("typed list = %v", typed)
	}
	got := structured(t, call(t, session, "memory_get", map[string]any{"memory_id": knee.ID}))["memory"].(map[string]any)
	if got["body"] != knee.Body {
		t.Fatal("memory_get must return the whole body")
	}
	compact := structured(t, call(t, session, "memory_context", map[string]any{}))["memories"].([]any)
	first := compact[0].(map[string]any)
	if len(compact) != 2 || first["title"] != "Rodilla" || first["pinned"] != true || len([]rune(first["body"].(string))) > 281 {
		t.Fatalf("context = %v", compact)
	}
}

// A user id in the arguments is never trusted: the person is whoever the request
// authenticated as, so user 2 naming user 1 neither reads nor writes user 1's memory.
func TestMemoryToolsNeverTrustAUserIDFromArguments(t *testing.T) {
	ctx := context.Background()
	world := newMemoryWorld(t)
	saved, err := world.services.ForUser(1).SaveMemory(ctx, training.MemoryInput{Title: "secreto", Body: "secreto de uno"})
	if err != nil {
		t.Fatal(err)
	}
	owner := saved.Memory
	if err = world.services.ForUser(2).SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	second := mustConnect(t, New(only(world.services.ForUser(2))).Handler())
	before := world.count(t, "user_id=1 AND body='secreto de uno'")

	for name, args := range map[string]map[string]any{
		"memory_search":              {"query": "secreto", "user_id": 1},
		"memory_context":             {"user_id": 1},
		"memory_get":                 {"memory_id": owner.ID, "user_id": 1},
		"memory_forget":              {"memory_id": owner.ID, "user_id": 1},
		"memory_save":                {"topic_key": owner.TopicKey, "title": "robado", "body": "robado", "user_id": 1},
		"memory_confirm":             {"memory_id": owner.ID, "user_id": 1},
		"memory_consolidate_preview": {"memory_ids": []int64{owner.ID, owner.ID + 1}, "title": "t", "body": "b", "user_id": 1},
	} {
		result := call(t, second, name, args)
		if text := answer(t, result); strings.Contains(text, "secreto") {
			t.Errorf("%s answered with user 1's memory: %s", name, text)
		}
	}
	if world.count(t, "user_id=1 AND body='secreto de uno'") != before || world.count(t, "user_id=1") != 1 {
		t.Fatal("user 1's memory changed")
	}
	if world.count(t, "user_id=2 AND title='robado'") > 1 {
		t.Fatal("user 2's save landed more than once")
	}
}

func TestMemorySaveRejectsAnUnknownType(t *testing.T) {
	world := newMemoryWorld(t)
	session := mustConnect(t, world.server.Handler())
	if result := call(t, session, "memory_save", map[string]any{"type": "secret", "title": "t", "body": "b"}); !result.IsError {
		t.Fatalf("unknown type accepted: %s", answer(t, result))
	}
}

// "Always" on saving never lets the coach forget without asking: the two writes
// are separate action types.
func TestAlwaysOnSavingDoesNotCoverForgetting(t *testing.T) {
	ctx := context.Background()
	world := newMemoryWorld(t)
	svc := world.services.ForUser(1)
	session := mustConnect(t, world.server.Handler())

	saved := structured(t, call(t, session, "memory_save", map[string]any{"title": "Rodilla", "body": "dolor"}))
	if _, err := svc.AcceptProposal(ctx, int64(saved["proposal_id"].(float64)), training.AnswerAllowAlways, world.server.Apply); err != nil {
		t.Fatal(err)
	}
	m, err := svc.SearchMemory(ctx, training.MemoryQuery{Text: "rodilla"})
	if err != nil || len(m) != 1 {
		t.Fatalf("saved = %v, %v", m, err)
	}

	out := structured(t, call(t, session, "memory_forget", map[string]any{"memory_id": m[0].ID}))
	if out["status"] != "pending" || world.count(t, "1") != 1 {
		t.Fatalf("forget after always-save = %v, rows = %d", out, world.count(t, "1"))
	}
	p, err := svc.GetProposal(ctx, int64(out["proposal_id"].(float64)))
	if err != nil || p.ActionType != "memory.forget" {
		t.Fatalf("forget proposal = %#v, %v", p, err)
	}
}

// newAcceptingWorld is newMemoryWorld with user 1 accepting every agent write.
func newAcceptingWorld(t *testing.T) (memoryWorld, *training.Service) {
	t.Helper()
	world := newMemoryWorld(t)
	svc := world.services.ForUser(1)
	if err := svc.SetAcceptAll(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	return world, svc
}

func TestSavingReturnsSimilarFactsAndJudgingSupersedes(t *testing.T) {
	world, _ := newAcceptingWorld(t)
	session := mustConnect(t, world.server.Handler())
	old := structured(t, call(t, session, "memory_save", map[string]any{"type": "limitation", "title": "Hombro lesionado", "body": "nada de press"}))
	if old["judgment_required"] != false || len(old["candidates"].([]any)) != 0 {
		t.Fatalf("first save = %v", old)
	}
	saved := structured(t, call(t, session, "memory_save", map[string]any{"type": "limitation", "title": "Hombro recuperado", "body": "press ligero"}))
	candidates := saved["candidates"].([]any)
	if saved["judgment_required"] != true || len(candidates) != 1 {
		t.Fatalf("second save = %v", saved)
	}
	candidate := candidates[0].(map[string]any)
	if candidate["memory"].(map[string]any)["title"] != "Hombro lesionado" {
		t.Fatalf("candidate = %v", candidate)
	}
	judged := structured(t, call(t, session, "memory_judge", map[string]any{"relation_id": candidate["relation_id"], "relation": "supersedes", "reason": "ya se recuperó"}))
	if judged["status"] != "accepted" || judged["relation"].(map[string]any)["relation"] != "supersedes" {
		t.Fatalf("judge = %v", judged)
	}
	if active := structured(t, call(t, session, "memory_context", map[string]any{}))["memories"].([]any); len(active) != 1 {
		t.Fatalf("context keeps the superseded fact: %v", active)
	}
	if found := structured(t, call(t, session, "memory_search", map[string]any{"query": "hombro"}))["memories"].([]any); len(found) != 1 {
		t.Fatalf("search keeps the superseded fact: %v", found)
	}
	if found := structured(t, call(t, session, "memory_search", map[string]any{"view": "superseded"}))["memories"].([]any); len(found) != 1 {
		t.Fatalf("superseded view = %v", found)
	}
	if result := call(t, session, "memory_judge", map[string]any{"relation_id": candidate["relation_id"], "relation": "related"}); !result.IsError {
		t.Fatalf("judging twice: %s", answer(t, result))
	}
}

func TestContextSurfacesDueFactsAndConfirmingRestartsTheCycle(t *testing.T) {
	world, svc := newAcceptingWorld(t)
	session := mustConnect(t, world.server.Handler())
	saved, err := svc.SaveMemory(context.Background(), training.MemoryInput{Type: "limitation", Title: "Hombro", Body: "molesta en press"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = world.db.Exec(`UPDATE coach_memory SET review_after='2026-08-01T00:00:00.000000000Z'`); err != nil {
		t.Fatal(err)
	}
	due := structured(t, call(t, session, "memory_context", map[string]any{}))["due"].([]any)
	if len(due) != 1 || due[0].(map[string]any)["title"] != "Hombro" {
		t.Fatalf("due = %v", due)
	}
	confirmed := structured(t, call(t, session, "memory_confirm", map[string]any{"memory_id": saved.Memory.ID}))
	if confirmed["status"] != "accepted" || confirmed["memory"].(map[string]any)["review_after"] != "2026-08-21T10:00:00Z" {
		t.Fatalf("confirm = %v", confirmed)
	}
	if due = structured(t, call(t, session, "memory_context", map[string]any{}))["due"].([]any); len(due) != 0 {
		t.Fatalf("still due: %v", due)
	}
}

func TestConsolidationNeedsThePreviewsFingerprint(t *testing.T) {
	ctx := context.Background()
	world, svc := newAcceptingWorld(t)
	session := mustConnect(t, world.server.Handler())
	var ids []int64
	for _, title := range []string{"Hombro", "Hombro derecho"} {
		saved, err := svc.SaveMemory(ctx, training.MemoryInput{TopicKey: title, Type: "limitation", Title: title, Body: "duele"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, saved.Memory.ID)
	}
	args := map[string]any{"memory_ids": ids, "type": "limitation", "title": "Hombro derecho", "body": "duele en press"}
	plan := structured(t, call(t, session, "memory_consolidate_preview", args))
	if len(plan["sources"].([]any)) != 2 || plan["fingerprint"] == "" || world.count(t, "deleted_at<>''") != 0 {
		t.Fatalf("preview = %v", plan)
	}
	if result := call(t, session, "memory_consolidate", map[string]any{"memory_ids": ids, "type": "limitation", "title": "Hombro derecho", "body": "otra cosa", "fingerprint": plan["fingerprint"]}); !result.IsError {
		t.Fatalf("a changed plan was merged: %s", answer(t, result))
	}
	if world.count(t, "deleted_at<>''") != 0 {
		t.Fatal("a refused consolidation forgot facts")
	}
	args["fingerprint"] = plan["fingerprint"]
	done := structured(t, call(t, session, "memory_consolidate", args))
	if done["status"] != "accepted" || world.count(t, "deleted_at<>'' AND forget_reason='consolidated'") != 2 || world.count(t, "deleted_at=''") != 1 {
		t.Fatalf("consolidate = %v", done)
	}
	p, err := svc.GetProposal(ctx, int64(done["proposal_id"].(float64)))
	if err != nil || p.ActionType != "memory.consolidate" {
		t.Fatalf("proposal = %#v, %v", p, err)
	}
}

func TestGetReturnsTheFactsHistory(t *testing.T) {
	ctx := context.Background()
	world, svc := newAcceptingWorld(t)
	for _, body := range []string{"duele", "ya no duele"} {
		if _, err := svc.SaveMemory(ctx, training.MemoryInput{TopicKey: "k", Title: "Hombro", Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	session := mustConnect(t, world.server.Handler())
	got := structured(t, call(t, session, "memory_get", map[string]any{"memory_id": 1}))
	revisions := got["revisions"].([]any)
	if got["memory"].(map[string]any)["revision"] != 2.0 || len(revisions) != 1 || revisions[0].(map[string]any)["body"] != "duele" {
		t.Fatalf("get = %v", got)
	}
}
