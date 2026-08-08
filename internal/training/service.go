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
	return s.StartPlannedSession(ctx, date, 0)
}

// StartPlannedSession begins a session optionally following a plan. planID 0
// means an unplanned, free-form session.
func (s *Service) StartPlannedSession(ctx context.Context, date string, planID int64) (Session, error) {
	if date == "" {
		date = s.clock().In(s.clock().Location()).Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return Session{}, ErrValidation
	}
	if planID < 0 {
		return Session{}, ErrValidation
	}
	return s.store.Start(ctx, Session{Date: date, PlanID: planID})
}

// CreatePlan stores a reusable workout template. Exercise names are normalized
// exactly as AddSet normalizes them, so a plan item always matches the sets
// logged against it.
func (s *Service) CreatePlan(ctx context.Context, p Plan) (Plan, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Items) == 0 || len(p.Items) > 40 {
		return Plan{}, ErrValidation
	}
	for i := range p.Items {
		it := &p.Items[i]
		it.Exercise = strings.ToLower(strings.TrimSpace(it.Exercise))
		if it.Exercise == "" || it.TargetSets <= 0 || it.TargetSets > 20 {
			return Plan{}, ErrValidation
		}
		if it.RepMin < 0 || it.RepMax < 0 || (it.RepMax > 0 && it.RepMin > it.RepMax) {
			return Plan{}, fmt.Errorf("%w: rep range for %q is inverted", ErrValidation, it.Exercise)
		}
		if it.TargetRPE != 0 && (it.TargetRPE < 1 || it.TargetRPE > 10) {
			return Plan{}, ErrValidation
		}
	}
	return s.store.CreatePlan(ctx, p)
}

func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	out, err := s.store.ListPlans(ctx)
	if err == nil && out == nil {
		out = []Plan{}
	}
	return out, err
}

func (s *Service) GetPlan(ctx context.Context, id int64) (Plan, error) {
	if id <= 0 {
		return Plan{}, ErrValidation
	}
	return s.store.GetPlan(ctx, id)
}

func (s *Service) DeletePlan(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrValidation
	}
	return s.store.DeletePlan(ctx, id)
}

// SetSessionItem adds an exercise to today's session plan or replaces its
// prescription. Adjusting a session never touches the plan it came from.
func (s *Service) SetSessionItem(ctx context.Context, sessionID int64, it PlanItem) error {
	it.Exercise = strings.ToLower(strings.TrimSpace(it.Exercise))
	if sessionID <= 0 || it.Exercise == "" || it.TargetSets <= 0 || it.TargetSets > 20 {
		return ErrValidation
	}
	if it.RepMin < 0 || it.RepMax < 0 || (it.RepMax > 0 && it.RepMin > it.RepMax) {
		return fmt.Errorf("%w: rep range is inverted", ErrValidation)
	}
	if it.TargetRPE != 0 && (it.TargetRPE < 1 || it.TargetRPE > 10) {
		return ErrValidation
	}
	return s.store.SetSessionItem(ctx, sessionID, it)
}

// AdjustSessionItem changes only what is given: the set count, the rep range,
// the RPE, or whether the exercise is skipped today.
func (s *Service) AdjustSessionItem(ctx context.Context, sessionID int64, exercise string, p SessionItemPatch) error {
	exercise = strings.ToLower(strings.TrimSpace(exercise))
	if sessionID <= 0 || exercise == "" || p.Empty() {
		return ErrValidation
	}
	if p.TargetSets != nil && (*p.TargetSets <= 0 || *p.TargetSets > 20) {
		return ErrValidation
	}
	if p.RepMin != nil && *p.RepMin < 0 {
		return ErrValidation
	}
	if p.RepMax != nil && *p.RepMax < 0 {
		return ErrValidation
	}
	if p.RepMin != nil && p.RepMax != nil && *p.RepMax > 0 && *p.RepMin > *p.RepMax {
		return fmt.Errorf("%w: rep range is inverted", ErrValidation)
	}
	if p.TargetRPE != nil && *p.TargetRPE != 0 && (*p.TargetRPE < 1 || *p.TargetRPE > 10) {
		return ErrValidation
	}
	return s.store.PatchSessionItem(ctx, sessionID, exercise, p)
}

// SwapSessionItem substitutes an exercise in today's session, keeping its
// prescription. Use it when the planned machine is taken.
func (s *Service) SwapSessionItem(ctx context.Context, sessionID int64, from, to string) error {
	from = strings.ToLower(strings.TrimSpace(from))
	to = strings.ToLower(strings.TrimSpace(to))
	if sessionID <= 0 || from == "" || to == "" || from == to {
		return ErrValidation
	}
	return s.store.SwapSessionItem(ctx, sessionID, from, to)
}

// RemoveSessionItem drops an exercise from today's plan. Sets already logged
// against it are kept and reappear as off-plan work.
func (s *Service) RemoveSessionItem(ctx context.Context, sessionID int64, exercise string) error {
	exercise = strings.ToLower(strings.TrimSpace(exercise))
	if sessionID <= 0 || exercise == "" {
		return ErrValidation
	}
	return s.store.RemoveSessionItem(ctx, sessionID, exercise)
}

// SaveSessionAsPlan promotes what a session actually planned — after any
// in-session adjustments — into a reusable plan.
func (s *Service) SaveSessionAsPlan(ctx context.Context, sessionID int64, name string) (Plan, error) {
	progress, err := s.SessionProgress(ctx, sessionID)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Name: name}
	for _, p := range progress {
		if p.Skipped {
			continue
		}
		sets := p.TargetSets
		if sets == 0 {
			// Off-plan work becomes part of the template at the volume it was
			// actually performed.
			sets = p.DoneSets
		}
		if sets == 0 {
			continue
		}
		plan.Items = append(plan.Items, PlanItem{
			Exercise: p.Exercise, TargetSets: sets,
			RepMin: p.RepMin, RepMax: p.RepMax, TargetRPE: p.TargetRPE,
		})
	}
	return s.CreatePlan(ctx, plan)
}

// RecordFeedback stores how one muscle group responded to a session. Only the
// groups actually trained need rating, which keeps this a few taps.
func (s *Service) RecordFeedback(ctx context.Context, sessionID int64, f Feedback) error {
	f.MuscleGroup = strings.ToLower(strings.TrimSpace(f.MuscleGroup))
	if sessionID <= 0 || !f.Valid() {
		return ErrValidation
	}
	return s.store.SetFeedback(ctx, sessionID, f)
}

func (s *Service) SessionFeedback(ctx context.Context, sessionID int64) ([]Feedback, error) {
	if sessionID <= 0 {
		return nil, ErrValidation
	}
	out, err := s.store.SessionFeedback(ctx, sessionID)
	if err == nil && out == nil {
		out = []Feedback{}
	}
	return out, err
}

// VolumeRecommendation turns the latest feedback per muscle group into a set
// change for next week, alongside the volume that feedback was a response to.
func (s *Service) VolumeRecommendation(ctx context.Context) ([]SetChange, error) {
	latest, err := s.store.LatestFeedback(ctx)
	if err != nil {
		return nil, err
	}
	weeks, err := s.store.WeeklyVolume(ctx, ListFilter{})
	if err != nil {
		return nil, err
	}
	// Sets from the most recent week each group was trained, which is the
	// volume the rating responded to.
	lastSets := map[string]int{}
	seen := map[string]bool{}
	for _, w := range weeks {
		if !seen[w.MuscleGroup] {
			seen[w.MuscleGroup] = true
			lastSets[w.MuscleGroup] = w.Sets
		}
	}
	out := []SetChange{}
	for _, group := range MuscleGroups {
		f, ok := latest[group]
		if !ok {
			continue
		}
		delta, advice := RecommendSets(f.Magnitude())
		out = append(out, SetChange{
			MuscleGroup: group, Magnitude: f.Magnitude(),
			SetsDelta: delta, Advice: advice, LastWeekSets: lastSets[group],
		})
	}
	return out, nil
}

// SessionProgress reports planned versus completed sets for a session.
func (s *Service) SessionProgress(ctx context.Context, sessionID int64) ([]PlanProgress, error) {
	if sessionID <= 0 {
		return nil, ErrValidation
	}
	out, err := s.store.SessionProgress(ctx, sessionID)
	if err == nil && out == nil {
		out = []PlanProgress{}
	}
	return out, err
}

// LogSet is the one-call entry path: it finds today's session, creates it if
// needed, and records count identical sets. Callers never handle a session id,
// which is what makes conversational logging a single step.
func (s *Service) LogSet(ctx context.Context, exercise string, weightKG float64, reps int, rpe float64, count int, technique string) (Session, []Set, error) {
	if count <= 0 {
		count = 1
	}
	if count > 20 {
		return Session{}, nil, ErrValidation
	}
	today := s.clock().Format("2006-01-02")
	existing, err := s.ListSessions(ctx, ListFilter{From: today, To: today, Limit: 1})
	if err != nil {
		return Session{}, nil, err
	}
	var session Session
	if len(existing) > 0 {
		session = Session{ID: existing[0].ID, Date: existing[0].Date, PlanName: existing[0].PlanName}
	} else {
		// Validate before creating, so a bad call never leaves an empty session
		// behind as a side effect of a typo.
		if !validSetFields(exercise, weightKG, reps, rpe) {
			return Session{}, nil, ErrValidation
		}
		if session, err = s.StartSession(ctx, today); err != nil {
			return Session{}, nil, err
		}
	}
	var out []Set
	for range count {
		set, total, err := s.AddSet(ctx, AddSetInput{
			SessionID: session.ID, Exercise: exercise, WeightKG: weightKG,
			Reps: reps, RPE: rpe, Technique: technique,
		})
		if err != nil {
			return session, out, err
		}
		session.TotalSI = total
		out = append(out, set)
	}
	return session, out, nil
}

// validSetFields holds the rules a recorded set must satisfy, shared by every
// entry path so none of them can drift.
func validSetFields(exercise string, weightKG float64, reps int, rpe float64) bool {
	return strings.TrimSpace(exercise) != "" && weightKG > 0 && reps > 0 && rpe >= 1 && rpe <= 10
}

func (s *Service) AddSet(ctx context.Context, in AddSetInput) (Set, float64, error) {
	in.Exercise = strings.ToLower(strings.TrimSpace(in.Exercise))
	in.Technique = normalizeTechnique(in.Technique)
	if in.SessionID <= 0 || !validSetFields(in.Exercise, in.WeightKG, in.Reps, in.RPE) {
		return Set{}, 0, ErrValidation
	}
	if len(in.Technique) > maxTechniqueLen {
		return Set{}, 0, ErrValidation
	}
	return s.store.AddSet(ctx, in)
}

// normalizeTechnique lowercases and trims so "Drop Set" and "drop set" are the
// same technique, the same rule exercise names follow.
func normalizeTechnique(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(v))), " ")
}
func (s *Service) UpdateSet(ctx context.Context, id int64, p SetPatch) (Set, float64, error) {
	if id <= 0 || p.Empty() || (p.Exercise != nil && strings.TrimSpace(*p.Exercise) == "") || (p.WeightKG != nil && *p.WeightKG <= 0) || (p.Reps != nil && *p.Reps <= 0) || (p.RPE != nil && (*p.RPE < 1 || *p.RPE > 10)) {
		return Set{}, 0, ErrValidation
	}
	if p.Exercise != nil {
		v := strings.ToLower(strings.TrimSpace(*p.Exercise))
		p.Exercise = &v
	}
	if p.Technique != nil {
		v := normalizeTechnique(*p.Technique)
		if len(v) > maxTechniqueLen {
			return Set{}, 0, ErrValidation
		}
		p.Technique = &v
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

// DeleteSession removes a session and every set in it. Irreversible, so the
// number of sets destroyed is returned for the caller to report.
func (s *Service) DeleteSession(ctx context.Context, id int64) (int, error) {
	if id <= 0 {
		return 0, ErrValidation
	}
	return s.store.DeleteSession(ctx, id)
}

// ExerciseHistory returns one exercise's sets newest first, plus its best set
// by estimated 1RM.
func (s *Service) ExerciseHistory(ctx context.Context, exercise string, limit int) (ExerciseHistory, error) {
	exercise = strings.ToLower(strings.TrimSpace(exercise))
	if exercise == "" || limit < 0 || limit > 500 {
		return ExerciseHistory{}, ErrValidation
	}
	if limit == 0 {
		limit = 50
	}
	out, err := s.store.ExerciseHistory(ctx, exercise, limit)
	if err == nil && out.Sets == nil {
		out.Sets = []ExerciseSet{}
	}
	return out, err
}

// WeeklyVolume returns SI per muscle group bucketed by training week.
func (s *Service) WeeklyVolume(ctx context.Context, f ListFilter) ([]WeeklyVolume, error) {
	if (f.From != "" && !validDate(f.From)) || (f.To != "" && !validDate(f.To)) {
		return nil, ErrValidation
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return nil, fmt.Errorf("%w: invalid date range: from must not be after to", ErrValidation)
	}
	out, err := s.store.WeeklyVolume(ctx, f)
	if err == nil && out == nil {
		out = []WeeklyVolume{}
	}
	return out, err
}

// SetExerciseGroup assigns an exercise to a muscle group. The exercise name is
// normalized the same way AddSet normalizes it, so the catalogue key always
// matches what is stored on sets.
func (s *Service) SetExerciseGroup(ctx context.Context, exercise, group string) error {
	exercise = strings.ToLower(strings.TrimSpace(exercise))
	group = strings.ToLower(strings.TrimSpace(group))
	if exercise == "" || !ValidMuscleGroup(group) {
		return ErrValidation
	}
	return s.store.SetExerciseGroup(ctx, ExerciseGroup{Exercise: exercise, MuscleGroup: group})
}

func (s *Service) ExerciseGroups(ctx context.Context) ([]ExerciseGroup, error) {
	out, err := s.store.ExerciseGroups(ctx)
	if err == nil && out == nil {
		out = []ExerciseGroup{}
	}
	return out, err
}

// VolumeByGroup totals SI per muscle group over an optional date range.
func (s *Service) VolumeByGroup(ctx context.Context, f ListFilter) ([]GroupVolume, error) {
	if (f.From != "" && !validDate(f.From)) || (f.To != "" && !validDate(f.To)) {
		return nil, ErrValidation
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return nil, fmt.Errorf("%w: invalid date range: from must not be after to", ErrValidation)
	}
	out, err := s.store.VolumeByGroup(ctx, f)
	if err == nil && out == nil {
		out = []GroupVolume{}
	}
	return out, err
}

func validDate(v string) bool { _, err := time.Parse("2006-01-02", v); return err == nil }
