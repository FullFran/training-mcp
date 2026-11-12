package websrv_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// acceptedPlan is a create_plan proposal the person accepted.
func (f reviewFixture) acceptedPlan(t *testing.T, name string) training.Proposal {
	t.Helper()
	p := f.propose(t, name)
	accepted, err := f.service.AcceptProposal(context.Background(), p.ID, training.AnswerAllowOnce, f.mcp.Apply)
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

// The history offers undo for an accepted proposal; undoing it from the page
// removes what it created and lists the undo as its own entry.
func TestUndoFromTheReviewHistory(t *testing.T) {
	f := newReviewFixture(t)
	p := f.acceptedPlan(t, "pierna")
	body := get(t, f.handler, "/review").Body.String()
	if !strings.Contains(body, `action="/review/`+itoa(p.ID)+`/undo"`) {
		t.Fatalf("no undo offered:\n%s", body)
	}
	w := post(t, f.handler, "/review/"+itoa(p.ID)+"/undo", url.Values{}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/review" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if plans := f.plans(t); len(plans) != 0 {
		t.Fatalf("plans after undo = %+v", plans)
	}
	body = get(t, f.handler, "/review").Body.String()
	if strings.Contains(body, `/review/`+itoa(p.ID)+`/undo"`) || !strings.Contains(body, "Aceptada, deshecha") || !strings.Contains(body, "Deshacer") {
		t.Fatalf("history after undo:\n%s", body)
	}
	again := post(t, f.handler, "/review/"+itoa(p.ID)+"/undo", url.Values{}, false)
	if again.Code != http.StatusConflict || !strings.Contains(again.Body.String(), "ya estaba deshecha") {
		t.Fatalf("second undo %d: %s", again.Code, again.Body)
	}
}

// A refused undo says why and changes nothing.
func TestUndoRefusalIsExplained(t *testing.T) {
	f := newReviewFixture(t)
	p := f.acceptedPlan(t, "pierna")
	plan := f.plans(t)[0]
	if _, err := f.service.StartPlannedSession(context.Background(), "2026-03-01", plan.ID); err != nil {
		t.Fatal(err)
	}
	w := post(t, f.handler, "/review/"+itoa(p.ID)+"/undo", url.Values{}, false)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "algo que hiciste después depende de la rutina") {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if plans := f.plans(t); len(plans) != 1 {
		t.Fatalf("a refused undo changed plans: %+v", plans)
	}
}

// Another account cannot undo a proposal: to it the proposal does not exist.
func TestUndoRouteIsIsolatedBetweenUsers(t *testing.T) {
	f := newReviewFixture(t)
	p := f.acceptedPlan(t, "pierna")
	otherID, err := f.store.ProvisionUser(context.Background(), "user_other")
	if err != nil {
		t.Fatal(err)
	}
	other := f.services.ForUser(otherID)
	serviceFor := func(context.Context) *training.Service { return other }
	server, err := websrv.NewWithOptions(serviceFor, f.services.Snapshot, webClock, "", websrv.Options{Apply: f.mcp.Apply, CSRFToken: func(*http.Request) string { return "tok" }})
	if err != nil {
		t.Fatal(err)
	}
	w := post(t, server.Handler(), "/review/"+itoa(p.ID)+"/undo", url.Values{}, false)
	if !strings.Contains(w.Body.String(), "ya no existe") {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if plans := f.plans(t); len(plans) != 1 {
		t.Fatal("another account undid the proposal")
	}
	list := get(t, server.Handler(), "/review/undoable").Body.String()
	if strings.Contains(list, itoa(p.ID)) {
		t.Fatalf("another account lists the proposal: %s", list)
	}
}

// ageProposal moves when a proposal was decided.
func ageProposal(t *testing.T, f reviewFixture, id int64, at time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE proposals SET decided_at=? WHERE id=?`, at.UTC().Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
}

type undoableList struct {
	Proposals []struct {
		ID      int64  `json:"id"`
		Label   string `json:"label"`
		Summary string `json:"summary"`
		When    string `json:"when"`
		UndoURL string `json:"undo_url"`
	} `json:"proposals"`
}

// The chat reads the proposals it can offer to undo: accepted in the last 12
// hours and not undone, newest first, at most five.
func TestUndoableListForTheChat(t *testing.T) {
	f := newReviewFixture(t)
	var ids []int64
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		ids = append(ids, f.acceptedPlan(t, name).ID)
	}
	f.propose(t, "pendiente")
	if _, err := f.service.UndoProposal(context.Background(), ids[5]); err != nil {
		t.Fatal(err)
	}
	old := f.acceptedPlan(t, "vieja")
	ageProposal(t, f, old.ID, webClock().Add(-training.SessionWindow-time.Minute))

	w := get(t, f.handler, "/review/undoable")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d type %q", w.Code, w.Header().Get("Content-Type"))
	}
	var got undoableList
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []int64{ids[4], ids[3], ids[2], ids[1], ids[0]}
	if len(got.Proposals) != len(want) {
		t.Fatalf("listed %+v, want %v", got.Proposals, want)
	}
	for i, p := range got.Proposals {
		if p.ID != want[i] || p.Label != "Crear rutina" || p.Summary == "" || p.When == "" || p.UndoURL != "/review/"+itoa(p.ID)+"/undo" {
			t.Fatalf("entry %d = %+v, want id %d", i, p, want[i])
		}
	}
}
