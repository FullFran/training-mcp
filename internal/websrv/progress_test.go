package websrv_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// seedProgress logs two sessions in the clock's week (Monday 3 and Wednesday
// 5 August 2026; the clock reads Friday 7) and one the month before, so the
// week dots, the month headers and the e1RM trend all have something to show.
func seedProgress(t *testing.T, svc *training.Service) (planned training.Session) {
	t.Helper()
	ctx := context.Background()
	if err := svc.SetExerciseGroup(ctx, "press banca", "pecho"); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.CreatePlan(ctx, training.Plan{Name: "Día A", Items: []training.PlanItem{{Exercise: "press banca", TargetSets: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	add := func(session training.Session, weight float64, reps int, category string) {
		t.Helper()
		if _, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "press banca", WeightKG: weight, Reps: reps, RPE: 8, Category: category}); err != nil {
			t.Fatal(err)
		}
	}
	july, err := svc.StartSession(ctx, "2026-07-20")
	if err != nil {
		t.Fatal(err)
	}
	add(july, 75, 6, "")
	if planned, err = svc.StartPlannedSession(ctx, "2026-08-03", plan.ID); err != nil {
		t.Fatal(err)
	}
	add(planned, 40, 10, training.SetWarmup)
	add(planned, 80, 6, "")
	add(planned, 85, 5, "")
	wednesday, err := svc.StartSession(ctx, "2026-08-05")
	if err != nil {
		t.Fatal(err)
	}
	add(wednesday, 82.5, 6, "")
	return planned
}

func getFragment(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The old history page is now Progreso's tab: the URL still lands there.
func TestHistoryRedirectsToProgreso(t *testing.T) {
	h, _ := newServer(t)
	w := get(t, h, base+"/history")
	if w.Code != http.StatusFound || w.Header().Get("Location") != base+"/?modo=progreso" {
		t.Fatalf("GET /history = %d %q, want 302 to Progreso", w.Code, w.Header().Get("Location"))
	}
}

func TestProgressHistoryShowsTheWeekAndTheSessions(t *testing.T) {
	h, svc := newServer(t)
	planned := seedProgress(t, svc)
	w := getFragment(t, h, base+"/progress")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if got := strings.Count(body, `class="day on"`); got != 2 {
		t.Errorf("trained days this week = %d, want 2 (Monday and Wednesday)\n%s", got, body)
	}
	if got := strings.Count(body, `class="day`); got != 7 {
		t.Errorf("week days = %d, want 7", got)
	}
	// 80x6 + 85x5 on Monday and 82.5x6 on Wednesday is 1400 kg; the warm-up does not count.
	for _, want := range []string{
		"2 sesiones", "1.400 kg",
		"Agosto", "Julio",
		"Día A", "Entreno libre",
		`href="` + base + `/s/` + strconv.FormatInt(planned.ID, 10) + `" data-panel`,
		"lun 3 ago",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Progreso lacks %q", want)
		}
	}
}

func TestProgressExercisesShowBestE1RMAndBestSet(t *testing.T) {
	h, svc := newServer(t)
	seedProgress(t, svc)
	body := getFragment(t, h, base+"/progress").Body.String()
	// Epley: 85x5 is 99.2 kg, above 82.5x6 (99) and 80x6 (96).
	for _, want := range []string{
		`href="` + base + `/progress/exercise?name=press%20banca" data-panel`,
		"press banca", "pecho", "99.2", "85 × 5", "<polyline",
		// The volume by muscle group the history page used to show.
		`aria-label="Volumen por grupo muscular"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Ejercicios lacks %q", want)
		}
	}
	if !strings.Contains(body, "Aún no registras tu peso ni medidas") {
		t.Error("Cuerpo has no empty state")
	}
}

func TestProgressEmptyInvitesToTrain(t *testing.T) {
	h, _ := newServer(t)
	body := getFragment(t, h, base+"/progress").Body.String()
	if !strings.Contains(body, "Tu primer entreno aparecerá aquí") || !strings.Contains(body, `data-mode-go="home"`) {
		t.Fatalf("empty Progreso:\n%s", body)
	}
}

func TestProgressOpensServerRenderedOnItsTab(t *testing.T) {
	h, svc := newServer(t)
	seedProgress(t, svc)
	if body := get(t, h, base+"/?modo=progreso").Body.String(); !strings.Contains(body, `id="pg-history"`) {
		t.Fatal("Progreso is not rendered when the page opens on it")
	}
	if body := get(t, h, base+"/").Body.String(); strings.Contains(body, `id="pg-history"`) || !strings.Contains(body, `hx-get="`+base+`/progress"`) {
		t.Fatal("Progreso should load on demand when the page opens elsewhere")
	}
}

func TestExerciseDetailShowsTrendBestSetsAndHistory(t *testing.T) {
	h, svc := newServer(t)
	seedProgress(t, svc)
	w := get(t, h, base+"/progress/exercise?name=press+banca")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"press banca", "99.2", "<polyline", "Mejores series", "85 × 5", "82.5 × 6", "75 × 6", "lun 3 ago", `href="` + base + `/?modo=progreso"`} {
		if !strings.Contains(body, want) {
			t.Errorf("exercise detail lacks %q", want)
		}
	}
	if w := get(t, h, base+"/progress/exercise?name=nada"); w.Code != http.StatusNotFound {
		t.Errorf("unknown exercise: %d, want 404", w.Code)
	}
}

func TestExercisesListFeedsThePicker(t *testing.T) {
	h, svc := newServer(t)
	seedProgress(t, svc)
	w := get(t, h, base+"/exercises")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var got struct {
		Groups    []string `json:"groups"`
		Recent    []string `json:"recent"`
		Exercises []struct {
			Name  string `json:"name"`
			Group string `json:"group"`
			Last  string `json:"last"`
		} `json:"exercises"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Exercises) != 1 || got.Exercises[0].Name != "press banca" || got.Exercises[0].Group != "pecho" || got.Exercises[0].Last != "82.5×6" {
		t.Fatalf("exercises = %+v", got.Exercises)
	}
	if len(got.Groups) != 1 || got.Groups[0] != "pecho" || len(got.Recent) != 1 || got.Recent[0] != "press banca" {
		t.Fatalf("groups %v recent %v", got.Groups, got.Recent)
	}
}

func TestYouGathersSettingsCoachAndData(t *testing.T) {
	h, svc := newServer(t)
	if err := svc.SetRestTarget(context.Background(), 150); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, base+"/?modo=tu").Body.String()
	for _, want := range []string{
		"Perfil", "Memoria del coach", `href="` + base + `/memory" data-panel`,
		"Descanso por defecto", `data-rest-value>2:30<`,
		`href="` + base + `/import" data-panel`, "Importar (Hevy, Strong)",
		`href="` + base + `/export.db" download`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Tú lacks %q", want)
		}
	}
}
