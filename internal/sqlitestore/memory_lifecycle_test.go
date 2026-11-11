package sqlitestore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

func save(t *testing.T, u training.UserStore, m training.Memory) training.MemorySaved {
	t.Helper()
	if m.Type == "" {
		m.Type = training.MemoryNote
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt, m.UpdatedAt = memoryAt(0), memoryAt(0)
	}
	saved, err := u.SaveMemory(context.Background(), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func listView(t *testing.T, u training.UserStore, view string, now time.Time) []string {
	t.Helper()
	got, err := u.ListMemory(context.Background(), training.MemoryQuery{View: view, Limit: 50, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return titles(got)
}

func TestChangingAFactKeepsItsPreviousTextAsARevision(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	first := save(t, u, training.Memory{TopicKey: "k", Title: "Hombro", Body: "duele"}).Memory
	if first.Revision != 1 {
		t.Fatalf("new fact revision = %d", first.Revision)
	}
	second := save(t, u, training.Memory{TopicKey: "k", Title: "Hombro", Body: "ya no duele", CreatedAt: memoryAt(5), UpdatedAt: memoryAt(5)}).Memory
	// The same text again is a confirmation, not a revision.
	same := save(t, u, training.Memory{TopicKey: "k", Title: "Hombro", Body: "ya no duele", CreatedAt: memoryAt(6), UpdatedAt: memoryAt(6)}).Memory
	if second.ID != first.ID || second.Revision != 2 || same.Revision != 2 {
		t.Fatalf("revisions %d then %d", second.Revision, same.Revision)
	}
	body := "molesta un poco"
	edited, err := u.UpdateMemory(ctx, first.ID, training.MemoryPatch{Body: &body, UpdatedAt: memoryAt(9)})
	if err != nil || edited.Revision != 3 {
		t.Fatalf("edit = %#v, %v", edited, err)
	}
	history, err := u.MemoryHistory(ctx, first.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %#v, %v", history, err)
	}
	if history[0].Revision != 1 || history[0].Body != "duele" || !history[0].ReplacedAt.Equal(memoryAt(5)) || history[1].Body != "ya no duele" {
		t.Fatalf("history = %#v", history)
	}
}

func TestSavingSurfacesSimilarFactsOnceUntilJudged(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	old := save(t, u, training.Memory{TopicKey: "a", Title: "Dolor de hombro", Body: "en press"})
	if len(old.Candidates) != 0 {
		t.Fatalf("first fact has candidates %#v", old.Candidates)
	}
	save(t, u, training.Memory{TopicKey: "b", Title: "Rodilla", Body: "nada que ver"})

	saved := save(t, u, training.Memory{TopicKey: "c", Title: "Hombro derecho", Body: "mejor"})
	if len(saved.Candidates) != 1 || saved.Candidates[0].Memory.ID != old.Memory.ID || saved.Candidates[0].RelationID == 0 {
		t.Fatalf("candidates = %#v", saved.Candidates)
	}
	// Saving again offers the same pending relation, not a new one.
	again := save(t, u, training.Memory{TopicKey: "c", Title: "Hombro derecho", Body: "mucho mejor"})
	if len(again.Candidates) != 1 || again.Candidates[0].RelationID != saved.Candidates[0].RelationID {
		t.Fatalf("second save candidates = %#v", again.Candidates)
	}
	if _, err := u.JudgeMemory(ctx, saved.Candidates[0].RelationID, training.RelationRelated, "mismo hombro", memoryAt(1)); err != nil {
		t.Fatal(err)
	}
	if judged := save(t, u, training.Memory{TopicKey: "c", Title: "Hombro derecho", Body: "bien"}); len(judged.Candidates) != 0 {
		t.Fatalf("a judged pair came back: %#v", judged.Candidates)
	}
	if _, err := u.JudgeMemory(ctx, saved.Candidates[0].RelationID, training.RelationRelated, "", memoryAt(2)); !errors.Is(err, training.ErrNotPending) {
		t.Fatalf("judging twice err = %v", err)
	}
}

func TestSupersedingRetiresTheOldFactFromContextAndSearch(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	old := save(t, u, training.Memory{TopicKey: "a", Title: "Hombro lesionado", Body: "no press"}).Memory
	saved := save(t, u, training.Memory{TopicKey: "b", Title: "Hombro recuperado", Body: "press ligero"})
	rel, err := u.JudgeMemory(ctx, saved.Candidates[0].RelationID, training.RelationSupersedes, "", memoryAt(1))
	if err != nil || rel.Relation != training.RelationSupersedes || rel.Status != training.RelationJudged {
		t.Fatalf("judge = %#v, %v", rel, err)
	}
	if got := listView(t, u, training.MemoryViewActive, memoryAt(0)); len(got) != 1 || got[0] != "Hombro recuperado" {
		t.Fatalf("active = %v", got)
	}
	if found, _ := u.ListMemory(ctx, training.MemoryQuery{Text: "hombro", View: training.MemoryViewActive, Limit: 10}); len(found) != 1 {
		t.Fatalf("search finds %v", titles(found))
	}
	if got := listView(t, u, training.MemoryViewSuperseded, memoryAt(0)); len(got) != 1 || got[0] != "Hombro lesionado" {
		t.Fatalf("superseded = %v", got)
	}
	kept, err := u.GetMemory(ctx, old.ID)
	if err != nil || kept.SupersededBy != saved.Memory.ID {
		t.Fatalf("old fact = %#v, %v", kept, err)
	}
}

func TestConflictsWaitForThePersonUntilResolved(t *testing.T) {
	ctx := context.Background()
	for keep, active := range map[string][]string{
		training.KeepNew:  {"Prefiere mañanas"},
		training.KeepOld:  {"Prefiere tardes"},
		training.KeepBoth: {"Prefiere mañanas", "Prefiere tardes"},
	} {
		t.Run(keep, func(t *testing.T) {
			u := openTwoUsers(t).ForUser(1)
			save(t, u, training.Memory{TopicKey: "a", Title: "Prefiere tardes", Body: "entrena a las 19"})
			saved := save(t, u, training.Memory{TopicKey: "b", Title: "Prefiere mañanas", Body: "entrena a las 7", CreatedAt: memoryAt(1), UpdatedAt: memoryAt(1)})
			id := saved.Candidates[0].RelationID
			if _, err := u.JudgeMemory(ctx, id, training.RelationConflictsWith, "horarios opuestos", memoryAt(2)); err != nil {
				t.Fatal(err)
			}
			conflicts, err := u.ListMemoryConflicts(ctx)
			if err != nil || len(conflicts) != 1 || conflicts[0].New.Title != "Prefiere mañanas" || conflicts[0].Old.Title != "Prefiere tardes" || conflicts[0].Relation.Reason != "horarios opuestos" {
				t.Fatalf("conflicts = %#v, %v", conflicts, err)
			}
			if err = u.ResolveMemoryConflict(ctx, id, keep, memoryAt(3)); err != nil {
				t.Fatal(err)
			}
			if got := listView(t, u, training.MemoryViewActive, memoryAt(0)); len(got) != len(active) || got[0] != active[0] {
				t.Fatalf("active = %v, want %v", got, active)
			}
			if conflicts, _ = u.ListMemoryConflicts(ctx); len(conflicts) != 0 {
				t.Fatalf("still listed: %#v", conflicts)
			}
			if err = u.ResolveMemoryConflict(ctx, id, keep, memoryAt(4)); !errors.Is(err, training.ErrNotPending) {
				t.Fatalf("resolving twice err = %v", err)
			}
		})
	}
}

func TestDueFactsAreThoseWhoseReviewDatePassed(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	soon := save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "duele", ReviewAfter: memoryAt(10)}).Memory
	save(t, u, training.Memory{TopicKey: "b", Title: "Mancuernas", Body: "hasta 30 kg", ReviewAfter: memoryAt(50)})
	if got := listView(t, u, training.MemoryViewDue, memoryAt(9)); len(got) != 0 {
		t.Fatalf("due too early: %v", got)
	}
	if got := listView(t, u, training.MemoryViewDue, memoryAt(10)); len(got) != 1 || got[0] != "Hombro" {
		t.Fatalf("due = %v", got)
	}
	confirmed, err := u.ConfirmMemory(ctx, soon.ID, memoryAt(30))
	if err != nil || !confirmed.ReviewAfter.Equal(memoryAt(30)) || confirmed.Revision != 1 {
		t.Fatalf("confirm = %#v, %v", confirmed, err)
	}
	if got := listView(t, u, training.MemoryViewDue, memoryAt(20)); len(got) != 0 {
		t.Fatalf("still due after confirming: %v", got)
	}
}

func TestForgettingIsSoftAndDeletingIsForGood(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	m := save(t, u, training.Memory{TopicKey: "a", Title: "Rodilla", Body: "dolor"}).Memory
	gone, err := u.ForgetMemory(ctx, m.ID, training.MemoryForgotten, memoryAt(4))
	if err != nil || !gone.DeletedAt.Equal(memoryAt(4)) || gone.ForgetReason != training.MemoryForgotten {
		t.Fatalf("forget = %#v, %v", gone, err)
	}
	if _, err = u.GetMemory(ctx, m.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("get forgotten err = %v", err)
	}
	if found, _ := u.ListMemory(ctx, training.MemoryQuery{Text: "rodilla", View: training.MemoryViewActive, Limit: 10}); len(found) != 0 {
		t.Fatalf("search finds a forgotten fact: %v", titles(found))
	}
	if got := listView(t, u, training.MemoryViewForgotten, memoryAt(0)); len(got) != 1 {
		t.Fatalf("forgotten = %v", got)
	}
	if _, err = u.ForgetMemory(ctx, m.ID, training.MemoryForgotten, memoryAt(5)); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("forgetting twice err = %v", err)
	}
	// The person deletes it for good, history and relations included.
	if _, err = u.DeleteMemory(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if got := listView(t, u, training.MemoryViewForgotten, memoryAt(0)); len(got) != 0 {
		t.Fatalf("deleted fact still listed: %v", got)
	}
}

func TestSavingAForgottenKeyBringsTheFactBack(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	m := save(t, u, training.Memory{TopicKey: "a", Title: "Rodilla", Body: "dolor"}).Memory
	if _, err := u.ForgetMemory(ctx, m.ID, training.MemoryForgotten, memoryAt(1)); err != nil {
		t.Fatal(err)
	}
	back := save(t, u, training.Memory{TopicKey: "a", Title: "Rodilla", Body: "vuelve el dolor"}).Memory
	if back.ID != m.ID || !back.DeletedAt.IsZero() || back.ForgetReason != "" || back.Revision != 2 {
		t.Fatalf("back = %#v", back)
	}
}

func TestDeletingEverythingRemovesHistoryAndRelationsToo(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	u := store.ForUser(1)
	save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "uno"})
	save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "dos"})
	save(t, u, training.Memory{TopicKey: "b", Title: "Hombro bis", Body: "tres"})
	n, err := u.DeleteAllMemory(ctx)
	if err != nil || n != 2 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	for _, table := range []string{"coach_memory", "coach_memory_revisions", "coach_memory_relations", "coach_memory_fts"} {
		var left int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&left); err != nil || left != 0 {
			t.Errorf("%s keeps %d rows, %v", table, left, err)
		}
	}
}

func TestConsolidationMergesIntoOneSurvivorWithProvenance(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	a := save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "duele"}).Memory
	b := save(t, u, training.Memory{TopicKey: "b", Title: "Hombro derecho", Body: "en press", Pinned: true}).Memory
	survivor, err := u.ConsolidateMemory(ctx, training.MemoryConsolidation{
		Sources:  map[int64]int{a.ID: 1, b.ID: 1},
		Survivor: training.Memory{TopicKey: "lesion/hombro", Type: training.MemoryLimitation, Title: "Hombro derecho", Body: "duele en press", Pinned: true, CreatedAt: memoryAt(3), UpdatedAt: memoryAt(3)},
		At:       memoryAt(3),
	})
	if err != nil || survivor.ID == a.ID || survivor.ID == b.ID || !survivor.Pinned {
		t.Fatalf("survivor = %#v, %v", survivor, err)
	}
	if got := listView(t, u, training.MemoryViewActive, memoryAt(0)); len(got) != 1 || got[0] != "Hombro derecho" {
		t.Fatalf("active = %v", got)
	}
	forgotten, err := u.ListMemory(ctx, training.MemoryQuery{View: training.MemoryViewForgotten, Limit: 10})
	if err != nil || len(forgotten) != 2 {
		t.Fatalf("forgotten = %v, %v", titles(forgotten), err)
	}
	for _, m := range forgotten {
		if m.ForgetReason != training.MemoryConsolidated || m.ConsolidatedInto != survivor.ID {
			t.Fatalf("source = %#v", m)
		}
	}
}

func TestConsolidationIntoASourcesKeyKeepsThatFact(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	a := save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "duele"}).Memory
	b := save(t, u, training.Memory{TopicKey: "b", Title: "Hombro bis", Body: "en press"}).Memory
	survivor, err := u.ConsolidateMemory(ctx, training.MemoryConsolidation{
		Sources:  map[int64]int{a.ID: 1, b.ID: 1},
		Survivor: training.Memory{TopicKey: "a", Type: training.MemoryNote, Title: "Hombro", Body: "duele en press", CreatedAt: memoryAt(3), UpdatedAt: memoryAt(3)},
		At:       memoryAt(3),
	})
	if err != nil || survivor.ID != a.ID || survivor.Revision != 2 {
		t.Fatalf("survivor = %#v, %v", survivor, err)
	}
	if got := listView(t, u, training.MemoryViewForgotten, memoryAt(0)); len(got) != 1 || got[0] != "Hombro bis" {
		t.Fatalf("forgotten = %v", got)
	}
}

func TestConsolidationRefusesAStalePlanAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := openTwoUsers(t)
	u := store.ForUser(1)
	a := save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "duele"}).Memory
	b := save(t, u, training.Memory{TopicKey: "b", Title: "Hombro bis", Body: "en press"}).Memory
	save(t, u, training.Memory{TopicKey: "b", Title: "Hombro bis", Body: "cambiado"})
	before := dumpAll(t, store)
	_, err := u.ConsolidateMemory(ctx, training.MemoryConsolidation{
		Sources:  map[int64]int{a.ID: 1, b.ID: 1},
		Survivor: training.Memory{TopicKey: "s", Type: training.MemoryNote, Title: "S", Body: "s", CreatedAt: memoryAt(3), UpdatedAt: memoryAt(3)},
		At:       memoryAt(3),
	})
	if !errors.Is(err, training.ErrStale) {
		t.Fatalf("err = %v", err)
	}
	if after := dumpAll(t, store); after != before {
		t.Fatalf("a refused consolidation wrote:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestConsolidationNeverOverwritesAnotherActiveFact(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	a := save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "duele"}).Memory
	b := save(t, u, training.Memory{TopicKey: "b", Title: "Hombro bis", Body: "en press"}).Memory
	save(t, u, training.Memory{TopicKey: "otro", Title: "Rodilla", Body: "intacta"})
	_, err := u.ConsolidateMemory(ctx, training.MemoryConsolidation{
		Sources:  map[int64]int{a.ID: 1, b.ID: 1},
		Survivor: training.Memory{TopicKey: "otro", Type: training.MemoryNote, Title: "Hombro", Body: "todo", CreatedAt: memoryAt(3), UpdatedAt: memoryAt(3)},
		At:       memoryAt(3),
	})
	if !errors.Is(err, training.ErrValidation) {
		t.Fatalf("err = %v", err)
	}
	if got := listView(t, u, training.MemoryViewActive, memoryAt(0)); len(got) != 3 {
		t.Fatalf("active = %v", got)
	}
}

// A database without 020 gets review dates from each fact's type.
func TestMigrationGivesExistingFactsAReviewDate(t *testing.T) {
	store := openTwoUsers(t)
	if _, err := store.db.Exec(`UPDATE coach_memory SET review_after=''`); err != nil {
		t.Fatal(err)
	}
	u := store.ForUser(1)
	save(t, u, training.Memory{TopicKey: "a", Type: training.MemoryLimitation, Title: "Hombro", Body: "duele"})
	if _, err := store.db.Exec(`UPDATE coach_memory SET review_after=''`); err != nil {
		t.Fatal(err)
	}
	body, err := migrationFS.ReadFile("migrations/020_memory_lifecycle.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Only the backfill statement, which ends at the first ";" after "UPDATE coach_memory".
	text := string(body)
	start := strings.Index(text, "UPDATE coach_memory")
	end := start + strings.Index(text[start:], ";")
	if _, err = store.db.Exec(text[start : end+1]); err != nil {
		t.Fatal(err)
	}
	got := listView(t, u, training.MemoryViewDue, memoryAt(0).AddDate(0, 0, 14))
	if len(got) != 1 || len(listView(t, u, training.MemoryViewDue, memoryAt(0).AddDate(0, 0, 13))) != 0 {
		t.Fatalf("backfilled review date is not 14 days after the update: %v", got)
	}
}

// supersededPair saves A, then B superseding it.
func supersededPair(t *testing.T, u training.UserStore) (old, newer training.Memory) {
	t.Helper()
	old = save(t, u, training.Memory{TopicKey: "a", Title: "Hombro lesionado", Body: "no press"}).Memory
	saved := save(t, u, training.Memory{TopicKey: "b", Title: "Hombro recuperado", Body: "press ligero"})
	if _, err := u.JudgeMemory(context.Background(), saved.Candidates[0].RelationID, training.RelationSupersedes, "", memoryAt(1)); err != nil {
		t.Fatal(err)
	}
	return old, saved.Memory
}

// Removing the fact that replaced another gives the older one back: nobody
// meant to retire it for good.
func TestRemovingTheReplacingFactBringsTheOldOneBack(t *testing.T) {
	ctx := context.Background()
	for name, remove := range map[string]func(training.UserStore, int64) error{
		"deleted": func(u training.UserStore, id int64) error { _, err := u.DeleteMemory(ctx, id); return err },
		"forgotten": func(u training.UserStore, id int64) error {
			_, err := u.ForgetMemory(ctx, id, training.MemoryForgotten, memoryAt(2))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			u := openTwoUsers(t).ForUser(1)
			old, newer := supersededPair(t, u)
			if err := remove(u, newer.ID); err != nil {
				t.Fatal(err)
			}
			got, err := u.GetMemory(ctx, old.ID)
			if err != nil || got.SupersededBy != 0 {
				t.Fatalf("old fact = %#v, %v", got, err)
			}
			if active := listView(t, u, training.MemoryViewActive, memoryAt(0)); len(active) != 1 || active[0] != "Hombro lesionado" {
				t.Fatalf("active = %v", active)
			}
		})
	}
}

// A fact forgotten before its relation is judged cannot retire anything.
func TestAForgottenFactSupersedesNothing(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	save(t, u, training.Memory{TopicKey: "a", Title: "Hombro lesionado", Body: "no press"})
	saved := save(t, u, training.Memory{TopicKey: "b", Title: "Hombro recuperado", Body: "press ligero"})
	if _, err := u.ForgetMemory(ctx, saved.Memory.ID, training.MemoryForgotten, memoryAt(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := u.JudgeMemory(ctx, saved.Candidates[0].RelationID, training.RelationSupersedes, "", memoryAt(2)); err != nil {
		t.Fatal(err)
	}
	if active := listView(t, u, training.MemoryViewActive, memoryAt(0)); len(active) != 1 || active[0] != "Hombro lesionado" {
		t.Fatalf("active = %v", active)
	}
}

// Merging the replacing fact passes the replacement on to the survivor.
func TestConsolidatingTheReplacingFactKeepsTheOldOneReplaced(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	old, newer := supersededPair(t, u)
	other := save(t, u, training.Memory{TopicKey: "c", Title: "Press ligero", Body: "con mancuernas"}).Memory
	survivor, err := u.ConsolidateMemory(ctx, training.MemoryConsolidation{
		Sources:  map[int64]int{newer.ID: 1, other.ID: 1},
		Survivor: training.Memory{TopicKey: "s", Type: training.MemoryNote, Title: "Hombro bien", Body: "press ligero con mancuernas", CreatedAt: memoryAt(3), UpdatedAt: memoryAt(3)},
		At:       memoryAt(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := u.GetMemory(ctx, old.ID)
	if err != nil || got.SupersededBy != survivor.ID {
		t.Fatalf("old fact = %#v, %v", got, err)
	}
}

// Deleting a merge's survivor for good leaves no pointer to a missing fact.
func TestDeletingASurvivorClearsItsSourcesPointer(t *testing.T) {
	ctx := context.Background()
	u := openTwoUsers(t).ForUser(1)
	a := save(t, u, training.Memory{TopicKey: "a", Title: "Hombro", Body: "uno"}).Memory
	b := save(t, u, training.Memory{TopicKey: "b", Title: "Rodilla", Body: "dos"}).Memory
	survivor, err := u.ConsolidateMemory(ctx, training.MemoryConsolidation{Sources: map[int64]int{a.ID: 1, b.ID: 1},
		Survivor: training.Memory{TopicKey: "s", Type: training.MemoryNote, Title: "S", Body: "s", CreatedAt: memoryAt(3), UpdatedAt: memoryAt(3)}, At: memoryAt(3)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = u.DeleteMemory(ctx, survivor.ID); err != nil {
		t.Fatal(err)
	}
	forgotten, err := u.ListMemory(ctx, training.MemoryQuery{View: training.MemoryViewForgotten, Limit: 10})
	if err != nil || len(forgotten) != 2 || forgotten[0].ConsolidatedInto != 0 || forgotten[1].ConsolidatedInto != 0 {
		t.Fatalf("forgotten = %#v, %v", forgotten, err)
	}
}

// Sharing only stopwords or very short words is not being about the same thing.
func TestCandidatesIgnoreStopwordsAndShortWords(t *testing.T) {
	u := openTwoUsers(t).ForUser(1)
	save(t, u, training.Memory{TopicKey: "a", Title: "No tiene barra para dominadas por la casa", Body: "x"})
	save(t, u, training.Memory{TopicKey: "b", Title: "The bar and the rack", Body: "x"})
	saved := save(t, u, training.Memory{TopicKey: "c", Title: "Prefiere entrenar por la mañana con the and", Body: "x"})
	if len(saved.Candidates) != 0 {
		t.Fatalf("stopwords made candidates: %#v", saved.Candidates)
	}
	// A real shared word still counts.
	if again := save(t, u, training.Memory{TopicKey: "d", Title: "Dominadas con lastre", Body: "x"}); len(again.Candidates) != 1 {
		t.Fatalf("candidates = %#v", again.Candidates)
	}
}
