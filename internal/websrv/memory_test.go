package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

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
func TestMemoryPanelEditsAndDeletesDirectly(t *testing.T) {
	ctx := context.Background()
	h, svc := memoryWeb(t)
	m, err := svc.SaveMemory(ctx, training.MemoryInput{Title: "Agarre", Body: "Neutro"})
	if err != nil {
		t.Fatal(err)
	}
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
