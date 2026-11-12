package websrv_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

// A session opened from Progreso can be deleted there, with every set in it,
// through the same service call delete_session makes.
func TestDeletingASessionFromItsPageRemovesItAndItsSets(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	session, err := svc.StartSession(ctx, "2026-08-05")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "press banca", WeightKG: 60, Reps: 8, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(session.ID, 10)

	page := get(t, h, base+"/s/"+id)
	if body := page.Body.String(); !strings.Contains(body, `action="`+base+`/s/`+id+`/delete"`) || !strings.Contains(body, "Borrar sesión") {
		t.Fatalf("session page offers no delete:\n%s", body)
	}

	w := post(t, h, base+"/s/"+id+"/delete", url.Values{}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/?modo=progreso" {
		t.Fatalf("status %d, location %q", w.Code, w.Header().Get("Location"))
	}
	if _, err := svc.GetSession(ctx, session.ID); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("session still there: %v", err)
	}
	if hist, err := svc.ExerciseHistory(ctx, "press banca", 10); err != nil || len(hist.Sets) != 0 {
		t.Fatalf("sets left = %+v, %v", hist, err)
	}

	// Deleting it again is a missing session, not a server error.
	if again := post(t, h, base+"/s/"+id+"/delete", url.Values{}, false); again.Code != http.StatusNotFound {
		t.Fatalf("second delete status %d", again.Code)
	}
}

// Behind the web login the delete form is a plain form, so it must carry the
// session's CSRF token itself or the middleware refuses it.
func TestTheDeleteSessionFormCarriesTheCSRFToken(t *testing.T) {
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	services := training.NewServices(store, webClock)
	svc := services.ForUser(1)
	server, err := websrv.NewWithOptions(func(context.Context) *training.Service { return svc }, services.Snapshot, webClock, "",
		websrv.Options{CSRFToken: func(*http.Request) string { return "tok-delete" }})
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.StartSession(context.Background(), "2026-08-05")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(session.ID, 10)
	body := get(t, server.Handler(), "/s/"+id).Body.String()
	start := strings.Index(body, `action="/s/`+id+`/delete"`)
	if start < 0 {
		t.Fatalf("no delete form:\n%s", body)
	}
	form := body[start:]
	form = form[:strings.Index(form, "</form>")]
	if !strings.Contains(form, `<input type="hidden" name="csrf_token" value="tok-delete">`) {
		t.Fatalf("delete form lacks the CSRF field:\n%s", form)
	}
}
