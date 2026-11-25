package websrv_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// blocksWorld serves the app over a database the test also reads, with two
// routines to rotate.
type blocksWorld struct {
	h    http.Handler
	svc  *training.Service
	db   *sql.DB
	a, b training.Plan
}

func newBlocksWorld(t *testing.T, csrf string) blocksWorld {
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
	services := training.NewServices(store, webClock)
	svc := services.ForUser(1)
	opts := websrv.Options{}
	if csrf != "" {
		opts.CSRFToken = func(*http.Request) string { return csrf }
	}
	server, err := websrv.NewWithOptions(func(context.Context) *training.Service { return svc }, services.Snapshot, webClock, base, opts)
	if err != nil {
		t.Fatal(err)
	}
	w := blocksWorld{h: server.Handler(), svc: svc, db: db}
	if w.a, err = svc.CreatePlan(context.Background(), training.Plan{Name: "Pierna A", Items: []training.PlanItem{{Exercise: "sentadilla", TargetSets: 3}}}); err != nil {
		t.Fatal(err)
	}
	if w.b, err = svc.CreatePlan(context.Background(), training.Plan{Name: "Torso B", Items: []training.PlanItem{{Exercise: "banca", TargetSets: 3}}}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w blocksWorld) page(t *testing.T, path string) string {
	t.Helper()
	r := get(t, w.h, base+path)
	if r.Code != http.StatusOK {
		t.Fatalf("GET %s: %d\n%s", path, r.Code, r.Body)
	}
	return r.Body.String()
}

// send posts a plain form, as the page's forms do without JavaScript.
func (w blocksWorld) send(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, w.h, base+path, form, false)
}

func (w blocksWorld) block(t *testing.T, id int64) training.BlockView {
	t.Helper()
	v, err := w.svc.GetBlock(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// activeBlock seeds, through the domain, an active sequence block that started
// on 1 Aug: microcycle 1 done (both routines), microcycle 2 current with Pierna
// A done, microcycle 3 an upcoming deload, and a session before the start kept
// off-plan.
func (w blocksWorld) activeBlock(t *testing.T) (training.BlockView, int64) {
	t.Helper()
	ctx := context.Background()
	intent, count := "Hipertrofia de verano", 3
	rotation := []int64{w.a.ID, w.b.ID}
	label, sets, effort := "descarga", -2, -1.0
	v, err := w.svc.SaveBlock(ctx, training.BlockInput{Intent: &intent, MicrocycleCount: &count, Rotation: &rotation,
		Microcycles: []training.MicrocycleInput{{Position: 3, Label: &label, SetsDelta: &sets, EffortDelta: &effort}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.AdvanceBlock(ctx, training.AdvanceInput{BlockID: v.ID, Action: training.BlockActivate, StartDate: "2026-08-01"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		date string
		plan int64
	}{{"2026-08-02", w.a.ID}, {"2026-08-03", w.b.ID}, {"2026-08-05", w.a.ID}} {
		if _, err = w.svc.StartPlannedSession(ctx, s.date, s.plan); err != nil {
			t.Fatal(err)
		}
	}
	off, err := w.svc.StartSession(ctx, "2026-07-30")
	if err != nil {
		t.Fatal(err)
	}
	return w.block(t, v.ID), off.ID
}

// section is the page from one marker up to the next, or to its end.
func pzSection(t *testing.T, body, from, to string) string {
	t.Helper()
	i := strings.Index(body, from)
	if i < 0 {
		t.Fatalf("no %q in:\n%s", from, body)
	}
	rest := body[i:]
	if to != "" {
		if j := strings.Index(rest[len(from):], to); j >= 0 {
			return rest[:len(from)+j]
		}
	}
	return rest
}

func TestBlocksEmptyStateSaysBlocksAreOptionalAndOffersASketch(t *testing.T) {
	w := newBlocksWorld(t, "")
	body := w.page(t, "/blocks")
	for _, want := range []string{
		"Los bloques son opcionales",
		"una sola rutina sigue funcionando",
		`action="` + base + `/blocks/save"`, `name="intent"`, `name="microcycle_count"`,
		`action="` + base + `/blocks/goal"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("empty state lacks %q:\n%s", want, body)
		}
	}
	// No suggested length: the count is the person's to type.
	count := regexp.MustCompile(`<input[^>]*name="microcycle_count"[^>]*>`).FindString(body)
	if count == "" || strings.Contains(count, "value=") {
		t.Errorf("the microcycle count suggests a length: %q", count)
	}
	// The screen is reached from Planificar's chips, and training is untouched.
	home := get(t, w.h, base+"/").Body.String()
	if !strings.Contains(home, `href="`+base+`/blocks"`) {
		t.Error("Planificar does not link the blocks screen")
	}
	if strings.Contains(home, "Los bloques son opcionales") {
		t.Error("the home shows the blocks empty state")
	}
}

func TestAGoalAndASketchBlockAreCreatedByHand(t *testing.T) {
	w := newBlocksWorld(t, "")
	r := w.send(t, "/blocks/goal", url.Values{"text": {"Banca 100 kg"}, "target_date": {"2026-12-20"}})
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != base+"/blocks" {
		t.Fatalf("goal: %d %q\n%s", r.Code, r.Header().Get("Location"), r.Body)
	}
	list, err := w.svc.ListBlocks(context.Background())
	if err != nil || len(list.Goals) != 1 || list.Goals[0].Text != "Banca 100 kg" || list.Goals[0].TargetDate != "2026-12-20" || list.Goals[0].Status != training.GoalActive {
		t.Fatalf("goals = %+v, %v", list.Goals, err)
	}
	goal := itoa(list.Goals[0].ID)

	r = w.send(t, "/blocks/save", url.Values{"intent": {"Base de fuerza"}, "microcycle_count": {"4"}, "goal_id": {goal}})
	if list, err = w.svc.ListBlocks(context.Background()); err != nil || len(list.Blocks) != 1 {
		t.Fatalf("blocks = %+v, %v", list.Blocks, err)
	}
	b := list.Blocks[0]
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != base+"/blocks?bloque="+itoa(b.ID) {
		t.Fatalf("save: %d %q", r.Code, r.Header().Get("Location"))
	}
	if b.Intent != "Base de fuerza" || b.Status != training.BlockSketch || b.MicrocycleCount != 4 || b.Advance != training.AdvanceSequence ||
		b.MicrocycleDays != 7 || itoa(b.GoalID) != goal {
		t.Fatalf("stored block = %+v", b)
	}

	body := w.page(t, "/blocks")
	for _, want := range []string{"Banca 100 kg", "Base de fuerza", "Borrador", `href="` + base + `/blocks?bloque=` + itoa(b.ID) + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Los bloques son opcionales") {
		t.Error("the empty state is still shown")
	}
	detail := w.page(t, "/blocks?bloque="+itoa(b.ID))
	for _, want := range []string{`name="block_id" value="` + itoa(b.ID) + `"`, `name="action" value="activate"`, "4 microciclos"} {
		if !strings.Contains(detail, want) {
			t.Errorf("sketch page lacks %q:\n%s", want, detail)
		}
	}

	// A refused create says why and stores nothing.
	r = w.send(t, "/blocks/save", url.Values{"intent": {"  "}, "microcycle_count": {"4"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), `role="alert"`) {
		t.Fatalf("blank intent: %d\n%s", r.Code, r.Body)
	}
	if list, _ = w.svc.ListBlocks(context.Background()); len(list.Blocks) != 1 {
		t.Fatalf("a refused block was stored: %+v", list.Blocks)
	}
}

func TestTheActiveBlockShowsItsMicrocyclesRotationAndWhatIsDue(t *testing.T) {
	w := newBlocksWorld(t, "")
	v, off := w.activeBlock(t)
	body := w.page(t, "/blocks")
	for _, want := range []string{"Hipertrofia de verano", "Activo", "Por secuencia", "microciclos de 7 días"} {
		if !strings.Contains(body, want) {
			t.Errorf("block header lacks %q", want)
		}
	}
	timeline := pzSection(t, body, `class="pz-timeline"`, "</ol>")
	for _, want := range []string{"Hecho", "Actual", "Por hacer", "descarga", "−2 series", "RPE −1", "2/2", "1/2"} {
		if !strings.Contains(timeline, want) {
			t.Errorf("timeline lacks %q:\n%s", want, timeline)
		}
	}
	current := pzSection(t, body, `id="pz-current"`, "</section>")
	if !regexp.MustCompile(`Pierna A[^<]*</span>\s*<span[^>]*>hecha`).MatchString(current) ||
		!regexp.MustCompile(`Torso B[^<]*</span>\s*<span[^>]*>pendiente`).MatchString(current) {
		t.Errorf("the current microcycle does not say which routine is done or pending:\n%s", current)
	}
	next := pzSection(t, body, `id="pz-next"`, "</form>")
	for _, want := range []string{`action="` + base + `/plan"`, `name="plan_id" value="` + itoa(w.b.ID) + `"`, "Torso B", "Empezar"} {
		if !strings.Contains(next, want) {
			t.Errorf("what is due next lacks %q:\n%s", want, next)
		}
	}
	offPlan := pzSection(t, body, `id="pz-off-plan"`, "</section>")
	if !strings.Contains(offPlan, `href="`+base+`/s/`+itoa(off)+`"`) {
		t.Errorf("the off-plan session is not listed:\n%s", offPlan)
	}
	// Only the remaining microcycles have an edit form.
	if strings.Contains(body, `name="position" value="1"`) || !strings.Contains(body, `name="position" value="2"`) || !strings.Contains(body, `name="position" value="3"`) {
		t.Errorf("edit forms are not limited to the remaining microcycles")
	}
	// The sequence block can be moved on by hand, and closed.
	for _, action := range []string{"complete_microcycle", "skip_microcycle", "close"} {
		if !strings.Contains(body, `name="action" value="`+action+`"`) {
			t.Errorf("no %s action", action)
		}
	}
	if v.Microcycles[0].State != training.StateDone || v.Microcycles[1].State != training.StateCurrent {
		t.Fatalf("seed = %+v", v.Microcycles)
	}
}

// Starting what is due goes through the existing start flow, and the session
// counts in the current microcycle, which closes once its rotation is done.
func TestStartingTheDueRoutineCountsInTheCurrentMicrocycle(t *testing.T) {
	w := newBlocksWorld(t, "")
	v, _ := w.activeBlock(t)
	r := w.send(t, "/plan", url.Values{"plan_id": {itoa(w.b.ID)}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", r.Code)
	}
	after := w.block(t, v.ID)
	m := after.Microcycles[1]
	if m.State != training.StateDone || len(m.Sessions) != 2 || m.Sessions[1].Date != "2026-08-07" || m.Sessions[1].PlanID != w.b.ID {
		t.Fatalf("microcycle 2 after starting Torso B: %+v", m)
	}
}

func TestAForwardOnlyEditIsRefusedWithTheReasonAndAValidOneIsStored(t *testing.T) {
	w := newBlocksWorld(t, "")
	v, _ := w.activeBlock(t)
	id := itoa(v.ID)
	r := w.send(t, "/blocks/microcycle", url.Values{"block_id": {id}, "position": {"1"}, "label": {"cambiado"}, "rotation_mode": {"block"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), `role="alert"`) || !strings.Contains(r.Body.String(), "Solo se pueden cambiar los microciclos que quedan") {
		t.Fatalf("editing a done microcycle: %d\n%s", r.Code, r.Body)
	}
	if got := w.block(t, v.ID).Microcycles[0]; got.Label != "" {
		t.Fatalf("a done microcycle changed: %+v", got)
	}

	r = w.send(t, "/blocks/microcycle", url.Values{"block_id": {id}, "position": {"3"}, "label": {"pico"}, "sets_delta": {"1"}, "effort_delta": {"0.5"},
		"rotation_mode": {"own"}, "rotation": {"", itoa(w.b.ID), ""}})
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != base+"/blocks" {
		t.Fatalf("edit: %d %q\n%s", r.Code, r.Header().Get("Location"), r.Body)
	}
	m := w.block(t, v.ID).Microcycles[2]
	if m.Label != "pico" || m.SetsDelta != 1 || m.EffortDelta != 0.5 || !m.OwnRotation || len(m.Rotation) != 1 || m.Rotation[0].PlanID != w.b.ID {
		t.Fatalf("microcycle 3 = %+v", m)
	}
	// Back to the block's rotation, with the modifiers cleared.
	w.send(t, "/blocks/microcycle", url.Values{"block_id": {id}, "position": {"3"}, "label": {"pico"}, "sets_delta": {""}, "effort_delta": {""}, "rotation_mode": {"block"}})
	if m = w.block(t, v.ID).Microcycles[2]; m.OwnRotation || m.SetsDelta != 0 || m.EffortDelta != 0 || len(m.Rotation) != 2 {
		t.Fatalf("microcycle 3 after inheriting = %+v", m)
	}
}

func TestHandEditsMoveTheBlockForwardAndPlaceSessions(t *testing.T) {
	w := newBlocksWorld(t, "")
	v, off := w.activeBlock(t)
	id := itoa(v.ID)
	advance := func(action string) {
		t.Helper()
		if r := w.send(t, "/blocks/advance", url.Values{"block_id": {id}, "action": {action}}); r.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d\n%s", action, r.Code, r.Body)
		}
	}
	advance("complete_microcycle")
	advance("skip_microcycle")
	got := w.block(t, v.ID)
	if got.Microcycles[1].Status != training.MicrocycleDone || got.Microcycles[2].Status != training.MicrocycleSkipped {
		t.Fatalf("after complete and skip: %+v", got.Microcycles)
	}

	// The off-plan session moves into microcycle 1, and one of microcycle 1's out.
	m1 := itoa(got.Microcycles[0].ID)
	if r := w.send(t, "/blocks/assign", url.Values{"session_id": {itoa(off)}, "microcycle_id": {m1}}); r.Code != http.StatusSeeOther {
		t.Fatalf("assign: %d\n%s", r.Code, r.Body)
	}
	moved := got.Microcycles[0].Sessions[0].SessionID
	if r := w.send(t, "/blocks/assign", url.Values{"session_id": {itoa(moved)}, "microcycle_id": {"off_plan"}}); r.Code != http.StatusSeeOther {
		t.Fatalf("off-plan: %d\n%s", r.Code, r.Body)
	}
	got = w.block(t, v.ID)
	ids := map[int64]bool{}
	for _, s := range got.Microcycles[0].Sessions {
		ids[s.SessionID] = true
	}
	if !ids[off] || ids[moved] || len(got.OffPlan) != 1 || got.OffPlan[0].SessionID != moved {
		t.Fatalf("after moving: microcycle 1 %+v, off-plan %+v", got.Microcycles[0].Sessions, got.OffPlan)
	}

	// Skipping past the end is refused, with the reason.
	r := w.send(t, "/blocks/advance", url.Values{"block_id": {id}, "action": {"skip_microcycle"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), `role="alert"`) {
		t.Fatalf("skip with nothing left: %d\n%s", r.Code, r.Body)
	}

	advance("close")
	if got = w.block(t, v.ID); got.Status != training.BlockDone || got.EndedOn != "2026-08-07" {
		t.Fatalf("after close: %+v", got)
	}
	body := w.page(t, "/blocks")
	if strings.Contains(body, `id="pz-current"`) || !strings.Contains(body, "Terminado") {
		t.Errorf("a closed block still shows as active:\n%s", body)
	}
	// A done block is never edited.
	r = w.send(t, "/blocks/save", url.Values{"block_id": {id}, "intent": {"otro"}, "microcycle_count": {"3"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), `role="alert"`) {
		t.Fatalf("editing a done block: %d\n%s", r.Code, r.Body)
	}
	if w.block(t, v.ID).Intent != "Hipertrofia de verano" {
		t.Fatal("a done block was edited")
	}
}

func TestEditingABlockChangesItsShape(t *testing.T) {
	w := newBlocksWorld(t, "")
	w.send(t, "/blocks/save", url.Values{"intent": {"Boceto"}, "microcycle_count": {"3"}})
	list, _ := w.svc.ListBlocks(context.Background())
	id := itoa(list.Blocks[0].ID)
	r := w.send(t, "/blocks/save", url.Values{"block_id": {id}, "intent": {"Fuerza máxima"}, "goal_id": {""}, "advance": {"calendar"},
		"microcycle_days": {"9"}, "microcycle_count": {"5"}, "status": {"planned"}, "rotation": {itoa(w.b.ID), "", itoa(w.a.ID), ""}})
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != base+"/blocks?bloque="+id {
		t.Fatalf("edit: %d %q\n%s", r.Code, r.Header().Get("Location"), r.Body)
	}
	got := w.block(t, list.Blocks[0].ID)
	if got.Intent != "Fuerza máxima" || got.Advance != training.AdvanceCalendar || got.MicrocycleDays != 9 || got.MicrocycleCount != 5 ||
		got.Status != training.BlockPlanned || len(got.Rotation) != 2 || got.Rotation[0].PlanID != w.b.ID || got.Rotation[1].PlanID != w.a.ID {
		t.Fatalf("edited block = %+v", got)
	}
	w.send(t, "/blocks/save", url.Values{"block_id": {id}, "intent": {"Fuerza máxima"}, "advance": {"calendar"}, "microcycle_days": {"9"},
		"microcycle_count": {"2"}, "status": {"planned"}, "rotation": {itoa(w.b.ID)}})
	if got = w.block(t, list.Blocks[0].ID); got.MicrocycleCount != 2 || len(got.Rotation) != 1 {
		t.Fatalf("shrunk block = %+v", got)
	}
	// A number that is not one is refused before the domain is called.
	if r := w.send(t, "/blocks/save", url.Values{"block_id": {id}, "intent": {"x"}, "microcycle_count": {"dos"}}); r.Code != http.StatusBadRequest {
		t.Fatalf("bad count: %d", r.Code)
	}
	// Activating a second block while one is active is refused with the reason.
	if r := w.send(t, "/blocks/advance", url.Values{"block_id": {id}, "action": {"activate"}, "start_date": {"2026-08-10"}}); r.Code != http.StatusSeeOther {
		t.Fatalf("activate: %d\n%s", r.Code, r.Body)
	}
	if got = w.block(t, list.Blocks[0].ID); got.Status != training.BlockActive || got.StartDate != "2026-08-10" {
		t.Fatalf("activated block = %+v", got)
	}
	w.send(t, "/blocks/save", url.Values{"intent": {"Otro"}, "microcycle_count": {"2"}})
	list, _ = w.svc.ListBlocks(context.Background())
	r = w.send(t, "/blocks/advance", url.Values{"block_id": {itoa(list.Blocks[1].ID)}, "action": {"activate"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "otro bloque activo") {
		t.Fatalf("second activation: %d\n%s", r.Code, r.Body)
	}
}

// Every write form carries the session's CSRF token, or the login middleware
// refuses it.
func TestEveryBlocksFormCarriesTheCSRFToken(t *testing.T) {
	w := newBlocksWorld(t, "tok-blocks")
	for _, path := range []string{"/blocks"} {
		body := w.page(t, path)
		if n := strings.Count(body, `<form method="post"`); n == 0 || n != strings.Count(body, `<input type="hidden" name="csrf_token" value="tok-blocks">`) {
			t.Errorf("%s (empty): %d forms, %d tokens", path, n, strings.Count(body, `name="csrf_token"`))
		}
	}
	v, _ := w.activeBlock(t)
	w.send(t, "/blocks/save", url.Values{"intent": {"Boceto"}, "microcycle_count": {"3"}})
	for _, path := range []string{"/blocks", "/blocks?bloque=" + itoa(v.ID)} {
		body := w.page(t, path)
		if n := strings.Count(body, `<form method="post"`); n < 8 || n != strings.Count(body, `<input type="hidden" name="csrf_token" value="tok-blocks">`) {
			t.Errorf("%s: %d forms, %d tokens", path, n, strings.Count(body, `name="csrf_token"`))
		}
		if strings.Contains(body, `<form method="get"`) || strings.Contains(body, "<form>") {
			t.Errorf("%s has a form that is not a post", path)
		}
	}
}

func TestABlockPageForAMissingBlockIsNotFound(t *testing.T) {
	w := newBlocksWorld(t, "")
	for _, q := range []string{"999", "x", "-1"} {
		if r := get(t, w.h, base+"/blocks?bloque="+q); r.Code != http.StatusNotFound {
			t.Errorf("?bloque=%s: %d", q, r.Code)
		}
	}
}
