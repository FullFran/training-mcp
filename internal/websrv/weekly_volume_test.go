package websrv_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// Progreso → Ejercicios shows the last four training weeks per muscle group,
// read from the same call weekly_volume makes: working sets only, Monday weeks.
func TestProgressShowsWeeklySetsPerMuscleGroup(t *testing.T) {
	h, svc := newServer(t) // today is Friday 2026-08-07, its week starts 2026-08-03
	ctx := context.Background()
	log := func(date, exercise string, sets int, category string) {
		t.Helper()
		session, err := svc.StartSession(ctx, date)
		if err != nil {
			t.Fatal(err)
		}
		for range sets {
			if _, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: exercise, WeightKG: 60, Reps: 8, RPE: 8, Category: category}); err != nil {
				t.Fatal(err)
			}
		}
	}
	log("2026-08-05", "press banca", 3, "")
	log("2026-08-05", "press banca", 1, training.SetWarmup)
	log("2026-07-29", "press banca", 2, "")
	log("2026-08-04", "remo", 1, "")
	log("2026-06-01", "press banca", 5, "") // older than four weeks
	for ex, group := range map[string]string{"press banca": "pecho", "remo": "espalda"} {
		if err := svc.SetExerciseGroup(ctx, ex, group); err != nil {
			t.Fatal(err)
		}
	}

	body := get(t, h, base+"/progress").Body.String()
	for _, want := range []string{
		"Series por semana",
		`data-cell="pecho 2026-08-03"><strong class="num">3</strong>`,
		`data-cell="pecho 2026-07-27"><strong class="num">2</strong>`,
		`data-cell="pecho 2026-07-20"><strong class="num">–</strong>`,
		`data-cell="espalda 2026-08-03"><strong class="num">1</strong>`,
		`<th scope="col" data-week="2026-07-13">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Progreso lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `data-week="2026-06-01"`) || strings.Contains(body, `data-week="2026-07-06"`) {
		t.Fatalf("Progreso shows weeks outside the last four:\n%s", body)
	}
	// Muscle groups keep the taxonomy's order: pecho before espalda.
	if strings.Index(body, `data-cell="pecho 2026-08-03"`) > strings.Index(body, `data-cell="espalda 2026-08-03"`) {
		t.Fatal("muscle groups out of order")
	}
}
