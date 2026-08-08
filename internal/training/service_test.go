package training

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	recentLimit          int
	recentResults        []ExerciseMemory
	groups               map[string]string
	volumeFilter         ListFilter
	volumeResults        []GroupVolume
	historyExercise      string
	historyLimit         int
	historyResult        ExerciseHistory
	weeklyFilter         ListFilter
	weeklyResults        []WeeklyVolume
	planCreated          Plan
	plans                []Plan
	progress             []PlanProgress
	itemSet              PlanItem
	itemPatched          string
	itemPatch            SessionItemPatch
	swapFrom, swapTo     string
	itemRemoved          string
	feedback             map[string]Feedback
	snapshotPath         string
	notes                map[string]string
	supersets            map[string]string
	planPatch            PlanPatch
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
	s := Set{ID: m.nextSet, SessionID: in.SessionID, Position: 1, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, SI: CalculateSI(in.RPE), Technique: in.Technique, Superset: in.Superset}
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
func TestServiceRecentExercises(t *testing.T) {
	svc := func(store *memoryStore) *Service {
		return NewService(store, func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) })
	}
	t.Run("zero limit falls back to the default", func(t *testing.T) {
		store := newMemoryStore()
		if _, err := svc(store).RecentExercises(context.Background(), 0); err != nil {
			t.Fatalf("RecentExercises() error = %v", err)
		}
		if store.recentLimit != 8 {
			t.Fatalf("limit forwarded = %d, want 8", store.recentLimit)
		}
	})
	t.Run("out of range limits are rejected", func(t *testing.T) {
		for _, limit := range []int{-1, 51} {
			if _, err := svc(newMemoryStore()).RecentExercises(context.Background(), limit); !errors.Is(err, ErrValidation) {
				t.Fatalf("RecentExercises(%d) error = %v, want validation", limit, err)
			}
		}
	})
	t.Run("nil results become an empty slice", func(t *testing.T) {
		got, err := svc(newMemoryStore()).RecentExercises(context.Background(), 5)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("RecentExercises() = %#v, %v, want empty non-nil", got, err)
		}
	})
	t.Run("results pass through in store order", func(t *testing.T) {
		store := newMemoryStore()
		store.recentResults = []ExerciseMemory{{Exercise: "squat", WeightKG: 100, Reps: 3, RPE: 8}}
		got, err := svc(store).RecentExercises(context.Background(), 5)
		if err != nil || !reflect.DeepEqual(got, store.recentResults) {
			t.Fatalf("RecentExercises() = %#v, %v", got, err)
		}
	})
}

func (m *memoryStore) RecentExercises(_ context.Context, limit int) ([]ExerciseMemory, error) {
	m.recentLimit = limit
	return m.recentResults, nil
}
func floatPtr(v float64) *float64 { return &v }

func (m *memoryStore) SetExerciseGroup(_ context.Context, g ExerciseGroup) error {
	if m.groups == nil {
		m.groups = map[string]string{}
	}
	m.groups[g.Exercise] = g.MuscleGroup
	return nil
}
func (m *memoryStore) ExerciseGroups(context.Context) ([]ExerciseGroup, error) {
	var out []ExerciseGroup
	for e, g := range m.groups {
		out = append(out, ExerciseGroup{Exercise: e, MuscleGroup: g})
	}
	return out, nil
}
func (m *memoryStore) VolumeByGroup(_ context.Context, f ListFilter) ([]GroupVolume, error) {
	m.volumeFilter = f
	return m.volumeResults, nil
}

func TestServiceExerciseGroupValidation(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()

	if err := svc.SetExerciseGroup(ctx, "  Press Banca ", "PECHO"); err != nil {
		t.Fatalf("SetExerciseGroup() error = %v", err)
	}
	// The key must match how AddSet stores the exercise, or volume never joins.
	if got := store.groups["press banca"]; got != "pecho" {
		t.Fatalf("stored mapping = %#v, want normalized key and group", store.groups)
	}
	for _, tt := range []struct{ exercise, group string }{
		{"", "pecho"},
		{"banca", ""},
		{"banca", "biceps femoral"},
	} {
		if err := svc.SetExerciseGroup(ctx, tt.exercise, tt.group); !errors.Is(err, ErrValidation) {
			t.Fatalf("SetExerciseGroup(%q,%q) error = %v, want validation", tt.exercise, tt.group, err)
		}
	}
	if _, err := svc.VolumeByGroup(ctx, ListFilter{From: "2026-08-07", To: "2026-08-01"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("inverted range error = %v, want validation", err)
	}
	got, err := svc.VolumeByGroup(ctx, ListFilter{})
	if err != nil || got == nil {
		t.Fatalf("VolumeByGroup() = %#v, %v, want empty non-nil", got, err)
	}
}

func (m *memoryStore) DeleteSession(_ context.Context, id int64) (int, error) {
	n := 0
	for sid, s := range m.sets {
		if s.SessionID == id {
			delete(m.sets, sid)
			n++
		}
	}
	if _, ok := m.sessions[id]; !ok {
		return 0, ErrNotFound
	}
	delete(m.sessions, id)
	return n, nil
}
func (m *memoryStore) ExerciseHistory(_ context.Context, exercise string, limit int) (ExerciseHistory, error) {
	m.historyExercise, m.historyLimit = exercise, limit
	return m.historyResult, nil
}
func (m *memoryStore) WeeklyVolume(_ context.Context, f ListFilter) ([]WeeklyVolume, error) {
	m.weeklyFilter = f
	return m.weeklyResults, nil
}

func TestEpley1RM(t *testing.T) {
	for _, tt := range []struct {
		name   string
		weight float64
		reps   int
		want   float64
	}{
		{"single rep returns the weight", 100, 1, 103.3},
		{"five reps", 100, 5, 116.7},
		{"non positive weight is zero", 0, 5, 0},
		{"non positive reps is zero", 100, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Epley1RM(tt.weight, tt.reps); got != tt.want {
				t.Fatalf("Epley1RM(%v,%v) = %v, want %v", tt.weight, tt.reps, got, tt.want)
			}
		})
	}
}

func TestServiceDeleteSessionValidation(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	if _, err := svc.DeleteSession(context.Background(), 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("DeleteSession(0) error = %v, want validation", err)
	}
	session, _ := svc.StartSession(context.Background(), "2026-08-07")
	if _, _, err := svc.AddSet(context.Background(), AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 5, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	n, err := svc.DeleteSession(context.Background(), session.ID)
	if err != nil || n != 1 {
		t.Fatalf("DeleteSession() = %d, %v, want 1", n, err)
	}
	if _, err := svc.DeleteSession(context.Background(), session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v, want not found", err)
	}
}

func TestServiceExerciseHistoryNormalizesAndBoundsInput(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()

	if _, err := svc.ExerciseHistory(ctx, "  Press Banca ", 0); err != nil {
		t.Fatalf("ExerciseHistory() error = %v", err)
	}
	// The lookup key must match how AddSet stores the exercise.
	if store.historyExercise != "press banca" || store.historyLimit != 50 {
		t.Fatalf("forwarded = %q/%d, want normalized name and default limit", store.historyExercise, store.historyLimit)
	}
	for _, tt := range []struct {
		exercise string
		limit    int
	}{{"", 10}, {"banca", -1}, {"banca", 501}} {
		if _, err := svc.ExerciseHistory(ctx, tt.exercise, tt.limit); !errors.Is(err, ErrValidation) {
			t.Fatalf("ExerciseHistory(%q,%d) error = %v, want validation", tt.exercise, tt.limit, err)
		}
	}
	got, err := svc.ExerciseHistory(ctx, "banca", 10)
	if err != nil || got.Sets == nil {
		t.Fatalf("ExerciseHistory() = %#v, %v, want empty non-nil sets", got, err)
	}
}

func TestServiceWeeklyVolumeValidatesRange(t *testing.T) {
	svc := NewService(newMemoryStore(), time.Now)
	ctx := context.Background()
	if _, err := svc.WeeklyVolume(ctx, ListFilter{From: "2026-08-07", To: "2026-08-01"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("inverted range error = %v, want validation", err)
	}
	if _, err := svc.WeeklyVolume(ctx, ListFilter{From: "nope"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad date error = %v, want validation", err)
	}
	got, err := svc.WeeklyVolume(ctx, ListFilter{})
	if err != nil || got == nil {
		t.Fatalf("WeeklyVolume() = %#v, %v, want empty non-nil", got, err)
	}
}

func (m *memoryStore) CreatePlan(_ context.Context, p Plan) (Plan, error) {
	m.planCreated = p
	p.ID = 1
	return p, nil
}
func (m *memoryStore) ListPlans(context.Context) ([]Plan, error) { return m.plans, nil }
func (m *memoryStore) GetPlan(_ context.Context, id int64) (Plan, error) {
	for _, p := range m.plans {
		if p.ID == id {
			return p, nil
		}
	}
	return Plan{}, ErrNotFound
}
func (m *memoryStore) DeletePlan(context.Context, int64) error { return nil }
func (m *memoryStore) SessionProgress(context.Context, int64) ([]PlanProgress, error) {
	return m.progress, nil
}

func TestServiceCreatePlanValidation(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()

	good := Plan{Name: " Empuje A ", Items: []PlanItem{{Exercise: " Press Banca ", TargetSets: 3, RepMin: 8, RepMax: 10, TargetRPE: 9}}}
	if _, err := svc.CreatePlan(ctx, good); err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	// Names must be normalized the same way sets are, or a plan item never
	// matches the sets logged against it.
	if store.planCreated.Name != "Empuje A" || store.planCreated.Items[0].Exercise != "press banca" {
		t.Fatalf("plan not normalized: %#v", store.planCreated)
	}

	for _, tt := range []struct {
		name string
		plan Plan
	}{
		{"no name", Plan{Items: []PlanItem{{Exercise: "banca", TargetSets: 1}}}},
		{"no items", Plan{Name: "x"}},
		{"empty exercise", Plan{Name: "x", Items: []PlanItem{{TargetSets: 1}}}},
		{"zero sets", Plan{Name: "x", Items: []PlanItem{{Exercise: "banca"}}}},
		{"too many sets", Plan{Name: "x", Items: []PlanItem{{Exercise: "banca", TargetSets: 21}}}},
		{"inverted rep range", Plan{Name: "x", Items: []PlanItem{{Exercise: "banca", TargetSets: 3, RepMin: 12, RepMax: 8}}}},
		{"rpe out of range", Plan{Name: "x", Items: []PlanItem{{Exercise: "banca", TargetSets: 3, TargetRPE: 11}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.CreatePlan(ctx, tt.plan); !errors.Is(err, ErrValidation) {
				t.Fatalf("CreatePlan(%s) error = %v, want validation", tt.name, err)
			}
		})
	}
}

func TestServicePlanLookupsValidateIDs(t *testing.T) {
	svc := NewService(newMemoryStore(), time.Now)
	ctx := context.Background()
	if _, err := svc.GetPlan(ctx, 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("GetPlan(0) error = %v", err)
	}
	if err := svc.DeletePlan(ctx, 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("DeletePlan(0) error = %v", err)
	}
	if _, err := svc.SessionProgress(ctx, 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("SessionProgress(0) error = %v", err)
	}
	if _, err := svc.StartPlannedSession(ctx, "2026-08-08", -1); !errors.Is(err, ErrValidation) {
		t.Fatalf("negative plan id error = %v, want validation", err)
	}
}

func (m *memoryStore) SetSessionItem(_ context.Context, id int64, it PlanItem) error {
	m.itemSet = it
	return nil
}
func (m *memoryStore) PatchSessionItem(_ context.Context, id int64, ex string, p SessionItemPatch) error {
	m.itemPatched, m.itemPatch = ex, p
	return nil
}
func (m *memoryStore) SwapSessionItem(_ context.Context, id int64, from, to string) error {
	m.swapFrom, m.swapTo = from, to
	return nil
}
func (m *memoryStore) RemoveSessionItem(_ context.Context, id int64, ex string) error {
	m.itemRemoved = ex
	return nil
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func TestServiceSessionItemValidation(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()

	if err := svc.SetSessionItem(ctx, 1, PlanItem{Exercise: " Press Banca ", TargetSets: 3}); err != nil {
		t.Fatalf("SetSessionItem() error = %v", err)
	}
	if store.itemSet.Exercise != "press banca" {
		t.Fatalf("exercise not normalized: %#v", store.itemSet)
	}
	for _, tt := range []struct {
		name string
		id   int64
		item PlanItem
	}{
		{"bad session", 0, PlanItem{Exercise: "banca", TargetSets: 1}},
		{"empty exercise", 1, PlanItem{TargetSets: 1}},
		{"zero sets", 1, PlanItem{Exercise: "banca"}},
		{"inverted range", 1, PlanItem{Exercise: "banca", TargetSets: 3, RepMin: 12, RepMax: 8}},
		{"rpe out of range", 1, PlanItem{Exercise: "banca", TargetSets: 3, TargetRPE: 11}},
	} {
		if err := svc.SetSessionItem(ctx, tt.id, tt.item); !errors.Is(err, ErrValidation) {
			t.Fatalf("SetSessionItem(%s) error = %v, want validation", tt.name, err)
		}
	}
}

func TestServiceAdjustSessionItem(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()

	if err := svc.AdjustSessionItem(ctx, 1, " Banca ", SessionItemPatch{TargetSets: intPtr(3)}); err != nil {
		t.Fatalf("AdjustSessionItem() error = %v", err)
	}
	if store.itemPatched != "banca" || *store.itemPatch.TargetSets != 3 {
		t.Fatalf("patch not forwarded normalized: %q %#v", store.itemPatched, store.itemPatch)
	}
	// An empty patch is a caller mistake, not a silent no-op.
	if err := svc.AdjustSessionItem(ctx, 1, "banca", SessionItemPatch{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty patch error = %v, want validation", err)
	}
	for _, tt := range []struct {
		name  string
		patch SessionItemPatch
	}{
		{"zero sets", SessionItemPatch{TargetSets: intPtr(0)}},
		{"too many sets", SessionItemPatch{TargetSets: intPtr(21)}},
		{"inverted range", SessionItemPatch{RepMin: intPtr(12), RepMax: intPtr(8)}},
	} {
		if err := svc.AdjustSessionItem(ctx, 1, "banca", tt.patch); !errors.Is(err, ErrValidation) {
			t.Fatalf("AdjustSessionItem(%s) error = %v, want validation", tt.name, err)
		}
	}
	// Skipping only needs the flag.
	if err := svc.AdjustSessionItem(ctx, 1, "banca", SessionItemPatch{Skipped: boolPtr(true)}); err != nil {
		t.Fatalf("skip error = %v", err)
	}
}

func TestServiceSwapAndRemoveSessionItem(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()

	if err := svc.SwapSessionItem(ctx, 1, " Banca ", " Press Plano "); err != nil {
		t.Fatalf("SwapSessionItem() error = %v", err)
	}
	if store.swapFrom != "banca" || store.swapTo != "press plano" {
		t.Fatalf("swap not normalized: %q -> %q", store.swapFrom, store.swapTo)
	}
	// Swapping an exercise for itself is a no-op the caller did not mean.
	if err := svc.SwapSessionItem(ctx, 1, "banca", "banca"); !errors.Is(err, ErrValidation) {
		t.Fatalf("self swap error = %v, want validation", err)
	}
	if err := svc.SwapSessionItem(ctx, 1, "banca", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty replacement error = %v, want validation", err)
	}
	if err := svc.RemoveSessionItem(ctx, 1, " Banca "); err != nil || store.itemRemoved != "banca" {
		t.Fatalf("RemoveSessionItem() = %v, removed %q", err, store.itemRemoved)
	}
	if err := svc.RemoveSessionItem(ctx, 1, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty exercise error = %v, want validation", err)
	}
}

func (m *memoryStore) SetFeedback(_ context.Context, id int64, f Feedback) error {
	if m.feedback == nil {
		m.feedback = map[string]Feedback{}
	}
	m.feedback[f.MuscleGroup] = f
	return nil
}
func (m *memoryStore) SessionFeedback(context.Context, int64) ([]Feedback, error) {
	var out []Feedback
	for _, f := range m.feedback {
		out = append(out, f)
	}
	return out, nil
}
func (m *memoryStore) LatestFeedback(context.Context) (map[string]Feedback, error) {
	return m.feedback, nil
}

func TestRecommendSetsFollowsTheSpreadsheetAnchors(t *testing.T) {
	for _, tt := range []struct {
		magnitude int
		delta     int
	}{
		{0, 3}, {1, 3}, // sheet anchor: magnitude 0 -> "Sube 3 series"
		{2, 2}, {3, 2},
		{4, 1}, {5, 1},
		{6, 0}, {7, 0}, // sheet anchor: magnitude 7 -> "Mantén o reduce 1 serie"
		{8, -1}, {9, -1},
	} {
		if delta, advice := RecommendSets(tt.magnitude); delta != tt.delta || advice == "" {
			t.Fatalf("RecommendSets(%d) = %d %q, want %d", tt.magnitude, delta, advice, tt.delta)
		}
	}
}

func TestFeedbackValidation(t *testing.T) {
	svc := NewService(newMemoryStore(), time.Now)
	ctx := context.Background()

	if err := svc.RecordFeedback(ctx, 1, Feedback{MuscleGroup: " PECHO ", Fatigue: 2, Pump: 3, Recovery: 1}); err != nil {
		t.Fatalf("RecordFeedback() error = %v", err)
	}
	if got := (Feedback{Fatigue: 2, Pump: 3, Recovery: 1}).Magnitude(); got != 6 {
		t.Fatalf("Magnitude() = %d, want 6", got)
	}
	for _, tt := range []struct {
		name string
		id   int64
		f    Feedback
	}{
		{"bad session", 0, Feedback{MuscleGroup: "pecho"}},
		{"unknown group", 1, Feedback{MuscleGroup: "biceps femoral"}},
		{"rating too high", 1, Feedback{MuscleGroup: "pecho", Fatigue: 4}},
		{"negative rating", 1, Feedback{MuscleGroup: "pecho", Pump: -1}},
	} {
		if err := svc.RecordFeedback(ctx, tt.id, tt.f); !errors.Is(err, ErrValidation) {
			t.Fatalf("RecordFeedback(%s) error = %v, want validation", tt.name, err)
		}
	}
}

func TestServiceLogSetCreatesTodaysSessionOnceAndRecordsRepeats(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC) })
	ctx := context.Background()

	session, sets, err := svc.LogSet(ctx, " Press Banca ", 80, 10, 9, 3, "")
	if err != nil || len(sets) != 3 {
		t.Fatalf("LogSet() = %#v, %v", sets, err)
	}
	if session.Date != "2026-08-08" || sets[0].Exercise != "press banca" {
		t.Fatalf("session/exercise wrong: %#v %#v", session, sets[0])
	}
	// Invalid input must not leave an empty session behind.
	store2 := newMemoryStore()
	svc2 := NewService(store2, func() time.Time { return time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC) })
	if _, _, err := svc2.LogSet(ctx, "", 80, 10, 9, 1, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("LogSet with empty exercise = %v, want validation", err)
	}
	if len(store2.sessions) != 0 {
		t.Fatalf("a rejected log created a session: %#v", store2.sessions)
	}
	if _, _, err := svc.LogSet(ctx, "banca", 80, 10, 9, 21, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("count above the cap = %v, want validation", err)
	}
}

func TestServiceNormalizesAndBoundsTechnique(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, func() time.Time { return time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC) })
	ctx := context.Background()
	session, _ := svc.StartSession(ctx, "2026-08-08")

	set, _, err := svc.AddSet(ctx, AddSetInput{
		SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 9,
		Technique: "  Drop   Set  ",
	})
	if err != nil || set.Technique != "drop set" {
		t.Fatalf("technique = %q, %v, want normalized", set.Technique, err)
	}
	_, _, err = svc.AddSet(ctx, AddSetInput{
		SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 9,
		Technique: strings.Repeat("x", 41),
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("over-long technique = %v, want validation", err)
	}
}

func (m *memoryStore) Snapshot(_ context.Context, path string) error {
	m.snapshotPath = path
	return nil
}

func (m *memoryStore) SetExerciseNote(_ context.Context, n ExerciseNote) error {
	if m.notes == nil {
		m.notes = map[string]string{}
	}
	if n.Note == "" {
		delete(m.notes, n.Exercise)
		return nil
	}
	m.notes[n.Exercise] = n.Note
	return nil
}
func (m *memoryStore) ExerciseNotes(context.Context) ([]ExerciseNote, error) {
	var out []ExerciseNote
	for e, n := range m.notes {
		out = append(out, ExerciseNote{Exercise: e, Note: n})
	}
	return out, nil
}
func (m *memoryStore) SupersetFor(_ context.Context, _ int64, exercise string) (string, error) {
	return m.supersets[exercise], nil
}

func TestServiceDerivesSupersetFromTheSessionPlan(t *testing.T) {
	store := newMemoryStore()
	store.supersets = map[string]string{"pull over": "A"}
	svc := NewService(store, time.Now)
	ctx := context.Background()
	session, _ := svc.StartSession(ctx, "2026-08-08")

	set, _, err := svc.AddSet(ctx, AddSetInput{SessionID: session.ID, Exercise: "pull over", WeightKG: 40, Reps: 10, RPE: 8})
	if err != nil || set.Superset != "A" {
		t.Fatalf("superset = %q, %v, want derived 'A'", set.Superset, err)
	}
	// An explicit label wins, for work done off any plan.
	set, _, err = svc.AddSet(ctx, AddSetInput{SessionID: session.ID, Exercise: "pull over", WeightKG: 40, Reps: 10, RPE: 8, Superset: " B "})
	if err != nil || set.Superset != "b" {
		t.Fatalf("explicit superset = %q, %v", set.Superset, err)
	}
	other, _, err := svc.AddSet(ctx, AddSetInput{SessionID: session.ID, Exercise: "curl", WeightKG: 10, Reps: 12, RPE: 8})
	if err != nil || other.Superset != "" {
		t.Fatalf("unlabelled exercise = %q", other.Superset)
	}
}

func TestServiceExerciseNoteValidation(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()
	if err := svc.SetExerciseNote(ctx, " Prensa ", "  asiento 4  "); err != nil {
		t.Fatalf("SetExerciseNote() error = %v", err)
	}
	if store.notes["prensa"] != "asiento 4" {
		t.Fatalf("note not normalized: %#v", store.notes)
	}
	if err := svc.SetExerciseNote(ctx, "", "x"); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty exercise = %v, want validation", err)
	}
	if err := svc.SetExerciseNote(ctx, "prensa", strings.Repeat("x", 281)); !errors.Is(err, ErrValidation) {
		t.Fatalf("over-long note = %v, want validation", err)
	}
	if err := svc.SetExerciseNote(ctx, "prensa", ""); err != nil || len(store.notes) != 0 {
		t.Fatalf("empty note should remove: %v %#v", err, store.notes)
	}
}

func (m *memoryStore) UpdatePlan(_ context.Context, id int64, p PlanPatch) error {
	m.planPatch = p
	return nil
}

func TestServiceUpdatePlanEditsIdentityOnly(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(store, time.Now)
	ctx := context.Background()

	notes := "  Bloque de acumulación, semana 3  "
	if err := svc.UpdatePlan(ctx, 1, PlanPatch{Notes: &notes}); err != nil {
		t.Fatalf("UpdatePlan() error = %v", err)
	}
	if *store.planPatch.Notes != "Bloque de acumulación, semana 3" {
		t.Fatalf("notes not trimmed: %q", *store.planPatch.Notes)
	}
	if store.planPatch.Name != nil {
		t.Fatalf("omitted field must stay nil, got %#v", store.planPatch.Name)
	}
	empty := ""
	if err := svc.UpdatePlan(ctx, 1, PlanPatch{Name: &empty}); !errors.Is(err, ErrValidation) {
		t.Fatalf("blank name = %v, want validation", err)
	}
	if err := svc.UpdatePlan(ctx, 1, PlanPatch{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty patch = %v, want validation", err)
	}
	long := strings.Repeat("x", 2001)
	if err := svc.UpdatePlan(ctx, 1, PlanPatch{Notes: &long}); !errors.Is(err, ErrValidation) {
		t.Fatalf("over-long notes = %v, want validation", err)
	}
}
