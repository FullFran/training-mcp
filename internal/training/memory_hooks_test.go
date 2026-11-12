package training_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

func TestParseLearningsTakesTheItemsOfTheClosingBlock(t *testing.T) {
	reply := "Buen trabajo hoy.\n\n## Aprendizajes\n- limitation: Le molesta el hombro derecho en press inclinado. Evitar por ahora.\n* Prefiere entrenar por la mañana\n2. objetivo: Diez dominadas seguidas en verano\n  antes de julio\n\n"
	got := training.ParseLearnings(reply)
	want := []training.MemoryInput{
		{Type: "limitation", Title: "Le molesta el hombro derecho en press inclinado.", Body: "Le molesta el hombro derecho en press inclinado. Evitar por ahora."},
		{Type: "note", Title: "Prefiere entrenar por la mañana", Body: "Prefiere entrenar por la mañana"},
		{Type: "goal", Title: "Diez dominadas seguidas en verano antes de julio", Body: "Diez dominadas seguidas en verano antes de julio"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("learnings =\n%#v\nwant\n%#v", got, want)
	}
}

func TestParseLearningsIgnoresBlocksThatDoNotCloseTheReply(t *testing.T) {
	for name, reply := range map[string]string{
		"no block":          "Sin nada que recordar.",
		"text after block":  "## Aprendizajes\n- Prefiere mancuernas\n\nY otra cosa que no es un item.",
		"heading after":     "## Aprendizajes\n- Prefiere mancuernas\n## Siguiente\nnada",
		"other heading":     "## Aprendizaje clave\n- Prefiere mancuernas",
		"level three":       "### Aprendizajes\n- Prefiere mancuernas",
		"empty block":       "Hecho.\n## Aprendizajes\n\n",
		"inside code fence": "```\n## Aprendizajes\n- Prefiere mancuernas\n```",
	} {
		if got := training.ParseLearnings(reply); len(got) != 0 {
			t.Errorf("%s: learnings = %#v", name, got)
		}
	}
}

func TestParseLearningsIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("## APRENDIZAJES:\n")
	for i := range 8 {
		b.WriteString("- " + strconv.Itoa(i) + " " + strings.Repeat("palabra ", 600) + "\n")
	}
	b.WriteString("- Prefiere mancuernas\n- prefiere mancuernas\n")
	got := training.ParseLearnings(b.String())
	if len(got) != training.MaxLearnings {
		t.Fatalf("items = %d, want %d", len(got), training.MaxLearnings)
	}
	for _, in := range got {
		if n := len([]rune(in.Title)); n > 120 {
			t.Fatalf("title runes = %d", n)
		}
		if n := len([]rune(in.Body)); n > 4000 {
			t.Fatalf("body runes = %d", n)
		}
	}
	dup := training.ParseLearnings("## Aprendizajes\n- Prefiere mancuernas\n- prefiere  mancuernas")
	if len(dup) != 1 {
		t.Fatalf("duplicates kept: %#v", dup)
	}
}

// hookFixture is a store with two users: the owner asks before agent writes.
type hookFixture struct {
	now      time.Time
	services *training.Services
	owner    *training.Service
	stranger *training.Service
	ids      []int64
	applied  []json.RawMessage
}

func newHookFixture(t *testing.T) *hookFixture {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	// Sets are stamped with the real time, so the clock follows it.
	f := &hookFixture{now: time.Now().UTC()}
	f.services = training.NewServices(store, func() time.Time { return f.now })
	ctx := context.Background()
	ids := []int64{}
	for _, name := range []string{"user_owner", "user_stranger"} {
		id, err := store.ProvisionUser(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	f.owner, f.stranger, f.ids = f.services.ForUser(ids[0]), f.services.ForUser(ids[1]), ids
	return f
}

func (f *hookFixture) apply(_ context.Context, _ *training.Service, tool string, payload json.RawMessage) (json.RawMessage, error) {
	if tool != training.MemorySaveTool {
		return nil, errors.New("unexpected tool " + tool)
	}
	f.applied = append(f.applied, payload)
	return json.RawMessage(`{}`), nil
}

// must stops a test on a setup error; it panics, which fails the test.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func addSet(t *testing.T, svc *training.Service, session int64, exercise string, kg float64, reps int, rpe float64, category string) {
	t.Helper()
	if _, _, err := svc.AddSet(context.Background(), training.AddSetInput{SessionID: session, Exercise: exercise, WeightKG: kg, Reps: reps, RPE: rpe, Category: category}); err != nil {
		t.Fatal(err)
	}
}

// workout builds a past session and today's planned one with a warm-up, a
// record, an unfinished, a skipped and an off-plan exercise.
func (f *hookFixture) workout(t *testing.T, svc *training.Service) training.Session {
	t.Helper()
	ctx := context.Background()
	before := must(svc.StartSession(ctx, f.now.AddDate(0, 0, -7).Format("2006-01-02")))
	addSet(t, svc, before.ID, "banca", 80, 5, 8, "")
	addSet(t, svc, before.ID, "remo", 60, 10, 8, "")
	plan := must(svc.CreatePlan(ctx, training.Plan{Name: "Torso", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 3, RepMin: 6, RepMax: 7},
		{Exercise: "remo", TargetSets: 3},
		{Exercise: "sentadilla", TargetSets: 2},
	}}))
	today := must(svc.StartPlannedSession(ctx, f.now.Format("2006-01-02"), plan.ID))
	addSet(t, svc, today.ID, "banca", 40, 10, 5, training.SetWarmup)
	addSet(t, svc, today.ID, "banca", 80, 8, 8, "")
	addSet(t, svc, today.ID, "banca", 80, 7, 8, "")
	addSet(t, svc, today.ID, "banca", 80, 6, 9, "")
	addSet(t, svc, today.ID, "remo", 60, 8, 8, "")
	addSet(t, svc, today.ID, "remo", 60, 8, 8.5, "")
	addSet(t, svc, today.ID, "curl", 20, 12, 9, "")
	skipped := true
	if err := svc.AdjustSessionItem(ctx, today.ID, "sentadilla", training.SessionItemPatch{Skipped: &skipped}); err != nil {
		t.Fatal(err)
	}
	return today
}

func TestWorkoutSummaryIsDeterministicAndLeavesWarmupsOut(t *testing.T) {
	f := newHookFixture(t)
	today := f.workout(t, f.owner)
	in, ok, err := f.owner.WorkoutSummary(context.Background(), today.ID)
	if err != nil || !ok {
		t.Fatalf("summary = %v, %v", ok, err)
	}
	day := f.now.Format("02/01/2006")
	if in.Type != training.MemoryNote || in.TopicKey != "workout/"+f.now.Format("2006-01-02")+"-"+strconv.FormatInt(today.ID, 10) || in.Title != "Entreno del "+day+" · Torso" {
		t.Fatalf("summary head = %+v", in)
	}
	for _, want := range []string{
		"3 ejercicios, 6 series efectivas, volumen 2880 kg",
		"- banca: 3 series (80 kg × 8 @ RPE 8, 80 kg × 7 @ RPE 8, 80 kg × 6 @ RPE 9); récord: 80 kg × 8 (1RM est. 101.3 kg, antes 93.3 kg)",
		"- remo: 2 series (60 kg × 8 @ RPE 8, 60 kg × 8 @ RPE 8.5)\n",
		"- curl: 1 serie (20 kg × 12 @ RPE 9)",
		"Respecto al plan: banca: 1 serie fuera de 6-7 reps; remo: 2 de 3 series; sentadilla: saltado; curl: fuera del plan.",
		"Calentamientos no contados.",
	} {
		if !strings.Contains(in.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, in.Body)
		}
	}
	if strings.Contains(in.Body, "40 kg") || strings.Contains(in.Body, "remo: 2 series (60 kg × 8 @ RPE 8, 60 kg × 8 @ RPE 8.5); récord") {
		t.Fatalf("body counts a warm-up or a non-record:\n%s", in.Body)
	}
	again, _, _ := f.owner.WorkoutSummary(context.Background(), today.ID)
	if !reflect.DeepEqual(in, again) {
		t.Fatalf("summary changed between calls")
	}
}

func TestWorkoutSummaryWithoutAPlanHasNoPlanLine(t *testing.T) {
	f := newHookFixture(t)
	ctx := context.Background()
	s := must(f.owner.StartSession(ctx, f.now.Format("2006-01-02")))
	addSet(t, f.owner, s.ID, "banca", 80, 5, 8, "")
	in, ok, err := f.owner.WorkoutSummary(ctx, s.ID)
	if !ok || err != nil || strings.Contains(in.Body, "plan") || in.Title != "Entreno del "+f.now.Format("02/01/2006") {
		t.Fatalf("summary = %+v, %v, %v", in, ok, err)
	}
}

func TestWorkoutSummaryOfOnlyWarmupsIsNothing(t *testing.T) {
	f := newHookFixture(t)
	ctx := context.Background()
	s := must(f.owner.StartSession(ctx, f.now.Format("2006-01-02")))
	addSet(t, f.owner, s.ID, "banca", 40, 10, 5, training.SetWarmup)
	if _, ok, err := f.owner.WorkoutSummary(ctx, s.ID); ok || err != nil {
		t.Fatalf("summary of warm-ups = %v, %v", ok, err)
	}
	if _, ok, err := f.owner.ProposeWorkoutSummary(ctx, s.ID, f.apply); ok || err != nil {
		t.Fatalf("proposal of warm-ups = %v, %v", ok, err)
	}
	// Nothing was claimed: a working set logged later still gets its summary.
	addSet(t, f.owner, s.ID, "banca", 80, 5, 8, "")
	if _, ok, err := f.owner.ProposeWorkoutSummary(ctx, s.ID, f.apply); !ok || err != nil {
		t.Fatalf("proposal after a working set = %v, %v", ok, err)
	}
}

func TestWorkoutSummaryIsProposedOnceAsAMemorySave(t *testing.T) {
	f := newHookFixture(t)
	ctx := context.Background()
	today := f.workout(t, f.owner)
	p, ok, err := f.owner.ProposeWorkoutSummary(ctx, today.ID, f.apply)
	if err != nil || !ok || p.Status != training.ProposalPending || p.Tool != training.MemorySaveTool || p.ActionType != training.MemorySaveAction || p.Session != "" {
		t.Fatalf("proposal = %+v, %v, %v", p, ok, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(p.Payload, &payload); err != nil || payload["type"] != "note" || payload["topic_key"] == "" || !strings.Contains(payload["body"].(string), "volumen 2880 kg") {
		t.Fatalf("payload = %s", p.Payload)
	}
	if _, ok, err := f.owner.ProposeWorkoutSummary(ctx, today.ID, f.apply); ok || err != nil {
		t.Fatalf("second proposal = %v, %v", ok, err)
	}
	if len(f.applied) != 0 {
		t.Fatalf("a pending proposal was applied")
	}
	pending := must(f.owner.ListProposals(ctx, training.ProposalFilter{Pending: true}))
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	// Accepting goes through the applier with the very payload proposed.
	if _, err := f.owner.AcceptProposal(ctx, p.ID, training.AnswerAllowOnce, f.apply); err != nil || len(f.applied) != 1 || string(f.applied[0]) != string(p.Payload) {
		t.Fatalf("accept = %v, applied %s", err, f.applied)
	}
}

func TestWorkoutSummaryOfAnotherUsersSessionIsMissing(t *testing.T) {
	f := newHookFixture(t)
	ctx := context.Background()
	today := f.workout(t, f.owner)
	if _, ok, err := f.stranger.ProposeWorkoutSummary(ctx, today.ID, f.apply); ok || !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("stranger proposal = %v, %v", ok, err)
	}
	if got := must(f.stranger.ListProposals(ctx, training.ProposalFilter{Pending: true})); len(got) != 0 {
		t.Fatalf("stranger proposals = %+v", got)
	}
	if got := must(f.owner.ListProposals(ctx, training.ProposalFilter{Pending: true})); len(got) != 0 {
		t.Fatalf("owner proposals = %+v", got)
	}
	// The stranger's attempt claimed nothing: the owner still gets it.
	if _, ok, err := f.owner.ProposeWorkoutSummary(ctx, today.ID, f.apply); !ok || err != nil {
		t.Fatalf("owner proposal = %v, %v", ok, err)
	}
}

func TestIdleWorkoutSummaryWaitsForTheSessionToGoIdle(t *testing.T) {
	f := newHookFixture(t)
	ctx := context.Background()
	f.workout(t, f.owner)
	f.now = time.Now().UTC().Add(training.WorkoutIdleAfter / 2)
	if got, err := f.owner.ProposeIdleWorkoutSummaries(ctx, f.apply); len(got) != 0 || err != nil {
		t.Fatalf("before idle = %+v, %v", got, err)
	}
	f.now = time.Now().UTC().Add(training.WorkoutIdleAfter + time.Minute)
	got, err := f.owner.ProposeIdleWorkoutSummaries(ctx, f.apply)
	if err != nil || len(got) != 1 || !strings.Contains(string(got[0].Payload), "Torso") {
		t.Fatalf("idle = %+v, %v", got, err)
	}
	// The earlier session of a week ago is too old to summarise now.
	if again, err := f.owner.ProposeIdleWorkoutSummaries(ctx, f.apply); len(again) != 0 || err != nil {
		t.Fatalf("again = %+v, %v", again, err)
	}
	if theirs, err := f.stranger.ProposeIdleWorkoutSummaries(ctx, f.apply); len(theirs) != 0 || err != nil {
		t.Fatalf("stranger idle = %+v, %v", theirs, err)
	}
}

func TestHostMemoryProposesInTheGivenSessionForThatUserOnly(t *testing.T) {
	f := newHookFixture(t)
	ctx := context.Background()
	if _, err := f.owner.SaveMemory(ctx, training.MemoryInput{Type: "limitation", Title: "Hombro", Body: "Molesta en press"}); err != nil {
		t.Fatal(err)
	}
	hooks := training.HostMemory{ForUser: f.services.ForUser, Apply: f.apply}
	owner, stranger := f.ids[0], f.ids[1]
	brief, err := hooks.MemoryContext(ctx, owner)
	if err != nil || len(brief.Memories) != 1 {
		t.Fatalf("owner context = %+v, %v", brief, err)
	}
	if theirs, err := hooks.MemoryContext(ctx, stranger); err != nil || len(theirs.Memories) != 0 {
		t.Fatalf("stranger context = %+v, %v", theirs, err)
	}
	session := training.CoachSessionPrefix + "conversation-7"
	if err := hooks.ProposeMemory(ctx, owner, session, training.MemoryInput{Title: "Mancuernas", Body: "Prefiere mancuernas"}); err != nil {
		t.Fatal(err)
	}
	pending := must(f.owner.ListProposals(ctx, training.ProposalFilter{Pending: true}))
	if len(pending) != 1 || pending[0].Session != session || pending[0].Tool != training.MemorySaveTool {
		t.Fatalf("owner pending = %+v", pending)
	}
	if theirs := must(f.stranger.ListProposals(ctx, training.ProposalFilter{Pending: true})); len(theirs) != 0 {
		t.Fatalf("stranger pending = %+v", theirs)
	}
	if err := hooks.ProposeMemory(ctx, owner, session, training.MemoryInput{Title: " ", Body: "x"}); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("invalid proposal err = %v", err)
	}
	if got := must(f.owner.ListProposals(ctx, training.ProposalFilter{Pending: true})); len(got) != 1 {
		t.Fatalf("an invalid fact was proposed: %+v", got)
	}
}
