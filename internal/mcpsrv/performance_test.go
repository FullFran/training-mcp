package mcpsrv

import (
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

func TestPerformanceDropOffExplainsTheComparisonOfASession(t *testing.T) {
	session := logSetsSession(t)
	var logged LogSetsOut
	decode(t, call(t, session, "log_sets", map[string]any{"idempotency_key": "remo", "exercise": "remo", "sets": []map[string]any{
		{"weight_kg": 59, "reps": 10, "rpe": 8}, {"weight_kg": 59, "reps": 10, "rpe": 8}, {"weight_kg": 59, "reps": 9},
	}}), &logged)
	call(t, session, "log_sets", map[string]any{"idempotency_key": "fly", "exercise": "aperturas", "sets": []map[string]any{{"weight_kg": 99.8, "reps": 6, "rpe": 8}}})

	var out PerformanceOut
	decode(t, call(t, session, "performance_dropoff", map[string]any{"session_id": logged.Session.ID, "exercise": " Remo "}), &out)
	if len(out.Analyses) != 1 {
		t.Fatalf("analyses = %#v", out.Analyses)
	}
	a := out.Analyses[0]
	if a.Exercise != "remo" || a.SessionID != logged.Session.ID || a.Date == "" || !a.Comparable || len(a.Sets) != 3 {
		t.Fatalf("analysis = %#v", a)
	}
	third := a.Sets[2]
	if third.RPEObserved {
		t.Fatalf("an unstated RPE reads as observed: %#v", third)
	}
	for _, e := range third.Estimates {
		if e.Model == training.EstimatorRIREpley && e.Unavailable != training.UnavailableRPENotObserved {
			t.Fatalf("RIR model used an imputed RPE: %#v", e)
		}
		if e.Model == training.EstimatorEpley && (e.DropOffPct == nil || *e.DropOffPct != 2.5) {
			t.Fatalf("epley drop = %#v, want 2.5", e)
		}
	}

	var all PerformanceOut
	decode(t, call(t, session, "performance_dropoff", map[string]any{"session_id": logged.Session.ID}), &all)
	if len(all.Analyses) != 2 || all.Analyses[0].Exercise != "remo" || all.Analyses[1].Exercise != "aperturas" || all.Analyses[1].Comparable {
		t.Fatalf("whole session = %#v", all.Analyses)
	}
	if !strings.Contains(all.Analyses[1].Explanation, "menos de dos") {
		t.Fatalf("single set did not abstain: %q", all.Analyses[1].Explanation)
	}
}

func TestPerformanceDropOffRejectsAMissingSession(t *testing.T) {
	session := logSetsSession(t)
	if result := call(t, session, "performance_dropoff", map[string]any{"session_id": 999}); !result.IsError {
		t.Fatal("accepted a missing session")
	}
	if result := call(t, session, "performance_dropoff", map[string]any{"session_id": 0}); !result.IsError {
		t.Fatal("accepted session id 0")
	}
}
