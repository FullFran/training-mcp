package websrv_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// mode returns the screen the server rendered, from the body's data-mode.
func mode(t *testing.T, body string) string {
	t.Helper()
	tag := section(t, body, "<body", ">")
	for _, m := range []string{"home", "train", "plan", "progress", "you"} {
		if strings.Contains(tag, `data-mode="`+m+`"`) {
			return m
		}
	}
	t.Fatalf("body has no data-mode: %s", tag)
	return ""
}

// Four tabs, always in the same order, each a plain link that also works
// without JavaScript; the active workout is a layer over them.
func TestTabBarLinksTheFourTabs(t *testing.T) {
	h, _ := newServer(t)
	body := get(t, h, base+"/").Body.String()
	if got := mode(t, body); got != "home" {
		t.Fatalf("first screen = %s, want home (Entrenar)", got)
	}
	tabs := section(t, body, `id="tabs"`, "</nav>")
	last := 0
	for _, want := range []string{
		`href="` + base + `/"`, ">Entrenar<",
		`href="` + base + `/?modo=planificar"`, ">Planificar<",
		`href="` + base + `/?modo=progreso"`, ">Progreso<",
		`href="` + base + `/?modo=tu"`, ">Tú<",
	} {
		i := strings.Index(tabs, want)
		if i < last {
			t.Fatalf("tab bar lacks %s in order: %s", want, tabs)
		}
		last = i
	}
	if !strings.Contains(section(t, tabs, `data-tab="home"`, "</a>"), `aria-current="page"`) {
		t.Fatalf("Entrenar is not the current tab: %s", tabs)
	}
	plan := section(t, get(t, h, base+"/?modo=planificar").Body.String(), `id="tabs"`, "</nav>")
	if !strings.Contains(section(t, plan, `data-tab="plan"`, "</a>"), `aria-current="page"`) {
		t.Fatalf("Planificar is not the current tab: %s", plan)
	}
}

// ?modo= picks the screen; anything else is Entrenar's home.
func TestModeQuerySelectsTheScreen(t *testing.T) {
	h, _ := newServer(t)
	for query, want := range map[string]string{
		"?modo=entrenar":   "train",
		"?modo=planificar": "plan",
		"?modo=progreso":   "progress",
		"?modo=tu":         "you",
		"?modo=otra":       "home",
		"":                 "home",
	} {
		if got := mode(t, get(t, h, base+"/"+query).Body.String()); got != want {
			t.Fatalf("%q renders %s, want %s", query, got, want)
		}
	}
}

// With no routine and nothing logged, Entrenar says so and offers the ways to
// begin, the coach first.
func TestEntrenarHomeFirstUse(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	home := section(t, get(t, world.as(1), base+"/").Body.String(), `id="home"`, "</section>")
	last := 0
	for _, want := range []string{
		"Aún no tienes rutina",
		`id="ask-routine"`, "Pídele una rutina al coach",
		`href="` + base + `/plans"`, "Crear rutina a mano",
		`href="` + base + `/import"`, "Importar de Hevy o Strong",
		`data-free-session`, "Entrenar sin rutina",
	} {
		i := strings.Index(home, want)
		if i < last {
			t.Fatalf("first-use home lacks %s in order: %s", want, home)
		}
		last = i
	}

	h, _ := newServer(t)
	home = section(t, get(t, h, base+"/").Body.String(), `id="home"`, "</section>")
	if strings.Contains(home, `id="ask-routine"`) || !strings.Contains(home, "Crear rutina a mano") {
		t.Fatalf("with the coach off the first-use home must not offer it: %s", home)
	}
}

// The next routine is the one after the last one done, in the plans' order;
// the others are listed with when they were last done, then the last session.
func TestEntrenarHomeSuggestsTheNextRoutine(t *testing.T) {
	h, service := newServer(t)
	a, err := service.CreatePlan(t.Context(), training.Plan{Name: "Día A", Items: []training.PlanItem{{Exercise: "remo", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.CreatePlan(t.Context(), training.Plan{Name: "Día B", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 4}, {Exercise: "militar", TargetSets: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	done, err := service.StartPlannedSession(t.Context(), "2026-07-28", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.AddSet(t.Context(), training.AddSetInput{SessionID: done.ID, Exercise: "remo", WeightKG: 60, Reps: 10, RPE: 8}); err != nil {
		t.Fatal(err)
	}

	home := section(t, get(t, h, base+"/").Body.String(), `id="home"`, "</section>")
	next := section(t, home, `id="next-routine"`, "</form>")
	for _, want := range []string{"Día B", "banca · militar", "2 ejercicios · 7 series", `action="` + base + `/plan"`, `name="plan_id" value="` + itoa(b.ID) + `"`, "Empezar"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next routine lacks %s: %s", want, next)
		}
	}
	others := section(t, home, `id="other-routines"`, "</ul>")
	if !strings.Contains(others, "Día A") || !strings.Contains(others, `value="`+itoa(a.ID)+`"`) || strings.Contains(others, "Día B") {
		t.Fatalf("other routines: %s", others)
	}
	if !strings.Contains(others, "hace ") {
		t.Fatalf("other routines do not say when: %s", others)
	}
	last := section(t, home, `id="last-session"`, "</a>")
	for _, want := range []string{"Día A", "1 serie", `href="` + base + `/s/` + itoa(done.ID) + `"`} {
		if !strings.Contains(last, want) {
			t.Fatalf("last session lacks %s: %s", want, last)
		}
	}
	if !strings.Contains(home, `data-free-session`) {
		t.Fatal("no empty workout")
	}
}

// Once a workout is live, Entrenar continues it and a pill on every tab
// leads back to it.
func TestEntrenarHomeContinuesALiveWorkout(t *testing.T) {
	h, service := newServer(t)
	if _, err := service.CreatePlan(t.Context(), training.Plan{Name: "Día A", Items: []training.PlanItem{{Exercise: "remo", TargetSets: 3}}}); err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)
	body := get(t, h, base+"/").Body.String()
	home := section(t, body, `id="home"`, "</section>")
	if strings.Contains(home, `id="next-routine"`) {
		t.Fatalf("a live workout still offers to start another: %s", home)
	}
	cont := section(t, home, `id="continue"`, "</a>")
	for _, want := range []string{`href="` + base + `/?modo=entrenar"`, `data-mode-go="train"`, "Continuar", "1 serie"} {
		if !strings.Contains(cont, want) {
			t.Fatalf("continue card lacks %s: %s", want, cont)
		}
	}
	pill := section(t, body, `id="workout-pill"`, "</a>")
	if !strings.Contains(pill, `href="`+base+`/?modo=entrenar"`) || !strings.Contains(pill, `data-mode-go="train"`) {
		t.Fatalf("pill: %s", pill)
	}
	h, _ = newServer(t)
	if pill := section(t, get(t, h, base+"/").Body.String(), `id="workout-pill"`, ">"); !strings.Contains(pill, " hidden") {
		t.Fatalf("pill shown with no workout: %s", pill)
	}
}

// The active workout is a full-screen layer: a way back to the tabs, chips
// per exercise with their progress, and a rest bar with -15, +15 and Saltar.
func TestActiveWorkoutLayer(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "Día B", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 2}, {Exercise: "militar", TargetSets: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)
	body := get(t, h, base+"/?modo=entrenar").Body.String()
	bar := section(t, body, `class="bar train-bar"`, "</header>")
	for _, want := range []string{`data-mode-go="home"`, `id="wk-clock"`, "Día B", `id="finish-open"`} {
		if !strings.Contains(bar, want) {
			t.Fatalf("workout bar lacks %s: %s", want, bar)
		}
	}
	chips := section(t, body, `class="ex-chips"`, "</nav>")
	for _, want := range []string{`data-chip="banca"`, "1/2", `data-chip="militar"`, "0/3"} {
		if !strings.Contains(chips, want) {
			t.Fatalf("chips lack %s: %s", want, chips)
		}
	}
	panel := section(t, body, `id="panel"`, ">")
	for _, want := range []string{`data-plan-name="Día B"`, `data-sets-done="1"`, `data-sets-planned="5"`} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel lacks %s: %s", want, panel)
		}
	}
	rest := section(t, body, `id="rest"`, "</div>")
	for _, want := range []string{`data-delta="-15"`, `data-delta="15"`, "Saltar"} {
		if !strings.Contains(rest, want) {
			t.Fatalf("rest bar lacks %s: %s", want, rest)
		}
	}
}

// With nothing done today, Entrenar's home offers every plan and an empty
// session; once a plan is chosen the workout lists the exercises with planned
// against done.
func TestEntrenarStartsFromAPlanOrAnEmptySession(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "Empuje A", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9},
		{Exercise: "fondos", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	start := section(t, get(t, h, base+"/").Body.String(), `id="home"`, "</section>")
	for _, want := range []string{
		`action="` + base + `/plan"`, `name="plan_id" value="` + itoa(plan.ID) + `"`, "Empuje A", "5 series",
		`data-free-session`, "Entreno vacío",
	} {
		if !strings.Contains(start, want) {
			t.Fatalf("start lacks %s: %s", want, start)
		}
	}

	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	post(t, h, base+"/sets", setForm("banca", "80", "9", "9"), true)
	train := section(t, get(t, h, base+"/?modo=entrenar").Body.String(), `id="train"`, "</main>")
	planned := section(t, train, `class="planned"`, "</section>")
	for _, want := range []string{`data-exercise="banca"`, "1/3", "8-10 reps @9", `data-exercise="fondos"`, "0/2"} {
		if !strings.Contains(planned, want) {
			t.Fatalf("planned list lacks %s: %s", want, planned)
		}
	}
}

// The training screen's entry form is the one that logs sets, and the rest
// timer stays in the dock below the list.
func TestEntrenarLogsWithTheSameFormAndTimer(t *testing.T) {
	h, _ := newServer(t)
	body := get(t, h, base+"/?modo=entrenar").Body.String()
	dock := section(t, body, `class="dock"`, "</footer>")
	for _, want := range []string{`id="workout"`, `id="wk-log"`, `id="rest"`, `id="entry"`, `hx-post="` + base + `/sets"`} {
		if !strings.Contains(dock, want) {
			t.Fatalf("dock lacks %s", want)
		}
	}
	if strings.Contains(dock, `id="panel"`) {
		t.Fatal("the workout panel belongs on the training screen, not in the sheet")
	}
	if !strings.Contains(section(t, body, `id="train"`, "</main>"), `id="panel"`) {
		t.Fatal("training screen has no workout panel")
	}
}

// In Entrenar a bar asks the coach; it opens the very same chat as an overlay
// that closes back to the workout.
func TestEntrenarOpensTheCoachAsAnOverlay(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	h := world.as(1)
	body := get(t, h, base+"/?modo=entrenar").Body.String()
	launch := section(t, body, `id="chat-launch"`, "</div>")
	for _, want := range []string{"Pregunta a tu coach", `id="launch-mic"`, `aria-controls="coach-sheet"`} {
		if !strings.Contains(launch, want) {
			t.Fatalf("chat bar lacks %s: %s", want, launch)
		}
	}
	sheet := section(t, body, `id="coach-sheet"`, `class="dock"`)
	for _, want := range []string{`id="coach-close"`, "Volver al entreno", `id="coach-log"`, `id="chat-cards"`, `id="coach-form"`, `id="coach-mic"`} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("coach sheet lacks %s", want)
		}
	}
	if strings.Contains(sheet, `id="workout"`) {
		t.Fatal("the coach sheet must not hold the workout")
	}

	h, _ = newServer(t)
	if strings.Contains(get(t, h, base+"/?modo=entrenar").Body.String(), `id="chat-launch"`) {
		t.Fatal("chat bar shown with the coach off")
	}
}

// Finishing shows a summary built from the server's data: exercises, sets,
// SI and records, plus the session rating.
func TestEntrenarFinishesWithASummary(t *testing.T) {
	h, service := newServer(t)
	if err := service.SetExerciseGroup(t.Context(), "banca", "pecho"); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, base+"/?modo=entrenar").Body.String()
	if !strings.Contains(section(t, body, `class="bar train-bar"`, "</header>"), `id="finish-open"`) {
		t.Fatal("training bar has no finish button")
	}
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)
	post(t, h, base+"/sets", setForm("banca", "85", "6", "9"), true)
	panel := post(t, h, base+"/sets", setForm("remo", "60", "10", "8"), true).Body.String()
	finish := section(t, panel, `id="finish"`, "</section>")
	for _, want := range []string{"Resumen", "3 series", "2 ejercicios", "SI", "banca", "85 kg × 6", "remo", `id="finish-done"`, "Valorar la sesión"} {
		if !strings.Contains(finish, want) {
			t.Fatalf("summary lacks %s: %s", want, finish)
		}
	}
}

// Logging without JavaScript lands back on the training screen.
func TestNonHtmxWritesReturnToEntrenar(t *testing.T) {
	h, service := newServer(t)
	w := post(t, h, base+"/sets", setForm("squat", "100", "5", "8"), false)
	if got := w.Header().Get("Location"); w.Code != http.StatusSeeOther || got != base+"/?modo=entrenar" {
		t.Fatalf("set: %d %q", w.Code, got)
	}
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "P", Items: []training.PlanItem{{Exercise: "banca", TargetSets: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	w = post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	if got := w.Header().Get("Location"); w.Code != http.StatusSeeOther || got != base+"/?modo=entrenar" {
		t.Fatalf("plan: %d %q", w.Code, got)
	}
}

// Planificar is the chat with its panels; the past session page keeps no
// training controls.
func TestPlanificarIsTheChatWithPanels(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	h := world.as(1)
	body := get(t, h, base+"/?modo=planificar").Body.String()
	bar := section(t, body, `class="bar chat-bar"`, "</header>")
	for _, want := range []string{`id="menu-open"`} {
		if !strings.Contains(bar, want) {
			t.Fatalf("planning bar lacks %s: %s", want, bar)
		}
	}
	if !strings.Contains(section(t, body, `id="drawer"`, "</aside>"), `class="panel-links"`) {
		t.Fatal("no panels in the drawer")
	}
}

// Each exercise is a card with a set table: done rows, then rows still to log,
// pre-filled from the plan and last session, each logged with one check.
func TestEntrenarShowsASetTablePerExercise(t *testing.T) {
	h, service := newServer(t)
	earlier, err := service.StartSession(t.Context(), "2026-08-05")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []training.AddSetInput{
		{SessionID: earlier.ID, Exercise: "banca", WeightKG: 75, Reps: 10, RPE: 8},
		{SessionID: earlier.ID, Exercise: "banca", WeightKG: 77.5, Reps: 9, RPE: 9},
	} {
		if _, _, err := service.AddSet(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "Empuje A", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9},
		{Exercise: "fondos", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	post(t, h, base+"/sets", setForm("banca", "80", "9", "9"), true)

	train := section(t, get(t, h, base+"/?modo=entrenar").Body.String(), `id="train"`, "</main>")
	banca := section(t, train, `data-exercise="banca"`, `data-exercise="fondos"`)
	if !strings.Contains(banca, "<details") || !strings.Contains(banca, " open") {
		t.Fatalf("the exercise in hand is not the open card: %s", banca)
	}
	// Done row: today's values beside last session's first set.
	done := section(t, banca, `class="set-row done"`, "</li>")
	for _, want := range []string{"75 × 10", "80 kg × 9 @9"} {
		if !strings.Contains(done, want) {
			t.Fatalf("done row lacks %s: %s", want, done)
		}
	}
	// Rows to log: two left, the next pre-filled with today's weight, the plan's
	// reps and RPE, and last session's second set as the reference.
	if n := strings.Count(banca, `class="set-row pending`); n != 2 {
		t.Fatalf("pending rows = %d, want 2: %s", n, banca)
	}
	live := section(t, banca, `class="set-row pending live"`, "</form>")
	for _, want := range []string{
		`hx-post="` + base + `/sets"`, `name="exercise" value="banca"`, "77.5 × 9",
		`name="weight_kg"`, `value="80"`, `name="reps"`, `value="9"`, `name="rpe"`, `class="check"`,
	} {
		if !strings.Contains(live, want) {
			t.Fatalf("live row lacks %s: %s", want, live)
		}
	}
	if !strings.Contains(section(t, train, `data-exercise="fondos"`, "</details>"), "0/2") {
		t.Fatal("fondos card lacks its count")
	}
}

// In the set table a warm-up shows a W instead of a number, does not count,
// and never pre-fills the next set; every row to log can be switched to W.
func TestEntrenarMarksWarmupsInTheSetTable(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "P", Items: []training.PlanItem{
		{Exercise: "sentadilla", TargetSets: 2, RepMin: 5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	warm := setForm("sentadilla", "60", "5", "6")
	warm.Set("category", "warmup")
	post(t, h, base+"/sets", warm, true)
	post(t, h, base+"/sets", setForm("sentadilla", "100", "5", "8"), true)

	card := section(t, get(t, h, base+"/?modo=entrenar").Body.String(), `data-exercise="sentadilla"`, "</details>")
	if !strings.Contains(card, `<span class="count">1/2</span>`) || !strings.Contains(card, `<span class="chk">○</span>`) {
		t.Fatalf("a warm-up counted as done: %s", card)
	}
	wu := section(t, card, `class="set-row done warmup"`, "</li>")
	if !strings.Contains(wu, `data-category="warmup"`) || !strings.Contains(wu, `class="warmup-tag"`) {
		t.Fatalf("warm-up row not marked: %s", wu)
	}
	work := section(t, card, `class="set-row done" data-set-id`, "</li>")
	if !strings.Contains(work, `<span class="set-n">1</span>`) {
		t.Fatalf("the first working set is not set 1: %s", work)
	}
	finish := section(t, get(t, h, base+"/?modo=entrenar").Body.String(), `id="finish"`, "</section>")
	if !strings.Contains(finish, "<strong>1 serie</strong>") || !strings.Contains(finish, "1 serie · mejor 100 kg × 5") {
		t.Fatalf("the summary counts the warm-up: %s", finish)
	}
	if home := section(t, get(t, h, base+"/").Body.String(), `id="continue"`, "</a>"); !strings.Contains(home, "1/2 series") {
		t.Fatalf("the home counts the warm-up: %s", home)
	}
	live := section(t, card, `class="set-row pending live"`, "</form>")
	for _, want := range []string{`<span class="set-n">2</span>`, `name="weight_kg"`, `value="100"`, `name="category" value=""`, `class="type-toggle"`} {
		if !strings.Contains(live, want) {
			t.Fatalf("live row lacks %s: %s", want, live)
		}
	}
}
