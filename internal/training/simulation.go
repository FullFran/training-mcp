package training

import (
	"context"
	"fmt"
	"strings"
)

// Where a muscle group's simulated weekly sets fall against that person's
// landmarks. The boundaries are those volume_recommendation clamps to: sitting
// exactly on MEV or MRV is inside.
const (
	VolumeBelowMEV    = "below_mev"
	VolumeWithin      = "within"
	VolumeAboveMRV    = "above_mrv"
	VolumeNoLandmarks = "no_landmarks"
)

const maxSessionsPerWeek = 7

// PlanEdit is one pending change to a saved plan: set (add or replace) one
// exercise, or remove one. Exactly one of the two is used.
type PlanEdit struct {
	Set    *PlanItem
	Remove string
}

// PlanSimulationInput is what to simulate: a saved plan by id, optionally with
// one edit on top, or unsaved items. SessionsPerWeek is how often the plan is
// done in a week; zero means once.
type PlanSimulationInput struct {
	PlanID          int64
	Items           []PlanItem
	Edit            *PlanEdit
	SessionsPerWeek int
}

// GroupSimulation is one trained muscle group's planned week.
type GroupSimulation struct {
	MuscleGroup string   `json:"muscle_group"`
	WeeklySets  int      `json:"weekly_sets"`
	WeeklySI    float64  `json:"weekly_si"`
	MEV         int      `json:"mev,omitempty"`
	MRV         int      `json:"mrv,omitempty"`
	Status      string   `json:"status"`
	Exercises   []string `json:"exercises"`
}

// PlanSimulation is what a plan would do to a week of training, computed from
// the plan, the person's exercise groups and landmarks. Nothing is stored.
type PlanSimulation struct {
	PlanID          int64             `json:"plan_id,omitempty"`
	SessionsPerWeek int               `json:"sessions_per_week"`
	WeeklySets      int               `json:"weekly_sets"`
	WeeklySI        float64           `json:"weekly_si"`
	Groups          []GroupSimulation `json:"groups"`
	// Unmapped are exercises with no muscle group: their sets are in the
	// totals but in no group, so the gap is visible rather than hidden.
	Unmapped []string `json:"unmapped_exercises"`
}

// SimulatePlan computes, without writing anything, the weekly working sets and
// SI a plan prescribes per muscle group and compares each group with the
// person's MEV and MRV. Every plan item is a working set (a plan prescribes no
// warm-ups), counted and weighted exactly as logged sets are: one set each,
// with SetSI of its target RPE, or of the imputed RPE when none is prescribed.
func (s *Service) SimulatePlan(ctx context.Context, in PlanSimulationInput) (PlanSimulation, error) {
	if in.SessionsPerWeek == 0 {
		in.SessionsPerWeek = 1
	}
	if in.SessionsPerWeek < 0 || in.SessionsPerWeek > maxSessionsPerWeek {
		return PlanSimulation{}, ErrValidation
	}
	items, err := s.simulatedItems(ctx, in)
	if err != nil {
		return PlanSimulation{}, err
	}
	groups, err := s.store.ExerciseGroups(ctx)
	if err != nil {
		return PlanSimulation{}, err
	}
	groupOf := make(map[string]string, len(groups))
	for _, g := range groups {
		groupOf[g.Exercise] = g.MuscleGroup
	}
	landmarks, err := s.store.AllLandmarks(ctx)
	if err != nil {
		return PlanSimulation{}, err
	}

	out := PlanSimulation{PlanID: in.PlanID, SessionsPerWeek: in.SessionsPerWeek, Groups: []GroupSimulation{}, Unmapped: []string{}}
	byGroup := map[string]*GroupSimulation{}
	var totalSI float64
	for _, it := range items {
		rpe := it.TargetRPE
		if rpe == 0 {
			rpe = ImputedRPE
		}
		sets := it.TargetSets * in.SessionsPerWeek
		si := float64(sets) * SetSI(rpe, SetWorking)
		out.WeeklySets += sets
		totalSI += si
		group := groupOf[it.Exercise]
		if group == "" {
			out.Unmapped = append(out.Unmapped, it.Exercise)
			continue
		}
		g := byGroup[group]
		if g == nil {
			g = &GroupSimulation{MuscleGroup: group, MEV: landmarks[group].MEV, MRV: landmarks[group].MRV, Exercises: []string{}}
			byGroup[group] = g
		}
		g.WeeklySets += sets
		g.WeeklySI += si
		g.Exercises = append(g.Exercises, it.Exercise)
	}
	out.WeeklySI = NormalizeSI(totalSI)
	for _, name := range MuscleGroups {
		g := byGroup[name]
		if g == nil {
			continue
		}
		g.WeeklySI = NormalizeSI(g.WeeklySI)
		g.Status = volumeStatus(g.WeeklySets, g.MEV, g.MRV)
		out.Groups = append(out.Groups, *g)
	}
	return out, nil
}

// volumeStatus places weekly sets against landmarks; zero means unknown.
func volumeStatus(sets, mev, mrv int) string {
	switch {
	case mev > 0 && sets < mev:
		return VolumeBelowMEV
	case mrv > 0 && sets > mrv:
		return VolumeAboveMRV
	case mev == 0 && mrv == 0:
		return VolumeNoLandmarks
	default:
		return VolumeWithin
	}
}

// simulatedItems resolves the input to validated, normalized items. It works
// on copies, so neither the caller's items nor the stored plan change.
func (s *Service) simulatedItems(ctx context.Context, in PlanSimulationInput) ([]PlanItem, error) {
	switch {
	case in.PlanID < 0, in.PlanID > 0 && len(in.Items) > 0, in.PlanID == 0 && len(in.Items) == 0:
		return nil, ErrValidation
	case in.PlanID == 0 && in.Edit != nil:
		return nil, fmt.Errorf("%w: an edit needs a saved plan", ErrValidation)
	}
	var items []PlanItem
	if in.PlanID > 0 {
		plan, err := s.GetPlan(ctx, in.PlanID)
		if err != nil {
			return nil, err
		}
		items = append(items, plan.Items...)
	} else {
		// The same bound create_plan puts on a new plan.
		if len(in.Items) > 40 {
			return nil, ErrValidation
		}
		items = append(items, in.Items...)
	}
	for i := range items {
		if err := validPlanItem(&items[i]); err != nil {
			return nil, err
		}
	}
	if in.Edit != nil {
		return applyPlanEdit(items, *in.Edit)
	}
	return items, nil
}

// applyPlanEdit applies a set or remove the way the store does: setting an
// exercise already in the plan replaces it in place, a new one goes last.
func applyPlanEdit(items []PlanItem, e PlanEdit) ([]PlanItem, error) {
	if (e.Set == nil) == (strings.TrimSpace(e.Remove) == "") {
		return nil, ErrValidation
	}
	if e.Set != nil {
		it := *e.Set
		if err := validPlanItem(&it); err != nil {
			return nil, err
		}
		for i := range items {
			if items[i].Exercise == it.Exercise {
				items[i] = it
				return items, nil
			}
		}
		return append(items, it), nil
	}
	remove := strings.ToLower(strings.TrimSpace(e.Remove))
	for i := range items {
		if items[i].Exercise == remove {
			return append(items[:i], items[i+1:]...), nil
		}
	}
	return nil, fmt.Errorf("%w: %q is not in the plan", ErrValidation, remove)
}
