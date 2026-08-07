package training

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

func NormalizeSI(si float64) float64 {
	return math.Round(si*10) / 10
}

func CalculateSI(rpe float64) float64 {
	v := 0.2*rpe - 0.6
	if v < 0 {
		return 0
	}
	return NormalizeSI(v)
}

type Service struct {
	store Store
	clock Clock
}

func NewService(store Store, clock Clock) *Service { return &Service{store: store, clock: clock} }

func (s *Service) StartSession(ctx context.Context, date string) (Session, error) {
	if date == "" {
		date = s.clock().In(s.clock().Location()).Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return Session{}, ErrValidation
	}
	return s.store.Start(ctx, Session{Date: date})
}
func (s *Service) AddSet(ctx context.Context, in AddSetInput) (Set, float64, error) {
	in.Exercise = strings.ToLower(strings.TrimSpace(in.Exercise))
	if in.SessionID <= 0 || in.Exercise == "" || in.WeightKG <= 0 || in.Reps <= 0 || in.RPE < 1 || in.RPE > 10 {
		return Set{}, 0, ErrValidation
	}
	return s.store.AddSet(ctx, in)
}
func (s *Service) UpdateSet(ctx context.Context, id int64, p SetPatch) (Set, float64, error) {
	if id <= 0 || p.Empty() || (p.Exercise != nil && strings.TrimSpace(*p.Exercise) == "") || (p.WeightKG != nil && *p.WeightKG <= 0) || (p.Reps != nil && *p.Reps <= 0) || (p.RPE != nil && (*p.RPE < 1 || *p.RPE > 10)) {
		return Set{}, 0, ErrValidation
	}
	if p.Exercise != nil {
		v := strings.ToLower(strings.TrimSpace(*p.Exercise))
		p.Exercise = &v
	}
	return s.store.UpdateSet(ctx, id, p)
}
func (s *Service) DeleteSet(ctx context.Context, id int64) (int64, float64, int, error) {
	if id <= 0 {
		return 0, 0, 0, ErrValidation
	}
	return s.store.DeleteSet(ctx, id)
}
func (s *Service) GetSession(ctx context.Context, id int64) (Session, error) {
	if id <= 0 {
		return Session{}, ErrValidation
	}
	return s.store.GetSession(ctx, id)
}
func (s *Service) ListSessions(ctx context.Context, f ListFilter) ([]SessionSummary, error) {
	if f.Limit < 0 || f.Limit > 100 || (f.From != "" && !validDate(f.From)) || (f.To != "" && !validDate(f.To)) {
		return nil, ErrValidation
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return nil, fmt.Errorf("%w: invalid date range: from must not be after to", ErrValidation)
	}
	if f.Limit == 0 {
		f.Limit = 20
	}
	sessions, err := s.store.ListSessions(ctx, f)
	if err == nil && sessions == nil {
		sessions = []SessionSummary{}
	}
	return sessions, err
}

// RecentExercises returns the most recently used exercises, newest first, each
// carrying the values of its last recorded set.
func (s *Service) RecentExercises(ctx context.Context, limit int) ([]ExerciseMemory, error) {
	if limit < 0 || limit > 50 {
		return nil, ErrValidation
	}
	if limit == 0 {
		limit = 8
	}
	out, err := s.store.RecentExercises(ctx, limit)
	if err == nil && out == nil {
		out = []ExerciseMemory{}
	}
	return out, err
}

func validDate(v string) bool { _, err := time.Parse("2006-01-02", v); return err == nil }
