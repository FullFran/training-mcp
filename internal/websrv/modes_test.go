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
	for _, m := range []string{"home", "train", "plan"} {
		if strings.Contains(tag, `data-mode="`+m+`"`) {
			return m
		}
	}
	t.Fatalf("body has no data-mode: %s", tag)
	return ""
}

// The first screen offers both modes, each a plain link that also works
// without JavaScript.
func TestHomeOffersEntrenarAndPlanificar(t *testing.T) {
	h, _ := newServer(t)
	body := get(t, h, base+"/").Body.String()
	if got := mode(t, body); got != "home" {
		t.Fatalf("first screen = %s, want home", got)
	}
	home := section(t, body, `id="home"`, "</section>")
	for _, want := range []string{
		`href="` + base + `/?modo=entrenar"`, ">Entrenar<",
		`href="` + base + `/?modo=planificar"`, ">Planificar<",
	} {
		if !strings.Contains(home, want) {
			t.Fatalf("home lacks %s: %s", want, home)
		}
	}
}

// ?modo= picks the screen; anything else is the home.
func TestModeQuerySelectsTheScreen(t *testing.T) {
	h, _ := newServer(t)
	for query, want := range map[string]string{
		"?modo=entrenar":   "train",
		"?modo=planificar": "plan",
		"?modo=otra":       "home",
		"":                 "home",
	} {
		if got := mode(t, get(t, h, base+"/"+query).Body.String()); got != want {
			t.Fatalf("%q renders %s, want %s", query, got, want)
		}
	}
}

// The home says how today's workout stands, so the choice is informed.
func TestHomeShowsTodaysWorkoutOnEntrenar(t *testing.T) {
	h, _ := newServer(t)
	if card := section(t, get(t, h, base+"/").Body.String(), `id="go-train"`, "</a>"); !strings.Contains(card, "Elige un plan o entrena libre") {
		t.Fatalf("idle Entrenar card: %s", card)
	}
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)
	if card := section(t, get(t, h, base+"/").Body.String(), `id="go-train"`, "</a>"); !strings.Contains(card, "1 serie hoy") {
		t.Fatalf("active Entrenar card: %s", card)
	}
}

// With nothing done today, Entrenar offers every plan and an empty session;
// once a plan is chosen it lists the exercises with planned against done.
func TestEntrenarStartsFromAPlanOrAnEmptySession(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "Empuje A", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9},
		{Exercise: "fondos", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	train := section(t, get(t, h, base+"/?modo=entrenar").Body.String(), `id="train"`, "</main>")
	start := section(t, train, `id="train-start"`, "</section>")
	for _, want := range []string{
		`action="` + base + `/plan"`, `name="plan_id" value="` + itoa(plan.ID) + `"`, "Empuje A", "5 series",
		`id="free-session"`, "Sesión libre",
	} {
		if !strings.Contains(start, want) {
			t.Fatalf("start lacks %s: %s", want, start)
		}
	}

	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	post(t, h, base+"/sets", setForm("banca", "80", "9", "9"), true)
	train = section(t, get(t, h, base+"/?modo=entrenar").Body.String(), `id="train"`, "</main>")
	if strings.Contains(train, `id="train-start"`) {
		t.Fatal("a started workout still offers to start one")
	}
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
	for _, want := range []string{`id="menu-open"`, `href="` + base + `/"`, `data-mode-go="home"`} {
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
	if home := section(t, get(t, h, base+"/").Body.String(), `id="go-train"`, "</a>"); strings.Contains(home, "2 series") {
		t.Fatalf("the home counts the warm-up: %s", home)
	}
	live := section(t, card, `class="set-row pending live"`, "</form>")
	for _, want := range []string{`<span class="set-n">2</span>`, `name="weight_kg"`, `value="100"`, `name="category" value=""`, `class="type-toggle"`} {
		if !strings.Contains(live, want) {
			t.Fatalf("live row lacks %s: %s", want, live)
		}
	}
}
