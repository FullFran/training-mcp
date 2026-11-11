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

func TestMemorySaveIsAProposalOfTypeMemoryUntilAccepted(t *testing.T) {
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
	if err != nil || p.ActionType != "memory" || p.Tool != "memory_save" {
		t.Fatalf("proposal = %#v, %v", p, err)
	}

	// Accepting it with "always" stores it and lets later memory writes through.
	if _, err = svc.AcceptProposal(ctx, p.ID, training.RememberAlways, world.server.Apply); err != nil {
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

func TestMemoryForgetIsAProposalAndDeletesOnlyWhenAccepted(t *testing.T) {
	ctx := context.Background()
	world := newMemoryWorld(t)
	svc := world.services.ForUser(1)
	m, err := svc.SaveMemory(ctx, training.MemoryInput{Title: "Rodilla", Body: "dolor"})
	if err != nil {
		t.Fatal(err)
	}
	session := mustConnect(t, world.server.Handler())

	out := structured(t, call(t, session, "memory_forget", map[string]any{"memory_id": m.ID}))
	if out["status"] != "pending" || world.count(t, "1") != 1 {
		t.Fatalf("out = %v", out)
	}
	p, err := svc.GetProposal(ctx, int64(out["proposal_id"].(float64)))
	if err != nil || p.ActionType != "memory" {
		t.Fatalf("proposal = %#v, %v", p, err)
	}
	if _, err = svc.AcceptProposal(ctx, p.ID, training.RememberOnce, world.server.Apply); err != nil {
		t.Fatal(err)
	}
	if world.count(t, "1") != 0 {
		t.Fatal("accepted forget left the memory")
	}
}

func TestMemoryReadsFindGetAndSummarise(t *testing.T) {
	ctx := context.Background()
	world := newMemoryWorld(t)
	svc := world.services.ForUser(1)
	pinned := true
	knee, err := svc.SaveMemory(ctx, training.MemoryInput{Type: "limitation", Title: "Rodilla", Body: strings.Repeat("dolor ", 100), Pinned: &pinned})
	if err != nil {
		t.Fatal(err)
	}
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
	owner, err := world.services.ForUser(1).SaveMemory(ctx, training.MemoryInput{Title: "secreto", Body: "secreto de uno"})
	if err != nil {
		t.Fatal(err)
	}
	if err = world.services.ForUser(2).SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	second := mustConnect(t, New(only(world.services.ForUser(2))).Handler())
	before := world.count(t, "user_id=1 AND body='secreto de uno'")

	for name, args := range map[string]map[string]any{
		"memory_search":  {"query": "secreto", "user_id": 1},
		"memory_context": {"user_id": 1},
		"memory_get":     {"memory_id": owner.ID, "user_id": 1},
		"memory_forget":  {"memory_id": owner.ID, "user_id": 1},
		"memory_save":    {"topic_key": owner.TopicKey, "title": "robado", "body": "robado", "user_id": 1},
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
