package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
)

func TestCoachConversationsArePerPersonAndOrdered(t *testing.T) {
	ctx := context.Background()
	st := openTwoUsers(t)
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	a, err := st.CreateCoachConversation(ctx, 1, "Pierna", t0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := st.CreateCoachConversation(ctx, 1, "Espalda", t0.Add(time.Minute))
	for i, m := range []engine.ConversationMessage{
		{Role: "user", Text: "¿qué hago?", Photos: 2, At: t0.Add(2 * time.Minute)},
		{Role: "assistant", Text: "Sentadilla", Steps: []string{"get_session:ok", "create_plan:pending"}, At: t0.Add(3 * time.Minute)},
	} {
		if err := st.AppendCoachMessage(ctx, 1, a, m); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	list, err := st.CoachConversations(ctx, 1, t0.Add(-time.Hour))
	if err != nil || len(list) != 2 || list[0].ID != a || list[1].ID != b || !list[0].UpdatedAt.Equal(t0.Add(3*time.Minute)) {
		t.Fatalf("list = %+v %v", list, err)
	}
	conv, msgs, err := st.CoachConversation(ctx, 1, a, t0.Add(-time.Hour))
	if err != nil || conv.Title != "Pierna" || len(msgs) != 2 || msgs[0].Photos != 2 || msgs[1].Steps[1] != "create_plan:pending" || msgs[1].Role != "assistant" {
		t.Fatalf("conversation = %+v %+v %v", conv, msgs, err)
	}
	// The other person sees nothing of it and can change nothing.
	if l, _ := st.CoachConversations(ctx, 2, t0.Add(-time.Hour)); len(l) != 0 {
		t.Fatalf("person 2 lists %v", l)
	}
	for name, err := range map[string]error{
		"read":   second(st.CoachConversation(ctx, 2, a, t0.Add(-time.Hour))),
		"append": st.AppendCoachMessage(ctx, 2, a, engine.ConversationMessage{Role: "user", Text: "x", At: t0}),
		"rename": st.RenameCoachConversation(ctx, 2, a, "mía"),
		"delete": st.DeleteCoachConversation(ctx, 2, a),
	} {
		if !errors.Is(err, engine.ErrNoConversation) {
			t.Errorf("person 2 %s: %v", name, err)
		}
	}
	if err := st.DeleteCoachConversations(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.CoachConversations(ctx, 1, t0.Add(-time.Hour)); len(l) != 2 {
		t.Fatalf("person 2's «borrar todo» touched person 1: %v", l)
	}
	if err := st.RenameCoachConversation(ctx, 1, a, "Pierna nueva"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteCoachConversation(ctx, 1, b); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.CoachConversations(ctx, 1, t0.Add(-time.Hour)); len(l) != 1 || l[0].Title != "Pierna nueva" {
		t.Fatalf("after rename and delete: %v", l)
	}
	if err := st.DeleteCoachConversations(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.CoachConversations(ctx, 1, t0.Add(-time.Hour)); len(l) != 0 {
		t.Fatalf("after «borrar todo»: %v", l)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM coach_messages`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d messages left behind", n)
	}
}

// 90 days after the last message, a conversation goes; training data stays.
func TestCoachPurgeDeletesOnlyOldConversations(t *testing.T) {
	ctx := context.Background()
	st := openTwoUsers(t)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-90 * 24 * time.Hour)
	old, _ := st.CreateCoachConversation(ctx, 1, "vieja", cutoff.Add(-time.Hour))
	edge, _ := st.CreateCoachConversation(ctx, 2, "justo", cutoff)
	revived, _ := st.CreateCoachConversation(ctx, 1, "revivida", cutoff.Add(-24*time.Hour))
	st.AppendCoachMessage(ctx, 1, revived, engine.ConversationMessage{Role: "user", Text: "sigo", At: now.Add(-time.Hour)})
	users := countRows(t, st, "SELECT COUNT(*) FROM users")
	if n, err := st.PurgeCoachConversations(ctx, cutoff); err != nil || n != 1 {
		t.Fatalf("purged %d, %v", n, err)
	}
	if _, _, err := st.CoachConversation(ctx, 1, old, time.Time{}); !errors.Is(err, engine.ErrNoConversation) {
		t.Fatalf("old conversation survived: %v", err)
	}
	for _, id := range []struct{ user, conv int64 }{{2, edge}, {1, revived}} {
		if _, _, err := st.CoachConversation(ctx, id.user, id.conv, time.Time{}); err != nil {
			t.Fatalf("conversation %d purged: %v", id.conv, err)
		}
	}
	// Listing hides what is past the cutoff even before a purge runs.
	if l, _ := st.CoachConversations(ctx, 1, now.Add(-time.Hour+time.Second)); len(l) != 0 {
		t.Fatalf("list ignores since: %v", l)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM users"); got != users {
		t.Fatalf("purge touched people: %d -> %d", users, got)
	}
}

func second[A, B any](_ A, _ B, err error) error { return err }
