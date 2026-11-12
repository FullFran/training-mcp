package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// Límites de volumen, under Tú, lists every muscle group with its MEV and MRV
// and records them through the call set_volume_landmarks makes.
func TestLandmarksPageListsAndRecordsMEVAndMRV(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()
	if err := svc.SetLandmarks(ctx, training.Landmarks{MuscleGroup: "pecho", MEV: 8, MRV: 20}); err != nil {
		t.Fatal(err)
	}

	if home := get(t, h, base+"/").Body.String(); !strings.Contains(home, `href="`+base+`/landmarks"`) {
		t.Fatalf("Tú does not link the landmarks:\n%s", home)
	}
	page := get(t, h, base+"/landmarks")
	body := page.Body.String()
	for _, want := range []string{`name="muscle_group" value="pecho"`, `name="mev" value="8"`, `name="mrv" value="20"`, `name="muscle_group" value="gemelo"`, `action="` + base + `/landmarks"`} {
		if page.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("landmarks page lacks %q (status %d):\n%s", want, page.Code, body)
		}
	}

	w := post(t, h, base+"/landmarks", url.Values{"muscle_group": {"espalda"}, "mev": {"10"}, "mrv": {"22"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/landmarks" {
		t.Fatalf("status %d, location %q", w.Code, w.Header().Get("Location"))
	}
	// A blank value is unknown, stored as 0, like the tool.
	if w = post(t, h, base+"/landmarks", url.Values{"muscle_group": {"pecho"}, "mev": {""}, "mrv": {"18"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("blank MEV: status %d", w.Code)
	}
	// MEV above MRV is refused on the page and stores nothing.
	bad := post(t, h, base+"/landmarks", url.Values{"muscle_group": {"espalda"}, "mev": {"30"}, "mrv": {"12"}}, false)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "El MEV no puede superar al MRV.") {
		t.Fatalf("MEV > MRV: %d\n%s", bad.Code, bad.Body)
	}

	got, err := svc.Landmarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (training.Landmarks{MuscleGroup: "pecho", MRV: 18}) || got[1] != (training.Landmarks{MuscleGroup: "espalda", MEV: 10, MRV: 22}) {
		t.Fatalf("landmarks = %+v", got)
	}
}
