package websrv_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

type pickerSuggestion struct {
	Suggested *struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"suggested"`
}

func suggestedExercise(t *testing.T, body []byte) (string, string) {
	t.Helper()
	var out pickerSuggestion
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if out.Suggested == nil {
		return "", ""
	}
	return out.Suggested.Name, out.Suggested.Reason
}

// The picker's "Sugerido por el coach" row comes from data already stored, with
// no model call: the muscle group the volume recommendation says to raise most,
// else the group with the fewest working sets this week; its most recently
// logged exercise not already in today's session.
func TestPickerSuggestsAnExerciseForTheLeastCoveredGroup(t *testing.T) {
	h, svc := newServer(t)
	ctx := context.Background()

	// Nothing logged and nothing grouped: no suggestion.
	if name, _ := suggestedExercise(t, get(t, h, base+"/exercises").Body.Bytes()); name != "" {
		t.Fatalf("suggested %q with no data", name)
	}

	for ex, group := range map[string]string{"press banca": "pecho", "remo": "espalda", "sentadilla": "cuadriceps", "prensa": "cuadriceps"} {
		if err := svc.SetExerciseGroup(ctx, ex, group); err != nil {
			t.Fatal(err)
		}
	}
	log := func(date, exercise string, sets int) int64 {
		session, err := svc.StartSession(ctx, date)
		if err != nil {
			t.Fatal(err)
		}
		for range sets {
			if _, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: exercise, WeightKG: 60, Reps: 8, RPE: 8}); err != nil {
				t.Fatal(err)
			}
		}
		return session.ID
	}
	log("2026-07-28", "prensa", 3) // last week
	monday := log("2026-08-03", "press banca", 4)
	log("2026-08-04", "remo", 4)
	tuesday := log("2026-08-05", "sentadilla", 2)

	// Cuádriceps has the fewest sets this week (2); sentadilla is its latest.
	name, reason := suggestedExercise(t, get(t, h, base+"/exercises").Body.Bytes())
	if name != "sentadilla" || reason != "Cuadriceps: 2 series esta semana" {
		t.Fatalf("suggested %q (%q)", name, reason)
	}

	// Already in today's session: the group's next exercise instead.
	log("2026-08-07", "sentadilla", 1)
	if name, _ := suggestedExercise(t, get(t, h, base+"/exercises").Body.Bytes()); name != "prensa" {
		t.Fatalf("with sentadilla done today suggested %q", name)
	}

	// Two rated sessions with almost no stimulus for pecho: the volume
	// recommendation says to add sets there, and that comes first.
	for _, id := range []int64{monday, tuesday} {
		if err := svc.RecordFeedback(ctx, id, training.Feedback{MuscleGroup: "pecho"}); err != nil {
			t.Fatal(err)
		}
	}
	name, reason = suggestedExercise(t, get(t, h, base+"/exercises").Body.Bytes())
	if name != "press banca" || reason != "Pecho: el volumen pide +3 series" {
		t.Fatalf("with a recommendation suggested %q (%q)", name, reason)
	}

	// The picker shows it as one row on top while adding exercises.
	js := get(t, h, base+"/static/picker.js").Body.String()
	if !strings.Contains(js, "data.suggested") || !strings.Contains(js, "'Sugerido por el coach'") {
		t.Fatal("picker.js does not show the suggestion")
	}
}
