package training

import (
	"context"
	"testing"
)

// memoryStore keeps no bodyweight: the training tests that use it never reach
// these, and the sqlitestore tests cover the real store.
func (m *memoryStore) PutBodyweight(context.Context, Bodyweight) error { return nil }
func (m *memoryStore) Bodyweights(context.Context, string, string) ([]Bodyweight, error) {
	return nil, nil
}
func (m *memoryStore) DeleteBodyweight(context.Context, string) error { return ErrNotFound }
func (m *memoryStore) SetSessionNotes(context.Context, int64, string) error {
	return ErrNotFound
}

func TestSummarizeBodyweightKeepsTheEarliestOfEqualWeights(t *testing.T) {
	s := SummarizeBodyweight([]Bodyweight{
		{Date: "2026-11-01", WeightKG: 80},
		{Date: "2026-11-03", WeightKG: 79},
		{Date: "2026-11-05", WeightKG: 81},
		{Date: "2026-11-07", WeightKG: 79},
		{Date: "2026-11-20", WeightKG: 81},
	})
	if s.Min.Date != "2026-11-03" || s.Max.Date != "2026-11-05" {
		t.Fatalf("min %+v max %+v, want the earliest of each tie", s.Min, s.Max)
	}
	// Only the last entry falls in the 7 days ending on it.
	if s.Avg7.Entries != 1 || s.Avg7.WeightKG != 81 || s.Avg7.From != "2026-11-14" || s.ChangeKG != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if SummarizeBodyweight(nil) != nil {
		t.Fatal("no entries must have no summary")
	}
}
