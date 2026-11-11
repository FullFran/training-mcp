package engine

import (
	"strings"
	"testing"
	"time"
)

// The coach reads from the event whether a set was a warm-up, which never
// counts, or a working set. Anything but a warm-up is a working set.
func TestSetLoggedEventCarriesTheCategory(t *testing.T) {
	at := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	for category, want := range map[string]string{
		"warmup":  " rpe=6 category=warmup (rest started at this time)",
		"working": " rpe=6 category=working (rest started at this time)",
		"":        " rpe=6 category=working (rest started at this time)",
		"x\nevil": " rpe=6 category=working (rest started at this time)",
	} {
		ev := SetLogged(at, "sentadilla", 60, 5, 6, "", category)
		got := composeContext(at, []WorkoutEvent{ev})
		if !strings.HasSuffix(got, want) {
			t.Fatalf("category %q: %q", category, got)
		}
	}
}
