package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

func bodyweights(t *testing.T, svc *training.Service) []training.Bodyweight {
	t.Helper()
	h, err := svc.BodyweightHistory(context.Background(), "2026-01-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	return h.Entries
}

// cuerpo is Progreso's last pane, Cuerpo, to the end of the fragment.
func cuerpo(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `id="pg-body"`)
	if i < 0 {
		t.Fatalf("no Cuerpo pane:\n%s", body)
	}
	return body[i:]
}

// Cuerpo asks for today's weight in one form, with nothing else to fill in.
func TestCuerpoAsksForTodaysWeightWithoutNagging(t *testing.T) {
	h, svc := newServer(t)
	seedProgress(t, svc)
	body := getFragment(t, h, base+"/progress").Body.String()
	pane := cuerpo(t, body)
	for _, want := range []string{
		"¿Cuánto pesas hoy?",
		`action="` + base + `/bodyweight"`, `name="weight"`, `name="weight_unit"`, `name="date" value="2026-08-07"`, `name="note"`,
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("Cuerpo lacks %q:\n%s", want, pane)
		}
	}
	if strings.Contains(body, "Aún no registras tu peso ni medidas") {
		t.Error("Cuerpo still says weight is not recorded")
	}
	// Historial shows no bodyweight card and no reminder until there is an entry.
	if history := section(t, body, `id="pg-history"`, `id="pg-exercises"`); strings.Contains(history, "Peso corporal") {
		t.Errorf("Historial shows bodyweight with no entry:\n%s", history)
	}
}

// Before the first session Progreso has no tabs, yet bodyweight can be logged.
func TestBodyweightCanBeLoggedBeforeTheFirstSession(t *testing.T) {
	h, svc := newServer(t)
	body := getFragment(t, h, base+"/progress").Body.String()
	if !strings.Contains(body, "Tu primer entreno aparecerá aquí") || !strings.Contains(cuerpo(t, body), "¿Cuánto pesas hoy?") {
		t.Fatalf("empty Progreso:\n%s", body)
	}
	w := post(t, h, base+"/bodyweight", url.Values{"weight": {"80"}, "weight_unit": {"kg"}}, true)
	if got := bodyweights(t, svc); w.Code != http.StatusOK || len(got) != 1 || got[0].Date != "2026-08-07" {
		t.Fatalf("%d, stored %+v", w.Code, got)
	}
	if !strings.Contains(cuerpo(t, w.Body.String()), "80 kg") {
		t.Fatalf("the entry is not listed:\n%s", w.Body)
	}
}

func TestLoggingBodyweightInLbStoresKGAndShowsItBack(t *testing.T) {
	h, svc := newServer(t)
	seedProgress(t, svc)
	if err := svc.SetWeightUnit(context.Background(), "lb"); err != nil {
		t.Fatal(err)
	}
	w := post(t, h, base+"/bodyweight", url.Values{"weight": {"176,4"}, "weight_unit": {"lb"}, "date": {"2026-08-07"}, "note": {"en ayunas"}}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	got := bodyweights(t, svc)
	wantKG := training.ToKG(176.4, training.UnitLB)
	if len(got) != 1 || got[0].Date != "2026-08-07" || got[0].WeightKG != wantKG || got[0].Note != "en ayunas" {
		t.Fatalf("stored %+v, want 176.4 lb as %v kg today", got, wantKG)
	}
	// The answer is Progreso again, with today's weight in lb.
	pane := cuerpo(t, w.Body.String())
	if !strings.Contains(pane, `Hoy: <span class="num">176.4 lb</span>`) || !strings.Contains(pane, "en ayunas") || strings.Contains(pane, "¿Cuánto pesas hoy?") {
		t.Fatalf("Cuerpo after logging:\n%s", pane)
	}

	// The same day again replaces the entry.
	if w := post(t, h, base+"/bodyweight", url.Values{"weight": {"175"}, "weight_unit": {"lb"}}, false); w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/?modo=progreso" {
		t.Fatalf("without htmx: %d %q", w.Code, w.Header().Get("Location"))
	}
	if got := bodyweights(t, svc); len(got) != 1 || training.FromKG(got[0].WeightKG, training.UnitLB) != 175 || got[0].Note != "" {
		t.Fatalf("after logging the day again: %+v", got)
	}
}

func TestLoggingAnImplausibleBodyweightIsRefusedAndExplained(t *testing.T) {
	h, svc := newServer(t)
	seedProgress(t, svc)
	for name, form := range map[string]url.Values{
		"too heavy":  {"weight": {"800"}, "weight_unit": {"kg"}},
		"not number": {"weight": {"ochenta"}, "weight_unit": {"kg"}},
		"bad date":   {"weight": {"80"}, "weight_unit": {"kg"}, "date": {"07/08/2026"}},
		"long note":  {"weight": {"80"}, "weight_unit": {"kg"}, "note": {strings.Repeat("x", 501)}},
	} {
		w := post(t, h, base+"/bodyweight", form, true)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d, want Progreso back with the reason:\n%s", name, w.Code, w.Body)
		}
		if w := post(t, h, base+"/bodyweight", form, false); w.Code != http.StatusBadRequest {
			t.Errorf("%s without htmx: %d, want 400", name, w.Code)
		}
	}
	if body := post(t, h, base+"/bodyweight", url.Values{"weight": {"800"}, "weight_unit": {"kg"}}, true).Body.String(); !strings.Contains(body, "entre 20 y 400 kg") {
		t.Errorf("the range is not explained:\n%s", body)
	}
	if got := bodyweights(t, svc); len(got) != 0 {
		t.Fatalf("a refused weight was stored: %+v", got)
	}
}

// Historial shows the bodyweight trend beside each week's volume once there are
// entries, and Cuerpo lists them with a way to delete a wrong one.
func TestHistoryShowsBodyweightBesideVolumeAndCuerpoListsEntries(t *testing.T) {
	ctx := context.Background()
	h, svc := newServer(t)
	seedProgress(t, svc)
	for date, kg := range map[string]float64{"2026-07-29": 82.4, "2026-08-03": 81.6, "2026-08-05": 81.2} {
		if _, err := svc.LogBodyweight(ctx, training.BodyweightInput{Date: date, Weight: kg, Unit: "kg"}); err != nil {
			t.Fatal(err)
		}
	}
	body := getFragment(t, h, base+"/progress").Body.String()
	history := section(t, body, `id="pg-history"`, `id="pg-exercises"`)
	card := section(t, history, `aria-label="Peso corporal y volumen"`, "</section>")
	// This week (from Monday 3 Aug): 1.400 kg moved and a mean of 81.4 kg.
	for _, want := range []string{"<polyline", "3 ago", "1.400 kg", "81.4 kg", "27 jul", "82.4 kg"} {
		if !strings.Contains(card, want) {
			t.Errorf("the bodyweight card lacks %q:\n%s", want, card)
		}
	}
	pane := cuerpo(t, body)
	for _, want := range []string{
		"¿Cuánto pesas hoy?", "81.2 kg", "mié 5 ago", "82.4 kg", "-1.2 kg",
		`action="` + base + `/bodyweight/delete"`, `name="date" value="2026-08-05"`,
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("Cuerpo lacks %q:\n%s", want, pane)
		}
	}
	for _, judgement := range []string{"objetivo", "deberías", "meta", "ideal"} {
		if strings.Contains(strings.ToLower(card+pane), judgement) {
			t.Errorf("bodyweight shows a judgement (%q)", judgement)
		}
	}
}

func TestDeletingABodyweightEntryFromCuerpo(t *testing.T) {
	ctx := context.Background()
	h, svc := newServer(t)
	seedProgress(t, svc)
	for _, date := range []string{"2026-08-04", "2026-08-05"} {
		if _, err := svc.LogBodyweight(ctx, training.BodyweightInput{Date: date, Weight: 80, Unit: "kg"}); err != nil {
			t.Fatal(err)
		}
	}
	if w := post(t, h, base+"/bodyweight/delete", url.Values{"date": {"2026-08-05"}}, true); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `value="2026-08-05"`) {
		t.Fatalf("delete: %d\n%s", w.Code, w.Body)
	}
	if got := bodyweights(t, svc); len(got) != 1 || got[0].Date != "2026-08-04" {
		t.Fatalf("left %+v", got)
	}
	if w := post(t, h, base+"/bodyweight/delete", url.Values{"date": {"2026-08-05"}}, true); w.Code != http.StatusNotFound {
		t.Fatalf("deleting it again: %d, want 404", w.Code)
	}
}

func TestSessionPageEditsTheSessionsNotes(t *testing.T) {
	ctx := context.Background()
	h, svc := newServer(t)
	session, err := svc.StartSession(ctx, "2026-08-05")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(session.ID, 10)
	page := get(t, h, base+"/s/"+id).Body.String()
	if !strings.Contains(page, `action="`+base+`/s/`+id+`/notes"`) || !strings.Contains(page, `name="notes"`) {
		t.Fatalf("the session page has no notes field:\n%s", page)
	}
	w := post(t, h, base+"/s/"+id+"/notes", url.Values{"notes": {"Dormí 5 horas"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/s/"+id {
		t.Fatalf("saving notes: %d %q", w.Code, w.Header().Get("Location"))
	}
	if got, _ := svc.GetSession(ctx, session.ID); got.Notes != "Dormí 5 horas" {
		t.Fatalf("notes = %q", got.Notes)
	}
	if page := get(t, h, base+"/s/"+id).Body.String(); !strings.Contains(page, ">Dormí 5 horas</textarea>") {
		t.Fatalf("the saved notes are not shown:\n%s", page)
	}
	if w := post(t, h, base+"/s/"+id+"/notes", url.Values{"notes": {strings.Repeat("x", 2001)}}, false); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "2000") {
		t.Fatalf("long notes: %d", w.Code)
	}
	if w := post(t, h, base+"/s/999999/notes", url.Values{"notes": {"x"}}, false); w.Code != http.StatusNotFound {
		t.Fatalf("missing session: %d", w.Code)
	}
	// Logging a set needs nothing new: the workout screen asks for neither.
	if home := get(t, h, base+"/?modo=entrenar").Body.String(); strings.Contains(home, "/bodyweight") || strings.Contains(home, "Notas de la sesión") {
		t.Fatal("the workout screen grew a bodyweight or session-notes field")
	}
}
