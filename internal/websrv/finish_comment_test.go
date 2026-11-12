package websrv_test

import (
	"strings"
	"testing"
)

// The finish summary offers "Comentar con el coach": a new Planificar
// conversation with a message about that session typed in. Only with the coach on.
func TestFinishSummaryOffersCommentingWithTheCoach(t *testing.T) {
	world := newCoachWorld(t, newFakeCoach())
	owner := world.as(1)
	if w := post(t, owner, base+"/sets", setForm("press banca", "80", "5", "8"), true); w.Code != 200 {
		t.Fatalf("logging a set: %d %s", w.Code, w.Body)
	}
	finish := section(t, get(t, owner, base+"/").Body.String(), `id="finish"`, "</section>")
	for _, want := range []string{
		`id="finish-comment"`, `href="` + base + `/?modo=planificar"`, `data-mode-go="plan"`, `data-new-chat`,
		`data-ask="Comenta mi sesión de hoy (Entreno libre, 2026-08-07): ¿qué tal ha ido y qué ajusto para la próxima?"`,
		">Comentar con el coach</a>",
	} {
		if !strings.Contains(finish, want) {
			t.Fatalf("finish summary lacks %s:\n%s", want, finish)
		}
	}

	// The link starts a new conversation before typing the message in.
	modes := get(t, owner, base+"/static/modes.js").Body.String()
	coach := get(t, owner, base+"/static/coach.js").Body.String()
	if !strings.Contains(modes, "data-new-chat") || !strings.Contains(modes, "'coach:new'") || !strings.Contains(coach, "addEventListener('coach:new', newChat)") {
		t.Fatal("modes.js and coach.js do not start a new conversation for data-new-chat")
	}

	h, _ := newServer(t)
	post(t, h, base+"/sets", setForm("press banca", "80", "5", "8"), true)
	if off := section(t, get(t, h, base+"/").Body.String(), `id="finish"`, "</section>"); strings.Contains(off, "Comentar con el coach") {
		t.Fatalf("with the coach off the summary must not offer it:\n%s", off)
	}
}
