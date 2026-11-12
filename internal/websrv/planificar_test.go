package websrv_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// planWorld is a person who is asked before every change, with the coach on,
// so the coach's proposals wait in Planificar.
type planWorld struct {
	handler http.Handler
	service *training.Service
}

func newPlanWorld(t *testing.T) planWorld {
	t.Helper()
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	services := training.NewServices(store, webClock)
	id, err := store.ProvisionUser(context.Background(), "user_planner")
	if err != nil {
		t.Fatal(err)
	}
	service := services.ForUser(id)
	if err := service.SetApprovalMode(context.Background(), training.ApprovalReviewAll); err != nil {
		t.Fatal(err)
	}
	serviceFor := func(context.Context) *training.Service { return service }
	mcp := mcpsrv.New(serviceFor)
	server, err := websrv.NewWithOptions(serviceFor, services.Snapshot, webClock, "", websrv.Options{
		Apply: mcp.Apply, Coach: newFakeCoach(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return planWorld{handler: server.Handler(), service: service}
}

func (w planWorld) propose(t *testing.T, tool, action string, payload map[string]any) training.Proposal {
	t.Helper()
	raw, _ := json.Marshal(payload)
	ctx := training.WithAgentSession(context.Background(), training.CoachSessionPrefix+"chat")
	p, err := w.service.Propose(ctx, training.ProposalInput{Tool: tool, ActionType: action, Payload: raw},
		func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	return p
}

func TestPlanificarHeaderHasConversationsNewAndChips(t *testing.T) {
	w := newPlanWorld(t)
	w.propose(t, "remember", "memory.save", map[string]any{"content": "rodilla"})
	w.propose(t, "remember", "memory.save", map[string]any{"content": "hombro"})
	body := get(t, w.handler, "/?modo=planificar").Body.String()

	head := section(t, body, `id="plan-head"`, "</header>")
	for _, want := range []string{`>Planificar</h1>`, `id="menu-open"`, `aria-controls="drawer"`, `id="chat-new"`, `+ Nueva`} {
		if !strings.Contains(head, want) {
			t.Fatalf("Planificar header lacks %s:\n%s", want, head)
		}
	}
	chips := section(t, body, `id="plan-chips"`, "</nav>")
	wantOrder := []string{`>Coach<`, `>Rutinas<`, `>Revisión`, `>Memoria<`}
	at := -1
	for _, want := range wantOrder {
		i := strings.Index(chips, want)
		if i <= at {
			t.Fatalf("chips out of order or missing %s:\n%s", want, chips)
		}
		at = i
	}
	for _, want := range []string{`aria-current="page">Coach<`, `href="/plans" data-panel`, `href="/review" data-panel`, `href="/memory" data-panel`, `<span class="badge num" data-review-count>2</span>`} {
		if !strings.Contains(chips, want) {
			t.Fatalf("chips lack %s:\n%s", want, chips)
		}
	}
}

func TestPlanificarReviewChipHasNoBadgeWhenNothingWaits(t *testing.T) {
	w := newPlanWorld(t)
	chips := section(t, get(t, w.handler, "/?modo=planificar").Body.String(), `id="plan-chips"`, "</nav>")
	if !strings.Contains(chips, `data-review-count hidden>`) {
		t.Fatalf("an empty review shows a badge:\n%s", chips)
	}
}

func TestPlanificarShowsWhatTheCoachSees(t *testing.T) {
	w := newPlanWorld(t)
	ctx := context.Background()
	if _, err := w.service.CreatePlan(ctx, training.Plan{Name: "Fuerza 3 días", Items: []training.PlanItem{{Position: 1, Exercise: "sentadilla", TargetSets: 3}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.service.LogSet(ctx, "sentadilla", 100, 5, 8, 1, "", ""); err != nil {
		t.Fatal(err)
	}
	card := section(t, get(t, w.handler, "/?modo=planificar").Body.String(), `id="coach-sees"`, "</a>")
	for _, want := range []string{`El coach ve`, `1 rutina`, `1 entreno en 4 semanas`, `0 recuerdos`, `href="/memory" data-panel`} {
		if !strings.Contains(card, want) {
			t.Fatalf("context card lacks %q:\n%s", want, card)
		}
	}
}

func TestRoutineProposalIsADraftToReviewAndSave(t *testing.T) {
	w := newPlanWorld(t)
	p := w.propose(t, "create_plan", "plan.create", map[string]any{"name": "Banca 6 semanas", "items": []map[string]any{
		{"exercise": "press banca", "target_sets": 5, "rep_min": 3, "rep_max": 5},
		{"exercise": "sentadilla", "target_sets": 3, "rep_min": 5, "rep_max": 5},
	}})
	id := strconv.FormatInt(p.ID, 10)
	body := get(t, w.handler, "/?modo=planificar").Body.String()
	card := section(t, body, `class="chat-card proposal-card draft-card" data-proposal="`+id+`"`, "</article>")
	for _, want := range []string{
		`Borrador de rutina`, `Banca 6 semanas`, `2 ejercicios · 8 series`,
		`action="/review/` + id + `/accept"`, `name="answer" value="allow_once"`, `Revisar y guardar`,
		`action="/review/` + id + `/reject"`, `name="answer" value="reject_once"`, `Descartar`,
		`Ver cambios`, `+ press banca`, `5 × 3-5`, `+ sentadilla`, `3 × 5 reps`,
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("draft card lacks %q:\n%s", want, card)
		}
	}
}

func TestOtherProposalsKeepTheirCard(t *testing.T) {
	w := newPlanWorld(t)
	p := w.propose(t, "remember", "memory.save", map[string]any{"content": "rodilla"})
	card := section(t, get(t, w.handler, "/?modo=planificar").Body.String(), `data-proposal="`+strconv.FormatInt(p.ID, 10)+`"`, "</article>")
	if strings.Contains(card, "draft-card") || !strings.Contains(card, "Aceptar una vez") {
		t.Fatalf("a memory proposal changed shape:\n%s", card)
	}
}

func TestDraftAcceptStillAppliesThroughReview(t *testing.T) {
	w := newPlanWorld(t)
	p := w.propose(t, "create_plan", "plan.create", map[string]any{"name": "Banca", "items": []map[string]any{{"exercise": "press banca", "target_sets": 3}}})
	res := post(t, w.handler, "/review/"+strconv.FormatInt(p.ID, 10)+"/accept", map[string][]string{"answer": {"allow_once"}}, false)
	if res.Code >= 400 {
		t.Fatalf("accept = %d", res.Code)
	}
	got, err := w.service.ListProposals(context.Background(), training.ProposalFilter{Pending: true})
	if err != nil || len(got) != 0 {
		t.Fatalf("still pending: %v %v", got, err)
	}
}

func TestEntrenarCoachSheet(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	h := world.as(1)
	if res := post(t, h, base+"/sets", setForm("press militar", "47.5", "8", "8"), true); res.Code != http.StatusOK {
		t.Fatalf("log = %d", res.Code)
	}
	body := get(t, h, base+"/?modo=entrenar").Body.String()
	head := section(t, body, `class="coach-sheet-head"`, "</div>")
	for _, want := range []string{`class="live-dot"`, `>Coach<`, `class="live-tag">Sesión en vivo<`, `id="coach-close"`, `aria-label="Cerrar el coach"`} {
		if !strings.Contains(head, want) {
			t.Fatalf("sheet head lacks %s:\n%s", want, head)
		}
	}
	rest := section(t, body, `id="coach-rest"`, "</p>")
	if !strings.Contains(rest, "Descanso") || !strings.Contains(rest, "sigue contando") {
		t.Fatalf("rest band: %s", rest)
	}
	quick := section(t, body, `id="coach-quick"`, "</div>")
	for _, want := range []string{`data-quick="swap">Sustituir ejercicio<`, `data-quick="pain">Me duele…<`, `data-quick="add">Añadir serie<`} {
		if !strings.Contains(quick, want) {
			t.Fatalf("quick chips lack %s:\n%s", want, quick)
		}
	}
	if !strings.Contains(body, `/static/coach-apply.js`) {
		t.Fatal("the Aplicar script is not loaded")
	}
}

// The chat offers to undo what was accepted in the last 12 h, from U6's
// endpoint: the page holds the place and the script fills it.
func TestChatOffersUndoForAcceptedProposals(t *testing.T) {
	w := newPlanWorld(t)
	body := get(t, w.handler, "/?modo=planificar").Body.String()
	log := section(t, body, `id="coach-log"`, `id="chat-end"`)
	if !strings.Contains(log, `<div id="undo-cards" class="undo-cards" aria-live="polite"></div>`) {
		t.Fatalf("no undo place in the chat:\n%s", log)
	}
	if !strings.Contains(body, `/static/coach-undo.js`) {
		t.Fatal("the undo script is not loaded")
	}
	script := get(t, w.handler, "/static/coach-undo.js").Body.String()
	for _, want := range []string{"'/review/undoable'", "undo_url", "Aplicado: ", "Deshacer", "X-CSRF-Token"} {
		if !strings.Contains(script, want) {
			t.Fatalf("undo script lacks %s", want)
		}
	}
	if sw := get(t, w.handler, "/sw.js").Body.String(); !strings.Contains(sw, "BASE + '/static/coach-undo.js'") {
		t.Fatal("the undo script is not cached offline")
	}
}
