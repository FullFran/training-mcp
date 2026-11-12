package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// Ejercicios, under Tú, lists every exercise with its muscle group and setup
// note, and changes them through the calls set_exercise_group and
// set_exercise_note make.
func TestCatalogueListsAndEditsGroupsAndNotes(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	session, err := svc.StartSession(ctx, "2026-08-05")
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range []string{"press banca", "remo"} {
		if _, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: ex, WeightKG: 50, Reps: 8, RPE: 8}); err != nil {
			t.Fatal(err)
		}
	}
	if err = svc.SetExerciseGroup(ctx, "press banca", "pecho"); err != nil {
		t.Fatal(err)
	}
	if err = svc.SetExerciseNote(ctx, "press banca", "banco en el 3"); err != nil {
		t.Fatal(err)
	}

	if home := get(t, h, base+"/").Body.String(); !strings.Contains(home, `href="`+base+`/catalogue"`) {
		t.Fatalf("Tú does not link the catalogue:\n%s", home)
	}
	page := get(t, h, base+"/catalogue")
	body := page.Body.String()
	for _, want := range []string{"press banca", "pecho", "banco en el 3", "remo", "Sin grupo", `<option value="cuadriceps"`, `action="` + base + `/catalogue"`} {
		if page.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("catalogue lacks %q (status %d):\n%s", want, page.Code, body)
		}
	}

	w := post(t, h, base+"/catalogue", url.Values{"exercise": {"remo"}, "muscle_group": {"espalda"}, "note": {"agarre prono"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/catalogue" {
		t.Fatalf("status %d, location %q", w.Code, w.Header().Get("Location"))
	}
	// No group chosen keeps the one stored; an empty note removes it.
	if w = post(t, h, base+"/catalogue", url.Values{"exercise": {"press banca"}, "muscle_group": {""}, "note": {""}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("clearing the note: status %d", w.Code)
	}
	// A group outside the taxonomy is refused and stores nothing.
	bad := post(t, h, base+"/catalogue", url.Values{"exercise": {"remo"}, "muscle_group": {"pierna"}, "note": {"otra"}}, false)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "Grupo muscular inválido.") {
		t.Fatalf("bad group: %d\n%s", bad.Code, bad.Body)
	}

	groups, err := svc.ExerciseGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, g := range groups {
		got[g.Exercise] = g.MuscleGroup
	}
	if got["remo"] != "espalda" || got["press banca"] != "pecho" {
		t.Fatalf("groups = %v", got)
	}
	notes, err := svc.ExerciseNotes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Exercise != "remo" || notes[0].Note != "agarre prono" {
		t.Fatalf("notes = %+v", notes)
	}
}
