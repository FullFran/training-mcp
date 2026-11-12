package websrv_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Past sets typed in lb but stored as kg: the person previews a scope, ticks
// the sets, and confirms; only those weights change, it never applies twice,
// and it can be undone back to the exact previous values.
func TestCorrectingPastSetsFromLb(t *testing.T) {
	world := newWebWorld(t)
	// The owner's session holds the 80 kg set; two more, one stays as it is.
	for _, w := range []string{"135", "100"} {
		f := setForm("sentadilla", w, "5", "8")
		post(t, world.owner, base+"/sets", f, true)
	}
	var today string
	if err := world.db.QueryRow(`SELECT id FROM sessions WHERE user_id=1 AND date='2026-08-07'`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	var squat135, squat100 string
	if err := world.db.QueryRow(`SELECT id FROM sets WHERE weight_kg=135`).Scan(&squat135); err != nil {
		t.Fatal(err)
	}
	if err := world.db.QueryRow(`SELECT id FROM sets WHERE weight_kg=100`).Scan(&squat100); err != nil {
		t.Fatal(err)
	}

	// The way in: Tú, and the session's own page.
	if you := get(t, world.owner, base+"/").Body.String(); !strings.Contains(you, `href="`+base+`/units/fix"`) {
		t.Fatal("Tú does not offer the correction")
	}
	if page := get(t, world.owner, base+"/s/"+today).Body.String(); !strings.Contains(page, `href="`+base+`/units/fix?session_id=`+today+`"`) {
		t.Fatal("the session page does not offer the correction")
	}

	preview := get(t, world.owner, base+"/units/fix?session_id="+today)
	body := preview.Body.String()
	for _, want := range []string{
		"2 series", "135 kg → 61.25 kg", "100 kg → 45.35 kg",
		`name="set_ids" value="` + squat135 + `" checked`, `name="set_ids" value="` + squat100 + `" checked`,
		`action="` + base + `/units/fix" method="post"`, "Corregir las series marcadas",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("preview lacks %q:\n%s", want, body)
		}
	}
	// By exercise and dates too.
	if byExercise := get(t, world.owner, base+"/units/fix?exercise=sentadilla&from=2026-08-01&to=2026-08-07").Body.String(); !strings.Contains(byExercise, "2 series") {
		t.Fatalf("by exercise:\n%s", byExercise)
	}

	// Only the 135 set is ticked: only it changes, plus the history entry.
	before := dumpDatabase(t, world.db)
	done := post(t, world.owner, base+"/units/fix", url.Values{"set_ids": {squat135}}, false)
	if done.Code != http.StatusSeeOther || !strings.HasPrefix(done.Header().Get("Location"), base+"/units/fix?hecho=") {
		t.Fatalf("apply: %d %q %s", done.Code, done.Header().Get("Location"), done.Body)
	}
	after := dumpDatabase(t, world.db)
	gone, added := onlyIn(before, after), onlyIn(after, before)
	if len(gone) != 1 || !strings.HasPrefix(gone[0], "sets ") || !strings.Contains(gone[0], "135") {
		t.Fatalf("rows changed or removed besides the corrected set: %q", gone)
	}
	var rows []string
	for _, line := range added {
		if !strings.HasPrefix(line, "proposals ") && !strings.HasPrefix(line, "decision_records ") {
			rows = append(rows, line)
		}
	}
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "sets ") || !strings.Contains(rows[0], "61.25") {
		t.Fatalf("rows added or changed besides the corrected set and its history entry: %q", rows)
	}
	confirmation := get(t, world.owner, done.Header().Get("Location")).Body.String()
	if !strings.Contains(confirmation, "Corregida 1 serie") || !strings.Contains(confirmation, "/undo") {
		t.Fatalf("confirmation:\n%s", confirmation)
	}

	// Twice: the preview says so and a second correction is refused.
	if again := get(t, world.owner, base+"/units/fix?session_id="+today).Body.String(); !strings.Contains(again, "ya corregida") || strings.Contains(again, `value="`+squat135+`" checked`) {
		t.Fatalf("preview after correcting:\n%s", again)
	}
	frozen := dumpDatabase(t, world.db)
	twice := post(t, world.owner, base+"/units/fix", url.Values{"set_ids": {squat135}}, false)
	if twice.Code != http.StatusConflict || !strings.Contains(twice.Body.String(), "ya estaba corregida") {
		t.Fatalf("second correction: %d\n%s", twice.Code, twice.Body)
	}
	if dumpDatabase(t, world.db) != frozen {
		t.Fatal("a refused correction changed rows")
	}

	// Undo puts the exact previous values back.
	var proposal string
	if err := world.db.QueryRow(`SELECT id FROM proposals WHERE tool='correct_lb_sets'`).Scan(&proposal); err != nil {
		t.Fatal(err)
	}
	post(t, world.owner, base+"/review/"+proposal+"/undo", url.Values{}, false)
	var kg float64
	var mark any
	if err := world.db.QueryRow(`SELECT weight_kg, lb_corrected_at FROM sets WHERE id=?`, squat135).Scan(&kg, &mark); err != nil || kg != 135 || mark != nil {
		t.Fatalf("after undo: %v %v %v", kg, mark, err)
	}
}

// Another person's session is missing to the preview, as a missing id is.
func testLbFixPreviewIsThePersonsOwn(t *testing.T) {
	world := newWebWorld(t)
	foreign := get(t, world.second, base+"/units/fix?session_id="+world.session)
	missing := get(t, world.second, base+"/units/fix?session_id="+missingWebID)
	if foreign.Code != http.StatusNotFound || foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign: %d %q\nmissing: %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
	if owner := get(t, world.owner, base+"/units/fix?session_id="+world.session); owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), "80 kg →") {
		t.Fatalf("owner: %d\n%s", owner.Code, owner.Body)
	}
}

// Correcting another person's set answers as a missing set does and changes
// nothing, not even the history.
func testLbFixApplyIsThePersonsOwn(t *testing.T) {
	world := newWebWorld(t)
	before := dumpDatabase(t, world.db)
	a := post(t, world.second, base+"/units/fix", url.Values{"set_ids": {world.set}}, false)
	b := post(t, world.second, base+"/units/fix", url.Values{"set_ids": {missingWebID}}, false)
	if a.Code != http.StatusNotFound || a.Code != b.Code || a.Body.String() != b.Body.String() {
		t.Fatalf("foreign: %d %q\nmissing: %d %q", a.Code, a.Body, b.Code, b.Body)
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("rows changed:\n%q", onlyIn(after, before))
	}
	if owner := post(t, world.owner, base+"/units/fix", url.Values{"set_ids": {world.set}}, false); owner.Code != http.StatusSeeOther {
		t.Fatalf("owner: %d\n%s", owner.Code, owner.Body)
	}
}

// onlyIn lists the dump lines of a that b lacks, counting repeats.
func onlyIn(a, b string) []string {
	have := map[string]int{}
	for _, l := range strings.Split(b, "\n") {
		have[l]++
	}
	var out []string
	for _, l := range strings.Split(a, "\n") {
		if have[l] > 0 {
			have[l]--
			continue
		}
		out = append(out, l)
	}
	return out
}
