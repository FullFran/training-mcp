package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// webHandlerAt serves one service with its own clock for the page.
func webHandlerAt(t *testing.T, svc *training.Service, clock training.Clock) http.Handler {
	t.Helper()
	server, err := websrv.NewWithOptions(func(context.Context) *training.Service { return svc }, nil, clock, base, websrv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func memoryWeb(t *testing.T) (http.Handler, *training.Service) {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	services := training.NewServices(store, webClock)
	return webHandler(t, services, 1), services.ForUser(1)
}

func TestMemoryPanelListsSearchesAndFilters(t *testing.T) {
	ctx := context.Background()
	h, svc := memoryWeb(t)
	for _, in := range []training.MemoryInput{
		{Type: "limitation", Title: "Rodilla izquierda", Body: "Evitar sentadilla profunda"},
		{Type: "goal", Title: "Dominadas", Body: "Diez seguidas en verano"},
	} {
		if _, err := svc.SaveMemory(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	all := get(t, h, base+"/memory").Body.String()
	if !strings.Contains(all, "Rodilla izquierda") || !strings.Contains(all, "Dominadas") || !strings.Contains(all, "Limitación") {
		t.Fatalf("list:\n%s", all)
	}
	found := get(t, h, base+"/memory?q=sentadilla").Body.String()
	if !strings.Contains(found, "Rodilla izquierda") || strings.Contains(found, "Dominadas") {
		t.Fatalf("search:\n%s", found)
	}
	typed := get(t, h, base+"/memory?type=goal").Body.String()
	if strings.Contains(typed, "Rodilla izquierda") || !strings.Contains(typed, "Dominadas") {
		t.Fatalf("type filter:\n%s", typed)
	}
	if w := get(t, h, base+"/memory?type=secret"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown type status = %d", w.Code)
	}
	if empty := get(t, h, base+"/memory?q=nada").Body.String(); !strings.Contains(empty, "No hay nada") {
		t.Fatalf("empty search:\n%s", empty)
	}
}

// The person edits and deletes directly: these are not agent writes, so no proposal.
// Deleting from the panel is for good, not a soft forget.
func TestMemoryPanelEditsAndDeletesDirectly(t *testing.T) {
	ctx := context.Background()
	h, svc := memoryWeb(t)
	saved, err := svc.SaveMemory(ctx, training.MemoryInput{Title: "Agarre", Body: "Neutro"})
	if err != nil {
		t.Fatal(err)
	}
	m := saved.Memory
	id := itoa(m.ID)
	w := post(t, h, base+"/memory/"+id+"/update", url.Values{"title": {"Agarre en banca"}, "body": {"Prono, a 1,5 palmos"}, "type": {"preference"}, "pinned": {"1"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/memory" {
		t.Fatalf("update: %d %s", w.Code, w.Header().Get("Location"))
	}
	got, err := svc.GetMemory(ctx, m.ID)
	if err != nil || got.Title != "Agarre en banca" || got.Body != "Prono, a 1,5 palmos" || got.Type != "preference" || !got.Pinned {
		t.Fatalf("after update = %#v, %v", got, err)
	}
	// Unticking pinned unpins.
	post(t, h, base+"/memory/"+id+"/update", url.Values{"title": {"Agarre en banca"}, "body": {"Prono"}, "type": {"preference"}}, false)
	if got, _ = svc.GetMemory(ctx, m.ID); got.Pinned {
		t.Fatal("still pinned")
	}
	if w = post(t, h, base+"/memory/"+id+"/update", url.Values{"title": {" "}, "body": {"x"}, "type": {"note"}}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("empty title status = %d", w.Code)
	}
	if w = post(t, h, base+"/memory/"+id+"/delete", nil, true); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Agarre en banca") {
		t.Fatalf("delete: %d\n%s", w.Code, w.Body)
	}
	if _, err = svc.GetMemory(ctx, m.ID); err == nil {
		t.Fatal("deleted memory still there")
	}
	proposals, err := svc.ListProposals(ctx, training.ProposalFilter{Limit: 10})
	if err != nil || len(proposals) != 0 {
		t.Fatalf("panel writes made proposals: %v, %v", proposals, err)
	}
	if w = post(t, h, base+"/memory/abc/delete", nil, true); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d", w.Code)
	}
}

func TestReviewPageLinksToMemory(t *testing.T) {
	h, _ := memoryWeb(t)
	if body := get(t, h, base+"/review").Body.String(); !strings.Contains(body, `href="`+base+`/memory"`) {
		t.Fatalf("no memory link:\n%s", body)
	}
}

func saveAll(t *testing.T, svc *training.Service, ins ...training.MemoryInput) []training.MemorySaved {
	t.Helper()
	var out []training.MemorySaved
	for _, in := range ins {
		saved, err := svc.SaveMemory(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, saved)
	}
	return out
}

func TestMemoryPanelShowsEachView(t *testing.T) {
	ctx := context.Background()
	h, svc := memoryWeb(t)
	saved := saveAll(t, svc,
		training.MemoryInput{Type: "limitation", Title: "Hombro lesionado", Body: "nada de press"},
		training.MemoryInput{Type: "limitation", Title: "Hombro recuperado", Body: "press ligero"},
		training.MemoryInput{Type: "equipment", Title: "Mancuernas", Body: "hasta 30 kg"},
		training.MemoryInput{Type: "note", Title: "Rodilla", Body: "sin problema"},
	)
	if _, err := svc.JudgeMemory(ctx, saved[1].Candidates[0].RelationID, training.RelationSupersedes, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ForgetMemory(ctx, saved[3].Memory.ID); err != nil {
		t.Fatal(err)
	}
	for view, want := range map[string][]string{
		"":           {"Hombro recuperado", "Mancuernas"},
		"superseded": {"Hombro lesionado"},
		"forgotten":  {"Rodilla"},
	} {
		body := get(t, h, base+"/memory?view="+view).Body.String()
		for _, title := range []string{"Hombro lesionado", "Hombro recuperado", "Mancuernas", "Rodilla"} {
			if shown, wanted := strings.Contains(body, title), slices.Contains(want, title); shown != wanted {
				t.Errorf("view %q shows %q = %v, want %v", view, title, shown, wanted)
			}
		}
	}
	if w := get(t, h, base+"/memory?view=secret"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown view status = %d", w.Code)
	}
}

func TestMemoryPanelListsDueFactsAndConfirmsThem(t *testing.T) {
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := webClock()
	clock := func() time.Time { return now }
	svc := training.NewServices(store, clock).ForUser(1)
	h := webHandlerAt(t, svc, clock)
	saved := saveAll(t, svc, training.MemoryInput{Type: "limitation", Title: "Hombro", Body: "molesta"})
	if body := get(t, h, base+"/memory?view=due").Body.String(); strings.Contains(body, "Hombro") {
		t.Fatalf("a fact saved today is due:\n%s", body)
	}
	now = now.AddDate(0, 0, 14)
	body := get(t, h, base+"/memory?view=due").Body.String()
	if !strings.Contains(body, "Hombro") || !strings.Contains(body, "Sigue vigente") {
		t.Fatalf("a fact past its review date is not due:\n%s", body)
	}
	if w := post(t, h, base+"/memory/"+itoa(saved[0].Memory.ID)+"/confirm", nil, false); w.Code != http.StatusSeeOther {
		t.Fatalf("confirm status = %d", w.Code)
	}
	if body = get(t, h, base+"/memory?view=due").Body.String(); strings.Contains(body, "Hombro") {
		t.Fatalf("still due after confirming:\n%s", body)
	}
}

func TestMemoryPanelResolvesConflicts(t *testing.T) {
	ctx := context.Background()
	h, svc := memoryWeb(t)
	saved := saveAll(t, svc,
		training.MemoryInput{Type: "preference", Title: "Prefiere tardes", Body: "a las 19"},
		training.MemoryInput{Type: "preference", Title: "Prefiere mañanas", Body: "a las 7"},
	)
	relation := saved[1].Candidates[0].RelationID
	if _, err := svc.JudgeMemory(ctx, relation, training.RelationConflictsWith, "horarios opuestos"); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, base+"/memory?view=conflicts").Body.String()
	if !strings.Contains(body, "Prefiere tardes") || !strings.Contains(body, "Prefiere mañanas") || !strings.Contains(body, "horarios opuestos") {
		t.Fatalf("conflicts:\n%s", body)
	}
	if w := post(t, h, base+"/memory/conflicts/"+itoa(relation)+"/resolve", url.Values{"keep": {"sideways"}}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("bad keep status = %d", w.Code)
	}
	if w := post(t, h, base+"/memory/conflicts/"+itoa(relation)+"/resolve", url.Values{"keep": {"new"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("resolve status = %d", w.Code)
	}
	active, err := svc.SearchMemory(ctx, training.MemoryQuery{})
	if err != nil || len(active) != 1 || active[0].Title != "Prefiere mañanas" {
		t.Fatalf("active = %v, %v", active, err)
	}
	if body = get(t, h, base+"/memory?view=conflicts").Body.String(); strings.Contains(body, "horarios opuestos") {
		t.Fatalf("resolved conflict still listed:\n%s", body)
	}
}

func TestMemoryPanelShowsHistory(t *testing.T) {
	h, svc := memoryWeb(t)
	saveAll(t, svc,
		training.MemoryInput{TopicKey: "k", Title: "Hombro", Body: "texto antiguo"},
		training.MemoryInput{TopicKey: "k", Title: "Hombro", Body: "texto nuevo"},
	)
	body := get(t, h, base+"/memory").Body.String()
	if !strings.Contains(body, "texto antiguo") || !strings.Contains(body, "texto nuevo") {
		t.Fatalf("history missing:\n%s", body)
	}
}

func TestDeletingEverythingNeedsTheConfirmationWord(t *testing.T) {
	ctx := context.Background()
	h, svc := memoryWeb(t)
	saveAll(t, svc, training.MemoryInput{Title: "Hombro", Body: "uno"}, training.MemoryInput{Title: "Rodilla", Body: "dos"})
	if w := post(t, h, base+"/memory/delete-all", url.Values{"confirm": {"si"}}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("without the word: %d", w.Code)
	}
	if left, _ := svc.SearchMemory(ctx, training.MemoryQuery{}); len(left) != 2 {
		t.Fatalf("deleted without confirming: %v", left)
	}
	if w := post(t, h, base+"/memory/delete-all", url.Values{"confirm": {" borrar "}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("with the word: %d", w.Code)
	}
	for _, view := range training.MemoryViews {
		if left, _ := svc.SearchMemory(ctx, training.MemoryQuery{View: view}); len(left) != 0 {
			t.Fatalf("%s left: %v", view, left)
		}
	}
}

// Accepting a merge whose facts changed since its preview is refused with a
// message, not an error page, and merges nothing.
func TestAcceptingAStaleMergeExplainsWhy(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := training.NewServices(store, webClock).ForUser(1)
	if err = svc.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	serviceFor := func(context.Context) *training.Service { return svc }
	tools := mcpsrv.New(serviceFor)
	server, err := websrv.NewWithOptions(serviceFor, nil, webClock, base, websrv.Options{Apply: tools.Apply})
	if err != nil {
		t.Fatal(err)
	}
	saved := saveAll(t, svc, training.MemoryInput{TopicKey: "a", Title: "Hombro", Body: "uno"}, training.MemoryInput{TopicKey: "b", Title: "Rodilla", Body: "dos"})
	in := training.ConsolidationInput{MemoryIDs: []int64{saved[0].Memory.ID, saved[1].Memory.ID}, Title: "Todo", Body: "uno y dos"}
	plan, err := svc.PreviewConsolidation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"memory_ids":[` + itoa(saved[0].Memory.ID) + `,` + itoa(saved[1].Memory.ID) + `],"title":"Todo","body":"uno y dos","fingerprint":"` + plan.Fingerprint + `"}`)
	p, err := svc.Propose(ctx, training.ProposalInput{Tool: "memory_consolidate", ActionType: "memory.consolidate", Payload: payload}, nil)
	if err != nil {
		t.Fatal(err)
	}
	saveAll(t, svc, training.MemoryInput{TopicKey: "a", Title: "Hombro", Body: "cambiado"})

	w := post(t, server.Handler(), base+"/review/"+itoa(p.ID)+"/accept", url.Values{"remember": {"once"}}, true)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "cambiaron") {
		t.Fatalf("status %d:\n%s", w.Code, w.Body)
	}
	if active, _ := svc.SearchMemory(ctx, training.MemoryQuery{}); len(active) != 2 {
		t.Fatalf("a stale merge merged: %v", active)
	}
}

// With nothing remembered, Memoria says what will appear there; a filter that
// matches nothing still says so.
func TestMemoryEmptyStateSaysWhatWillAppear(t *testing.T) {
	h, _ := memoryWeb(t)
	empty := "El coach aún no recuerda nada. Lo que le cuentes y confirmes aparecerá aquí."
	if page := get(t, h, base+"/memory").Body.String(); !strings.Contains(page, empty) {
		t.Fatalf("empty Memoria lacks %q:\n%s", empty, page)
	}
	if page := get(t, h, base+"/memory?q=sentadilla").Body.String(); !strings.Contains(page, "No hay nada con ese filtro.") || strings.Contains(page, empty) {
		t.Fatalf("filtered empty Memoria:\n%s", page)
	}
	if page := get(t, h, base+"/memory?view=forgotten").Body.String(); !strings.Contains(page, "No hay nada aquí todavía.") || strings.Contains(page, empty) {
		t.Fatalf("empty forgotten view:\n%s", page)
	}
}
