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

func storedWeight(t *testing.T, svc *training.Service, exercise string) []float64 {
	t.Helper()
	sessions, err := svc.ListSessions(context.Background(), training.ListFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var out []float64
	for _, summary := range sessions {
		session, err := svc.GetSession(context.Background(), summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, set := range session.Sets {
			if set.Exercise == exercise {
				out = append(out, set.WeightKG)
			}
		}
	}
	return out
}

// Tú › Entrenamiento holds the default unit: kg until chosen, then lb, for
// that person only.
func TestWeightUnitIsAPerPersonSetting(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	you := section(t, get(t, world.as(1), base+"/").Body.String(), `id="you"`, "</section>")
	for _, want := range []string{`action="` + base + `/units"`, "Unidades", `name="unit" value="kg" aria-pressed="true"`, `name="unit" value="lb" aria-pressed="false"`} {
		if !strings.Contains(you, want) {
			t.Fatalf("Tú lacks %s:\n%s", want, you)
		}
	}
	w := post(t, world.as(1), base+"/units", url.Values{"unit": {"lb"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/?modo=tu" {
		t.Fatalf("POST /units: %d %q", w.Code, w.Header().Get("Location"))
	}
	if you = section(t, get(t, world.as(1), base+"/").Body.String(), `id="you"`, "</section>"); !strings.Contains(you, `name="unit" value="lb" aria-pressed="true"`) {
		t.Fatalf("lb not chosen:\n%s", you)
	}
	// The other person still reads kg.
	if other := section(t, get(t, world.as(2), base+"/").Body.String(), `id="you"`, "</section>"); !strings.Contains(other, `name="unit" value="kg" aria-pressed="true"`) {
		t.Fatalf("the unit leaked to another person:\n%s", other)
	}
	if bad := post(t, world.as(1), base+"/units", url.Values{"unit": {"stone"}}, false); bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown unit: %d", bad.Code)
	}
}

// Every weight input sends its unit; the server stores kg. No unit is kg, as
// every existing caller means, and an unknown one stores nothing.
func TestEveryWeightInputConvertsItsUnit(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	if err := svc.SetWeightUnit(ctx, "lb"); err != nil {
		t.Fatal(err)
	}
	form := func(weight, unit string) url.Values {
		f := setForm("press banca", weight, "8", "8")
		if unit != "" {
			f.Set("weight_unit", unit)
		}
		return f
	}
	post(t, h, base+"/sets", form("135", "lb"), true)
	post(t, h, base+"/sets", form("60", "kg"), true) // kg while the default is lb
	post(t, h, base+"/sets", form("70", ""), true)   // an older client
	bad := post(t, h, base+"/sets", form("50", "stone"), true)
	if !strings.Contains(bad.Body.String(), "Revisa peso, reps y RPE.") {
		t.Fatalf("unknown unit:\n%s", bad.Body)
	}
	if got := storedWeight(t, svc, "press banca"); len(got) != 3 || got[0] != 61.25 || got[1] != 60 || got[2] != 70 {
		t.Fatalf("stored %v", got)
	}

	// Each form and the edit form carry the unit, starting on the default.
	page := get(t, h, base+"/?modo=entrenar").Body.String()
	for _, want := range []string{
		`<input type="hidden" name="weight_unit" value="lb">`,
		`data-unit-toggle`,
		`<span class="label">Peso (<span data-unit-label>lb</span>)</span>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("workout lacks %s", want)
		}
	}
	js := get(t, h, base+"/static/app.js").Body.String()
	if !strings.Contains(js, `name="weight_unit"`) || !strings.Contains(js, "data-unit-toggle") {
		t.Fatal("the edit form carries no unit toggle")
	}
}

// A load shown in lb and saved again keeps its stored kg: 60 kg is prefilled
// as 132.3 lb and logging it stores 60, not 60.01.
func TestALoadShownInLbIsSavedWithoutDrift(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	post(t, h, base+"/sets", setForm("remo", "60", "8", "8"), true)
	if err := svc.SetWeightUnit(ctx, "lb"); err != nil {
		t.Fatal(err)
	}
	page := get(t, h, base+"/?modo=entrenar").Body.String()
	if !strings.Contains(page, `name="weight_kg" type="number" inputmode="decimal" step="0.1" min="0.1" required value="132.3"`) {
		t.Fatalf("the next row is not prefilled in lb:\n%s", section(t, page, `id="panel"`, "</section>"))
	}
	f := setForm("remo", "132.3", "8", "8")
	f.Set("weight_unit", "lb")
	post(t, h, base+"/sets", f, true)
	if got := storedWeight(t, svc, "remo"); len(got) != 2 || got[1] != 60 {
		t.Fatalf("stored %v", got)
	}
}

// Editing works as before, now with a unit: kg changes only that set's weight,
// lb stores the converted kg, and another person's set is refused.
func TestEditingASetWithAUnit(t *testing.T) {
	world := newWebWorld(t)
	before := dumpDatabase(t, world.db)

	w := post(t, world.owner, base+"/sets/"+world.set+"/update", url.Values{"weight_kg": {"82.5"}, "weight_unit": {"kg"}}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("kg edit: %d %s", w.Code, w.Body)
	}
	after := dumpDatabase(t, world.db)
	want := strings.Replace(before, "80", "82.5", 1)
	if diffLines(before, after) != 1 || !strings.Contains(after, "82.5") || after == before {
		t.Fatalf("a kg edit changed more than the set's weight:\nwant one line changed\n%s", lineDiff(want, after))
	}

	post(t, world.owner, base+"/sets/"+world.set+"/update", url.Values{"weight_kg": {"135"}, "weight_unit": {"lb"}}, true)
	var kg float64
	if err := world.db.QueryRow(`SELECT weight_kg FROM sets WHERE id=?`, world.set).Scan(&kg); err != nil || kg != 61.25 {
		t.Fatalf("lb edit stored %v, %v", kg, err)
	}

	// Leaving the weight out leaves it alone, as before.
	post(t, world.owner, base+"/sets/"+world.set+"/update", url.Values{"reps": {"9"}}, true)
	if err := world.db.QueryRow(`SELECT weight_kg FROM sets WHERE id=?`, world.set).Scan(&kg); err != nil || kg != 61.25 {
		t.Fatalf("a reps edit moved the weight to %v, %v", kg, err)
	}

	frozen := dumpDatabase(t, world.db)
	foreign := post(t, world.second, base+"/sets/"+world.set+"/update", url.Values{"weight_kg": {"135"}, "weight_unit": {"lb"}}, true)
	missing := post(t, world.second, base+"/sets/"+missingWebID+"/update", url.Values{"weight_kg": {"135"}, "weight_unit": {"lb"}}, true)
	if foreign.Body.String() != missing.Body.String() || dumpDatabase(t, world.db) != frozen {
		t.Fatalf("another person's set: %d\n%s", foreign.Code, foreign.Body)
	}
}

// diffLines counts the lines that differ between two dumps of the same rows.
func diffLines(a, b string) int {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(x) != len(y) {
		return -1
	}
	n := 0
	for i := range x {
		if x[i] != y[i] {
			n++
		}
	}
	return n
}

func lineDiff(a, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out strings.Builder
	for i := 0; i < len(x) || i < len(y); i++ {
		var l, r string
		if i < len(x) {
			l = x[i]
		}
		if i < len(y) {
			r = y[i]
		}
		if l != r {
			out.WriteString("- " + l + "\n+ " + r + "\n")
		}
	}
	return out.String()
}

// With lb chosen, every weight the app shows is in lb: the workout, the
// finish summary, a session's page, Progreso and its exercise detail, the
// picker and the exercise info.
func TestWeightsShowInTheDefaultUnitEverywhere(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	if err := svc.SetExerciseGroup(ctx, "sentadilla", "cuadriceps"); err != nil {
		t.Fatal(err)
	}
	earlier, err := svc.StartSession(ctx, "2026-08-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.AddSet(ctx, training.AddSetInput{SessionID: earlier.ID, Exercise: "sentadilla", WeightKG: 100, Reps: 5, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	post(t, h, base+"/sets", setForm("sentadilla", "100", "5", "8"), true)
	if err := svc.SetWeightUnit(ctx, "lb"); err != nil {
		t.Fatal(err)
	}
	sessions, err := svc.ListSessions(ctx, training.ListFilter{Limit: 1})
	if err != nil || len(sessions) != 1 {
		t.Fatal(sessions, err)
	}
	id := strconv.FormatInt(sessions[0].ID, 10)

	checks := map[string][]string{
		base + "/?modo=entrenar":                    {"220.5 lb × 5", `data-weight="220.5"`, "mejor 220.5 lb × 5", `<span>lb</span><span>Reps</span>`},
		base + "/s/" + id:                           {"220.5 lb × 5"},
		base + "/progress":                          {"mejor 220.5 × 5", "1.102 lb"},
		base + "/progress/exercise?name=sentadilla": {"220.5 × 5", "<small>lb</small>"},
		base + "/exercises":                         {`"last":"220.5×5"`},
		base + "/exercise-info?name=sentadilla":     {`"unit":"lb"`, `"weight":220.5`},
	}
	for path, wants := range checks {
		body := get(t, h, path).Body.String()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
		if strings.Contains(body, "100 kg") {
			t.Errorf("%s still shows kg", path)
		}
	}
}

// Strong's export has no unit: the import assumes the person's own.
func TestImportAssumesThePersonsUnit(t *testing.T) {
	h, svc := newServer(t)
	if page := get(t, h, base+"/import").Body.String(); !strings.Contains(page, `<option value="kg" selected>kg</option>`) {
		t.Fatalf("kg person:\n%s", page)
	}
	if err := svc.SetWeightUnit(context.Background(), "lb"); err != nil {
		t.Fatal(err)
	}
	if page := get(t, h, base+"/import").Body.String(); !strings.Contains(page, `<option value="lb" selected>lb</option>`) {
		t.Fatalf("lb person:\n%s", page)
	}
}
