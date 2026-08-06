package training

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCalculateSI(t *testing.T) {
	for _, tt := range []struct {
		name string
		rpe  float64
		want float64
	}{
		{"low effort", 1, 0},
		{"boundary", 2, 0},
		{"RPE 8", 8, 1.0},
		{"RPE 9", 9, 1.2},
		{"RPE 10", 10, 1.4},
		{"decimal RPE rounds SI to one decimal", 8.25, 1.1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := CalculateSI(tt.rpe); got != tt.want {
				t.Fatalf("CalculateSI(%v) = %v, want %v", tt.rpe, got, tt.want)
			}
		})
	}
}

func TestServiceStartSessionDefaultsToLocalDateAndNormalizesSets(t *testing.T) {
	store := newMemoryStore()
	clock := func() time.Time { return time.Date(2026, 8, 6, 23, 30, 0, 0, time.FixedZone("west", -2*60*60)) }
	svc := NewService(store, clock)
	session, err := svc.StartSession(context.Background(), "")
	if err != nil || session.Date != "2026-08-06" {
		t.Fatalf("StartSession() = %#v, %v", session, err)
	}
	set, total, err := svc.AddSet(context.Background(), AddSetInput{SessionID: session.ID, Exercise: "  Bench Press ", WeightKG: 80, Reps: 5, RPE: 8})
	if err != nil || set.Exercise != "bench press" || total != 1 || set.Position != 1 {
		t.Fatalf("AddSet() = %#v, %v, %v", set, total, err)
	}
}

func TestServicePatchAndAtomicValidation(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC) })
	session, _ := svc.StartSession(context.Background(), "2026-08-06")
	set, _, _ := svc.AddSet(context.Background(), AddSetInput{SessionID: session.ID, Exercise: "squat", WeightKG: 100, Reps: 3, RPE: 7})
	updated, total, err := svc.UpdateSet(context.Background(), set.ID, SetPatch{RPE: floatPtr(10)})
	if err != nil || updated.RPE != 10 || updated.SI != 1.4 || total != 1.4 {
		t.Fatalf("UpdateSet() = %#v, %v, %v", updated, total, err)
	}
	if _, _, err := svc.AddSet(context.Background(), AddSetInput{SessionID: session.ID, Exercise: "", WeightKG: 1, Reps: 1, RPE: 1}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid add error = %v, want validation", err)
	}
	got, _ := svc.GetSession(context.Background(), session.ID)
	if len(got.Sets) != 1 || got.Sets[0].WeightKG != 100 {
		t.Fatalf("invalid add mutated session: %#v", got)
	}
}

func TestServiceListSessionsDateRange(t *testing.T) {
	tests := []struct {
		name      string
		filter    ListFilter
		wantErr   bool
		wantCalls int
	}{
		{name: "inverted range is rejected", filter: ListFilter{From: "2026-08-07", To: "2026-08-06"}, wantErr: true},
		{name: "equal bounds are valid and inclusive", filter: ListFilter{From: "2026-08-06", To: "2026-08-06"}, wantCalls: 1},
		{name: "ascending range is valid", filter: ListFilter{From: "2026-08-05", To: "2026-08-06"}, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMemoryStore()
			store.listResults = []SessionSummary{{ID: 1, Date: "2026-08-06"}}
			svc := NewService(store, time.Now)

			got, err := svc.ListSessions(context.Background(), tt.filter)
			if tt.wantErr {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("ListSessions() error = %v, want validation error", err)
				}
				if err.Error() != "validation error: invalid date range: from must not be after to" {
					t.Fatalf("ListSessions() error = %q, want clear range validation", err)
				}
			} else if err != nil {
				t.Fatalf("ListSessions() error = %v", err)
			} else if len(got) != 1 || got[0].Date != "2026-08-06" {
				t.Fatalf("ListSessions() = %#v, want store result", got)
			}
			if store.listCalls != tt.wantCalls {
				t.Fatalf("store ListSessions calls = %d, want %d", store.listCalls, tt.wantCalls)
			}
			if tt.wantCalls == 1 && (store.listFilter.From != tt.filter.From || store.listFilter.To != tt.filter.To) {
				t.Fatalf("store filter = %#v, want bounds from %#v", store.listFilter, tt.filter)
			}
		})
	}
}

func TestServiceListSessionsResults(t *testing.T) {
	tests := []struct {
		name  string
		store []SessionSummary
		want  []SessionSummary
	}{
		{name: "empty results are an empty list", want: []SessionSummary{}},
		{
			name: "non-empty results preserve store ordering",
			store: []SessionSummary{
				{ID: 2, Date: "2026-08-06"},
				{ID: 1, Date: "2026-08-05"},
			},
			want: []SessionSummary{
				{ID: 2, Date: "2026-08-06"},
				{ID: 1, Date: "2026-08-05"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMemoryStore()
			store.listResults = tt.store
			svc := NewService(store, time.Now)

			got, err := svc.ListSessions(context.Background(), ListFilter{})
			if err != nil {
				t.Fatalf("ListSessions() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ListSessions() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

type memoryStore struct {
	nextSession, nextSet int64
	sessions             map[int64]Session
	sets                 map[int64]Set
	listCalls            int
	listFilter           ListFilter
	listResults          []SessionSummary
}

func newMemoryStore() *memoryStore {
	return &memoryStore{sessions: map[int64]Session{}, sets: map[int64]Set{}}
}
func (m *memoryStore) Start(_ context.Context, s Session) (Session, error) {
	m.nextSession++
	s.ID = m.nextSession
	m.sessions[s.ID] = s
	return s, nil
}
func (m *memoryStore) AddSet(_ context.Context, in AddSetInput) (Set, float64, error) {
	m.nextSet++
	s := Set{ID: m.nextSet, SessionID: in.SessionID, Position: 1, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, SI: CalculateSI(in.RPE)}
	m.sets[s.ID] = s
	return s, s.SI, nil
}
func (m *memoryStore) UpdateSet(_ context.Context, id int64, p SetPatch) (Set, float64, error) {
	s := m.sets[id]
	if p.RPE != nil {
		s.RPE = *p.RPE
		s.SI = CalculateSI(s.RPE)
	}
	m.sets[id] = s
	return s, s.SI, nil
}
func (m *memoryStore) DeleteSet(context.Context, int64) (int64, float64, int, error) {
	return 0, 0, 0, nil
}
func (m *memoryStore) GetSession(_ context.Context, id int64) (Session, error) {
	s := m.sessions[id]
	for _, set := range m.sets {
		if set.SessionID == id {
			s.Sets = append(s.Sets, set)
			s.TotalSI += set.SI
		}
	}
	return s, nil
}
func (m *memoryStore) ListSessions(_ context.Context, filter ListFilter) ([]SessionSummary, error) {
	m.listCalls++
	m.listFilter = filter
	return m.listResults, nil
}
func floatPtr(v float64) *float64 { return &v }
