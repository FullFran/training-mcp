package websrv_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// section returns the HTML of the element whose opening tag carries marker, up
// to the closing tag given, so an assertion cannot pass on text elsewhere.
func section(t *testing.T, body, marker, closing string) string {
	t.Helper()
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("no %q in page", marker)
	}
	end := strings.Index(body[start:], closing)
	if end < 0 {
		t.Fatalf("%q is never closed by %q", marker, closing)
	}
	return body[start : start+end]
}

// The home is the chat: panel buttons go to history, plans, review and
// progress, each a plain link that also works without JavaScript.
func TestHomeIsTheChatWithPanelButtons(t *testing.T) {
	h, _ := newServer(t)
	body := get(t, h, base+"/").Body.String()
	nav := section(t, body, `class="panel-links"`, "</nav>")
	for _, want := range []string{
		`href="` + base + `/history" data-panel`,
		`href="` + base + `/plans" data-panel`,
		`href="` + base + `/review" data-panel`,
		`href="` + base + `/history#progreso" data-panel`,
	} {
		if !strings.Contains(nav, want) {
			t.Fatalf("panel buttons lack %s: %s", want, nav)
		}
	}
	for _, want := range []string{`id="coach-log"`, `id="panel-sheet"`, `id="panel-back"`, base + "/static/shell.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("home lacks %s", want)
		}
	}
	if !strings.Contains(get(t, h, base+"/history").Body.String(), `id="progreso"`) {
		t.Fatal("history has no progress anchor for the progress panel")
	}
	if w := get(t, h, base+"/static/shell.js"); w.Code != http.StatusOK {
		t.Fatalf("shell.js: %d", w.Code)
	}
}

// With no workout the card collapses; once a set is logged it shows the
// workout, built from the same entry form, panel and rest timer as before.
func TestWorkoutCardCollapsesWithoutAWorkout(t *testing.T) {
	h, _ := newServer(t)
	idle := section(t, get(t, h, base+"/").Body.String(), `id="workout"`, `id="wk-more"`)
	if !strings.Contains(idle, `class="workout idle"`) || !strings.Contains(idle, "Sin entreno activo") {
		t.Fatalf("idle workout card: %s", idle)
	}

	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)
	body := get(t, h, base+"/").Body.String()
	card := section(t, body, `id="workout"`, "</footer>")
	if strings.Contains(card, `class="workout idle"`) {
		t.Fatal("workout card stays collapsed with a set logged")
	}
	for _, want := range []string{`id="wk-log"`, `id="rest"`, `id="entry"`} {
		if !strings.Contains(card, want) {
			t.Fatalf("workout card lacks %s", want)
		}
	}
	// The logged set shows on the training screen above the dock.
	if train := section(t, body, `id="train"`, "</main>"); !strings.Contains(train, `id="panel"`) || !strings.Contains(train, "80 kg × 8") {
		t.Fatalf("training screen lacks the set: %s", train)
	}
}

// The plan of the day is a card in the chat, rendered by the server.
func TestChatShowsThePlanOfTheDay(t *testing.T) {
	h, service := newServer(t)
	plan, err := service.CreatePlan(t.Context(), training.Plan{Name: "Empuje A", Items: []training.PlanItem{
		{Exercise: "banca", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9},
		{Exercise: "fondos", TargetSets: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pick := section(t, get(t, h, base+"/").Body.String(), `id="chat-cards"`, `id="chat-end"`)
	if !strings.Contains(pick, `class="chat-card plan-pick"`) || !strings.Contains(pick, "Empuje A · 5 series") {
		t.Fatalf("chat does not offer the plans: %s", pick)
	}

	post(t, h, base+"/plan", url.Values{"plan_id": {itoa(plan.ID)}}, false)
	post(t, h, base+"/sets", setForm("banca", "80", "9", "9"), true)
	cards := section(t, get(t, h, base+"/").Body.String(), `id="chat-cards"`, `id="chat-end"`)
	for _, want := range []string{`class="chat-card plan-today"`, "Empuje A", "0/2", "banca", "1/3", "8-10 reps @9", "fondos"} {
		if !strings.Contains(cards, want) {
			t.Fatalf("plan of the day card lacks %q: %s", want, cards)
		}
	}
}

// A pending proposal is a card with accept and reject, posting to the same
// CSRF-protected endpoints as /review.
func TestChatShowsPendingProposalsWithAcceptAndReject(t *testing.T) {
	f := newReviewFixture(t)
	p := f.propose(t, "fuerza")
	cards := section(t, get(t, f.handler, "/").Body.String(), `id="chat-cards"`, `id="chat-end"`)
	for _, want := range []string{
		`data-proposal="` + itoa(p.ID) + `"`, "Crear rutina", "fuerza",
		`action="/review/` + itoa(p.ID) + `/accept"`, `action="/review/` + itoa(p.ID) + `/reject"`,
		`name="csrf_token" value="tok"`,
	} {
		if !strings.Contains(cards, want) {
			t.Fatalf("proposal card lacks %s: %s", want, cards)
		}
	}

	// The card's form is the review endpoint itself: accepting applies it.
	w := post(t, f.handler, "/review/"+itoa(p.ID)+"/accept", url.Values{"remember": {"once"}}, true)
	if w.Code != http.StatusOK || len(f.plans(t)) != 1 {
		t.Fatalf("accept from the card: %d, plans %d", w.Code, len(f.plans(t)))
	}
	if strings.Contains(get(t, f.handler, "/").Body.String(), `data-proposal="`) {
		t.Fatal("an answered proposal is still offered in the chat")
	}
}

// The chat cards are each person's own: the second person sees neither the
// owner's proposal nor the owner's plan.
func TestChatCardsAreEachPersonsOwn(t *testing.T) {
	world := newWebWorld(t)
	owner := get(t, world.owner, base+"/").Body.String()
	if !strings.Contains(owner, `data-proposal="`+world.proposal+`"`) || !strings.Contains(owner, secretPlan) {
		t.Fatal("the owner does not see their own proposal and plan")
	}
	second := get(t, world.second, base+"/").Body.String()
	for _, leak := range []string{`data-proposal="`, secretPlan, secretExercise} {
		if strings.Contains(second, leak) {
			t.Fatalf("the second person's chat shows %q", leak)
		}
	}
}

// The composer has the voice controls and the slot the photo button mounts in.
func TestComposerHasVoiceAndTheExtrasSlot(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	body := get(t, world.as(1), base+"/").Body.String()
	composer := section(t, body, `id="coach-form"`, "</form>")
	for _, want := range []string{`id="composer-extras"`, `id="coach-mic"`, `id="coach-text"`, `id="coach-send"`} {
		if !strings.Contains(composer, want) {
			t.Fatalf("composer lacks %s: %s", want, composer)
		}
	}
	if !strings.Contains(body, base+"/static/markdown.js") {
		t.Fatal("the coach page does not load the reply renderer")
	}
	// Dictation only: replies are not read aloud.
	if !strings.Contains(body, `id="coach-autosend"`) || strings.Contains(body, `coach-speak`) {
		t.Fatal("voice controls: want the dictation auto-send switch and no read-aloud")
	}
	for _, file := range []string{"static/coach.js", "static/shell.js", "static/app.js"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "speechSynthesis") || strings.Contains(string(src), "SpeechSynthesis") {
			t.Fatalf("%s still reads replies aloud", file)
		}
	}
	// The workout and the panels do not depend on the coach.
	for _, want := range []string{`id="workout"`, `class="panel-links"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("coach page lacks %s", want)
		}
	}
}

// The new shell is cached for offline use under a new version.
func TestServiceWorkerCachesTheChatShell(t *testing.T) {
	h, _ := newServer(t)
	body := get(t, h, base+"/sw.js").Body.String()
	for _, want := range []string{"const VERSION = 'v13';", "BASE + '/static/shell.js'", "BASE + '/static/modes.js'", "BASE + '/static/coach.js'", "BASE + '/static/markdown.js'", "BASE + '/static/coach-attach.js'"} {
		if !strings.Contains(body, want) {
			t.Fatalf("service worker lacks %s", want)
		}
	}
}

// The proposal card and /review say what the change does in words; the raw
// payload stays one tap away on /review only.
func TestProposalsReadAsASentence(t *testing.T) {
	f := newReviewFixture(t)
	f.propose(t, "Pierna <b>coach</b>")
	want := "Crear rutina «Pierna &lt;b&gt;coach&lt;/b&gt;»: sentadilla 3 series"
	cards := section(t, get(t, f.handler, "/").Body.String(), `id="chat-cards"`, `id="chat-end"`)
	if !strings.Contains(cards, want) || strings.Contains(cards, "target_sets") {
		t.Fatalf("chat card: %s", cards)
	}
	review := get(t, f.handler, "/review").Body.String()
	if !strings.Contains(review, want) {
		t.Fatalf("review page lacks the summary")
	}
	if !strings.Contains(section(t, review, `<details class="raw"`, "</details>"), "target_sets") {
		t.Fatal("review page no longer offers the raw payload")
	}
}

// A type without a summary keeps the key/value view everywhere.
func TestUnknownProposalsKeepTheirFields(t *testing.T) {
	f := newReviewFixture(t)
	_, err := f.service.Propose(t.Context(), training.ProposalInput{Tool: "a_future_tool", ActionType: "future.thing", Payload: []byte(`{"widget":"azul"}`)},
		func(context.Context) (json.RawMessage, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/review"} {
		body := get(t, f.handler, path).Body.String()
		if !strings.Contains(body, "<dt>widget</dt><dd>azul</dd>") {
			t.Fatalf("%s lacks the fields of an unknown proposal", path)
		}
	}
}

// The home reads like ChatGPT: a top bar with the menu, the panels and the
// account settings in a drawer, the conversation, and at the bottom the
// workout bar above the message box, which has one send/stop place.
func TestHomeIsAChatGPTStyleShell(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	h := world.as(1)
	post(t, h, base+"/sets", setForm("banca", "80", "8", "8"), true)
	body := get(t, h, base+"/").Body.String()

	if bar := section(t, body, `class="bar chat-bar"`, "</header>"); !strings.Contains(bar, `id="menu-open"`) || !strings.Contains(bar, `aria-controls="drawer"`) {
		t.Fatalf("top bar has no menu button: %s", bar)
	}
	drawer := section(t, body, `id="drawer"`, "</aside>")
	for _, want := range []string{`class="panel-links"`, `id="review-link"`, `id="coach-state"`, `id="coach-autosend"`, `id="coach-disconnect"`} {
		if !strings.Contains(drawer, want) {
			t.Fatalf("drawer lacks %s: %s", want, drawer)
		}
	}
	composer := section(t, body, `id="coach-form"`, "</form>")
	for _, want := range []string{`id="coach-send"`, `id="coach-stop"`, `id="coach-mic"`, `id="composer-extras"`} {
		if !strings.Contains(composer, want) {
			t.Fatalf("composer lacks %s: %s", want, composer)
		}
	}
	for _, unwanted := range []string{`id="coach-disconnect"`, `id="coach-autosend"`, `id="coach-state"`} {
		if strings.Contains(composer, unwanted) {
			t.Fatalf("settings %s still crowd the composer", unwanted)
		}
	}
	// The coach sheet holds the conversation and then the message box; the
	// workout bar is Entrenar's dock, outside it.
	sheet := section(t, body, `id="coach-sheet"`, `class="dock"`)
	chat, form := strings.Index(sheet, `id="coach-log"`), strings.Index(sheet, `id="coach-form"`)
	if chat < 0 || form < chat || strings.Contains(sheet, `id="workout"`) || !strings.Contains(section(t, body, `class="dock"`, "</footer>"), `id="workout"`) {
		t.Fatalf("want chat then composer in the coach sheet, workout in the dock: %d %d", chat, form)
	}
	if sheet := section(t, body, `id="wk-more"`, `id="entry"`); !strings.Contains(sheet, `class="wk-sheet"`) || !strings.Contains(sheet, `data-close-sheet`) {
		t.Fatalf("the entry form is not a closable sheet: %s", sheet)
	}
	// Empty chat: suggestions written by the server, never by the model.
	empty := section(t, body, `id="chat-empty"`, `id="chat-end"`)
	for _, want := range []string{`data-suggest="¿Cómo voy hoy?"`, `data-suggest="¿Cómo voy esta semana?"`} {
		if !strings.Contains(empty, want) {
			t.Fatalf("empty chat lacks %s: %s", want, empty)
		}
	}
	if !strings.Contains(body, `id="chat-jump"`) {
		t.Fatal("no jump-to-latest button")
	}
}

// The model and thinking picker: the title opens it, a chip in the composer
// shows the thinking level, and the sheet's lists are filled from the person's
// own engine, never written into the page.
func TestHomeHasTheModelPicker(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	body := get(t, world.as(1), base+"/").Body.String()
	if bar := section(t, body, `class="bar chat-bar"`, "</header>"); !strings.Contains(bar, `id="model-open"`) || !strings.Contains(bar, `aria-controls="model-sheet"`) {
		t.Fatalf("title does not open the picker: %s", bar)
	}
	sheet := section(t, body, `id="model-sheet"`, "</section>")
	for _, want := range []string{`id="model-list"`, `id="thinking-levels"`, `id="model-close"`} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("picker lacks %s: %s", want, sheet)
		}
	}
	if !strings.Contains(section(t, body, `id="coach-form"`, "</form>"), `id="thinking-chip"`) {
		t.Fatal("composer has no thinking chip")
	}
	h, _ := newServer(t)
	if off := get(t, h, base+"/").Body.String(); strings.Contains(off, `id="model-sheet"`) || strings.Contains(off, `id="model-open"`) {
		t.Fatal("picker shown with the coach off")
	}
}

// Conversations live in the drawer with a new-chat button in the top bar and
// in the drawer, and «borrar todo» next to the account; the list itself is
// filled from the person's own conversations, never written into the page.
func TestHomeHasTheConversationList(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	body := get(t, world.as(1), base+"/").Body.String()
	if bar := section(t, body, `class="bar chat-bar"`, "</header>"); !strings.Contains(bar, `id="chat-new"`) {
		t.Fatalf("no new chat in the top bar: %s", bar)
	}
	drawer := section(t, body, `id="drawer"`, "</aside>")
	for _, want := range []string{`id="drawer-new"`, `id="conversations"`, `id="conv-delete-all"`, "90 días"} {
		if !strings.Contains(drawer, want) {
			t.Fatalf("drawer lacks %s: %s", want, drawer)
		}
	}
	if strings.Contains(drawer, "Pierna") {
		t.Fatal("a conversation title was written into the page")
	}
	h, _ := newServer(t)
	if off := get(t, h, base+"/").Body.String(); strings.Contains(off, `id="conversations"`) || strings.Contains(off, `id="chat-new"`) {
		t.Fatal("conversations shown with the coach off")
	}
}
