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

// Every "create a routine" entry point lands on creating one: the name field
// at the top and focused, then the new routine's own editor to add exercises.
// Checked with no routines and with several.
func TestCreatingARoutineLandsOnItsEditor(t *testing.T) {
	for _, existing := range []int{0, 3} {
		t.Run(strconv.Itoa(existing)+" routines", func(t *testing.T) {
			h, svc := newServer(t)
			ctx := context.Background()
			for i := range existing {
				if _, err := svc.CreatePlan(ctx, training.Plan{Name: "Rutina vieja " + strconv.Itoa(i), Items: []training.PlanItem{{Exercise: "remo", TargetSets: 3}}}); err != nil {
					t.Fatal(err)
				}
			}
			newURL := base + "/plans?nueva=1"

			// Entrenar's empty state (no routines) and Planificar › Rutinas both
			// point at the create screen.
			if existing == 0 {
				home := section(t, get(t, h, base+"/").Body.String(), `id="home"`, "</section>")
				if !strings.Contains(home, `href="`+newURL+`"`) {
					t.Fatalf("Crear rutina a mano does not open the create screen:\n%s", home)
				}
			}
			plans := get(t, h, base+"/plans").Body.String()
			if !strings.Contains(plans, `href="`+newURL+`"`) || !strings.Contains(plans, "Crear a mano") {
				t.Fatalf("Rutinas offers no create link:\n%s", plans)
			}

			page := get(t, h, newURL)
			body := page.Body.String()
			if page.Code != http.StatusOK {
				t.Fatalf("create screen: %d", page.Code)
			}
			field := strings.Index(body, `name="name" placeholder="Empuje A" required autofocus`)
			if field < 0 || !strings.Contains(body, `action="`+base+`/plans/create" method="post"`) {
				t.Fatalf("create screen lacks the focused name field:\n%s", body)
			}
			if old := strings.Index(body, "Rutina vieja"); old >= 0 && old < field {
				t.Fatalf("existing routines come before the name field:\n%s", body)
			}

			if shell := get(t, h, base+"/static/shell.js").Body.String(); !strings.Contains(shell, "doc.querySelector('[autofocus]')") {
				t.Fatal("panels do not focus the autofocus field")
			}

			created := post(t, h, base+"/plans/create", url.Values{"name": {"Empuje A"}}, false)
			loc := created.Header().Get("Location")
			if created.Code != http.StatusSeeOther || !strings.HasPrefix(loc, base+"/plans?rutina=") {
				t.Fatalf("create: %d, location %q", created.Code, loc)
			}
			id := strings.TrimPrefix(loc, base+"/plans?rutina=")

			editor := get(t, h, loc).Body.String()
			if !strings.Contains(editor, `value="Empuje A"`) || strings.Contains(editor, "Rutina vieja") {
				t.Fatalf("editor does not show just the new routine:\n%s", editor)
			}
			if !strings.Contains(editor, `name="exercise" list="catalogue" placeholder="ejercicio" required autofocus`) {
				t.Fatalf("editor does not focus the exercise field:\n%s", editor)
			}
			if strings.Contains(editor, "por definir") || !strings.Contains(editor, "Sin ejercicios todavía: añade el primero.") {
				t.Fatalf("a new routine shows its placeholder:\n%s", editor)
			}
			if !strings.Contains(editor, `"rutina":"`+id+`"`) {
				t.Fatalf("editor edits do not keep the editor open:\n%s", editor)
			}

			// The first exercise replaces the placeholder the empty routine
			// was created with, and the answer stays on this routine.
			added := post(t, h, base+"/plans/item", url.Values{"plan_id": {id}, "rutina": {id}, "exercise": {"press banca"}, "target_sets": {"3"}}, true).Body.String()
			if strings.Contains(added, "Rutina vieja") || !strings.Contains(added, "press banca") {
				t.Fatalf("after adding, the editor shows:\n%s", added)
			}
			planID, _ := strconv.ParseInt(id, 10, 64)
			plan, err := svc.GetPlan(ctx, planID)
			if err != nil || len(plan.Items) != 1 || plan.Items[0].Exercise != "press banca" {
				t.Fatalf("routine items = %+v, %v", plan.Items, err)
			}

			// A blank name stays on the create screen with the reason.
			bad := post(t, h, base+"/plans/create", url.Values{"name": {"  "}}, false)
			if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "Ponle un nombre a la rutina.") || !strings.Contains(bad.Body.String(), "autofocus") {
				t.Fatalf("blank name: %d\n%s", bad.Code, bad.Body)
			}
		})
	}
}

// Another person's routine id opens what a missing id opens.
func TestRoutineEditorTreatsAnotherUsersRoutineAsMissing(t *testing.T) {
	world := newWebWorld(t)
	foreign := get(t, world.second, base+"/plans?rutina="+world.plan)
	missing := get(t, world.second, base+"/plans?rutina="+missingWebID)
	if foreign.Code != http.StatusNotFound || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign: %d %q, missing: %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
	owner := get(t, world.owner, base+"/plans?rutina="+world.plan)
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), secretPlan) {
		t.Fatalf("owner: %d %q", owner.Code, owner.Body)
	}
}
