package training

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
)

// Microcycle review (#41, slice 3): what was planned in a microcycle next to
// what was done, as plain numbers. It reads only, and gives no verdict, no
// recommendation and no deload call: deciding is the person's.

// Bounds keep the review short enough for a chat: totals come first and the
// detail lists stop at these lengths, each with its full count beside it.
const (
	reviewMaxSessions  = 30
	reviewMaxExercises = 25
	reviewMaxExtra     = 25
	reviewMaxDropOff   = 40
	reviewMaxOffPlan   = 20
	reviewMaxUncertain = 25
)

// How the reviewed microcycle was chosen.
const (
	ReviewRequested  = "requested"
	ReviewLastClosed = "last_closed"
	ReviewCurrent    = "current"
	ReviewFirst      = "first"
)

// Rotation routine states in a review.
const (
	RoutineDone    = "done"
	RoutineSkipped = "skipped"
	RoutinePending = "pending"
)

// reviewNotes state where each number comes from, with every review.
var reviewNotes = []string{
	"Planned sets of a session that was done are that session's own copy of its routine: the microcycle modifiers it got when it started and any change made during it. For a routine of the rotation with no session they are the routine as it is today with this microcycle's modifiers applied.",
	"Done sets are working sets; warm-ups count nowhere. working_sets_done counts the exercises each session planned; any other working set in the microcycle's sessions is extra work, listed apart and counted in muscles done_sets.",
	"A routine of the rotation no session matched is skipped once the microcycle is closed or its dates passed, and pending while it is current or upcoming.",
	"Off-plan sessions are listed apart and count in no total.",
	"Effort compares the target RPE of the session's copy with the RPE logged, only on working sets that have both. An imputed RPE is a placeholder, never an observation: it is counted, not compared.",
	"This is a description of what happened. It gives no verdict and no recommendation.",
}

// ReviewInput picks the microcycle. Both are optional: without a block, the
// active one, else the most recently closed; without a microcycle, the last
// closed one of the block, else the current one, else the first.
type ReviewInput struct {
	BlockID      int64
	MicrocycleID int64
}

// MicrocycleReview is one microcycle, planned against done.
type MicrocycleReview struct {
	Block          ReviewBlock         `json:"block"`
	Microcycle     MicrocycleView      `json:"microcycle"`
	SelectedBy     string              `json:"selected_by"`
	Window         ReviewWindow        `json:"window"`
	Totals         ReviewTotals        `json:"totals"`
	Rotation       []ReviewRoutine     `json:"rotation"`
	Sessions       []ReviewSession     `json:"sessions"`
	SessionsTotal  int                 `json:"sessions_total"`
	Exercises      []ReviewExercise    `json:"exercises"`
	ExercisesTotal int                 `json:"exercises_total"`
	ExtraWork      []ReviewExtra       `json:"extra_work"`
	ExtraTotal     int                 `json:"extra_work_total"`
	Muscles        ReviewMuscles       `json:"muscles"`
	Effort         ReviewEffort        `json:"effort"`
	DropOff        []ReviewDropOff     `json:"dropoff"`
	DropOffTotal   int                 `json:"dropoff_total"`
	OffPlan        []ReviewOffPlan     `json:"off_plan_sessions"`
	OffPlanTotal   int                 `json:"off_plan_total"`
	Feedback       []ReviewFeedback    `json:"feedback"`
	Bodyweight     ReviewBodyweight    `json:"bodyweight"`
	Previous       *ReviewPrevious     `json:"previous,omitempty"`
	Notes          []string            `json:"notes"`
	missing        map[int64]string    // routines deleted since, by plan id
	exercises      map[string]*exTally // the whole per-exercise tally
}

// ReviewBlock is the block the microcycle belongs to.
type ReviewBlock struct {
	ID              int64  `json:"id"`
	Intent          string `json:"intent"`
	Advance         string `json:"advance"`
	Status          string `json:"status"`
	MicrocycleCount int    `json:"microcycle_count"`
	StartDate       string `json:"start_date,omitempty"`
}

// ReviewWindow is the dates the review reads off-plan sessions and
// bodyweight in, and how they were set.
type ReviewWindow struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Basis string `json:"basis"`
}

// ReviewTotals are the headline counts.
type ReviewTotals struct {
	RotationPlanned         int `json:"rotation_planned"`
	RotationDone            int `json:"rotation_done"`
	RotationSkipped         int `json:"rotation_skipped"`
	RotationPending         int `json:"rotation_pending"`
	Sessions                int `json:"sessions"`
	SessionsOutsideRotation int `json:"sessions_outside_rotation"`
	OffPlanSessions         int `json:"off_plan_sessions"`
	PlannedSets             int `json:"planned_sets"`
	WorkingSetsDone         int `json:"working_sets_done"`
	ExtraSets               int `json:"extra_sets"`
}

// ReviewRoutine is one routine of the microcycle's rotation.
type ReviewRoutine struct {
	PlanID    int64  `json:"plan_id,omitempty"`
	PlanName  string `json:"plan_name"`
	Status    string `json:"status"`
	SessionID int64  `json:"session_id,omitempty"`
}

// ReviewSession is one session placed in the microcycle.
type ReviewSession struct {
	SessionID   int64   `json:"session_id"`
	Date        string  `json:"date"`
	PlanID      int64   `json:"plan_id,omitempty"`
	PlanName    string  `json:"plan_name,omitempty"`
	InRotation  bool    `json:"in_rotation"`
	PlannedSets int     `json:"planned_sets"`
	WorkingSets int     `json:"working_sets"`
	ExtraSets   int     `json:"extra_sets"`
	SetsDelta   int     `json:"sets_delta,omitempty"`
	EffortDelta float64 `json:"effort_delta,omitempty"`
}

// ReviewExercise is one exercise, planned against done.
type ReviewExercise struct {
	Exercise        string `json:"exercise"`
	MuscleGroup     string `json:"muscle_group"`
	PlannedSets     int    `json:"planned_sets"`
	WorkingSetsDone int    `json:"working_sets_done"`
}

// ReviewExtra is work done outside what a session planned.
type ReviewExtra struct {
	Exercise    string `json:"exercise"`
	MuscleGroup string `json:"muscle_group"`
	WorkingSets int    `json:"working_sets"`
	Sessions    int    `json:"sessions"`
}

// ReviewMuscles is sets per muscle group in both counts.
type ReviewMuscles struct {
	SingleGroup []ReviewMuscle    `json:"single_group"`
	Fractional  *ReviewFractional `json:"fractional,omitempty"`
}

// ReviewMuscle is one muscle group's planned and done sets.
type ReviewMuscle struct {
	MuscleGroup string  `json:"muscle_group"`
	PlannedSets float64 `json:"planned_sets"`
	DoneSets    float64 `json:"done_sets"`
}

// ReviewFractional is the fractional count, with its method and source, and
// the credit of uncertain muscles kept out of every group.
type ReviewFractional struct {
	Method         string            `json:"method"`
	Source         string            `json:"source"`
	Groups         []ReviewMuscle    `json:"groups"`
	Uncertain      []ReviewUncertain `json:"uncertain"`
	UncertainTotal int               `json:"uncertain_total"`
}

// ReviewUncertain is an uncertain muscle's planned and done credit.
type ReviewUncertain struct {
	Exercise     string   `json:"exercise"`
	SourceMuscle string   `json:"source_muscle"`
	Role         string   `json:"role"`
	Candidates   []string `json:"candidates"`
	PlannedSets  float64  `json:"planned_sets"`
	DoneSets     float64  `json:"done_sets"`
}

// ReviewEffort compares target and logged RPE where both exist.
type ReviewEffort struct {
	ComparedSets      int                    `json:"compared_sets"`
	MeanTargetRPE     *float64               `json:"mean_target_rpe,omitempty"`
	MeanLoggedRPE     *float64               `json:"mean_logged_rpe,omitempty"`
	MeanDifference    *float64               `json:"mean_difference,omitempty"`
	RPEImputedSets    int                    `json:"rpe_imputed_sets"`
	RPEMissingSets    int                    `json:"rpe_missing_sets"`
	SetsWithoutTarget int                    `json:"sets_without_target"`
	Exercises         []ReviewExerciseEffort `json:"exercises"`
}

// ReviewExerciseEffort is one exercise's compared sets.
type ReviewExerciseEffort struct {
	Exercise       string  `json:"exercise"`
	ComparedSets   int     `json:"compared_sets"`
	MeanTargetRPE  float64 `json:"mean_target_rpe"`
	MeanLoggedRPE  float64 `json:"mean_logged_rpe"`
	MeanDifference float64 `json:"mean_difference"`
}

// ReviewDropOff is the drop-off read of one exercise in one session, short.
type ReviewDropOff struct {
	SessionID      int64            `json:"session_id"`
	Date           string           `json:"date"`
	Exercise       string           `json:"exercise"`
	Comparable     bool             `json:"comparable"`
	ComparableSets int              `json:"comparable_sets"`
	NotComparable  string           `json:"not_comparable,omitempty"`
	Excluded       []ReviewExcluded `json:"excluded"`
	Models         []ModelSummary   `json:"models"`
}

// ReviewExcluded counts the sets left out of a drop-off read for one reason.
type ReviewExcluded struct {
	Reason string `json:"reason"`
	Sets   int    `json:"sets"`
}

// ReviewOffPlan is an off-plan session of the block inside the window.
type ReviewOffPlan struct {
	SessionID   int64  `json:"session_id"`
	Date        string `json:"date"`
	PlanName    string `json:"plan_name,omitempty"`
	WorkingSets int    `json:"working_sets"`
}

// ReviewFeedback is one muscle group's session feedback over the microcycle.
type ReviewFeedback struct {
	MuscleGroup string  `json:"muscle_group"`
	Sessions    int     `json:"sessions"`
	Fatigue     float64 `json:"fatigue_avg"`
	Pump        float64 `json:"pump_avg"`
	Recovery    float64 `json:"recovery_avg"`
}

// ReviewBodyweight is the bodyweight entries inside the window.
type ReviewBodyweight struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Count     int      `json:"count"`
	FirstDate string   `json:"first_date,omitempty"`
	FirstKG   *float64 `json:"first_kg,omitempty"`
	LastDate  string   `json:"last_date,omitempty"`
	LastKG    *float64 `json:"last_kg,omitempty"`
	ChangeKG  *float64 `json:"change_kg,omitempty"`
}

// ReviewPrevious is the previous microcycle of the same block, in short.
type ReviewPrevious struct {
	MicrocycleID   int64          `json:"microcycle_id"`
	Position       int            `json:"position"`
	Label          string         `json:"label,omitempty"`
	State          string         `json:"state"`
	SetsDelta      int            `json:"sets_delta,omitempty"`
	EffortDelta    float64        `json:"effort_delta,omitempty"`
	Totals         ReviewTotals   `json:"totals"`
	SingleGroup    []ReviewMuscle `json:"single_group"`
	Fractional     []ReviewMuscle `json:"fractional,omitempty"`
	ComparedSets   int            `json:"compared_sets"`
	MeanDifference *float64       `json:"mean_difference,omitempty"`
	RPEImputedSets int            `json:"rpe_imputed_sets"`
}

// exTally is everything counted for one exercise.
type exTally struct {
	planned, done, extra int
	extraSessions        int
	doneSI               float64
	compared             int
	targetSum, loggedSum float64
}

// reviewContext is read once per review and shared by both microcycles.
type reviewContext struct {
	groups  map[string]string
	lib     personalLibrary
	hasLib  bool
	today   string
	block   Block
	view    BlockView
	ps      PeriodizationStore
	working map[int64][]Set // working sets per session, read once
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func ptr(v float64) *float64 { return &v }

// ReviewMicrocycle reads one microcycle planned against done. See ReviewInput
// for the defaults.
func (s *Service) ReviewMicrocycle(ctx context.Context, in ReviewInput) (MicrocycleReview, error) {
	ps, err := s.periodization()
	if err != nil {
		return MicrocycleReview{}, err
	}
	if in.BlockID < 0 || in.MicrocycleID < 0 {
		return MicrocycleReview{}, ErrValidation
	}
	b, err := s.reviewBlock(ctx, ps, in)
	if err != nil {
		return MicrocycleReview{}, err
	}
	v, err := s.view(ctx, ps, b)
	if err != nil {
		return MicrocycleReview{}, err
	}
	at, selected := -1, ""
	if in.MicrocycleID > 0 {
		for i, m := range b.Microcycles {
			if m.ID == in.MicrocycleID {
				at = i
			}
		}
		if at < 0 {
			return MicrocycleReview{}, ErrNotFound
		}
		selected = ReviewRequested
	} else {
		at, selected = defaultMicrocycle(v)
	}
	rc := &reviewContext{today: s.today(), block: b, view: v, ps: ps, working: map[int64][]Set{}}
	if rc.groups, err = s.groupMap(ctx); err != nil {
		return MicrocycleReview{}, err
	}
	if _, ok := s.store.(ExerciseLibraryStore); ok {
		if rc.lib, err = s.library(ctx); err != nil {
			return MicrocycleReview{}, err
		}
		rc.hasLib = true
	}
	out, err := s.reviewOne(ctx, rc, at)
	if err != nil {
		return MicrocycleReview{}, err
	}
	out.SelectedBy = selected
	out.Block = ReviewBlock{ID: b.ID, Intent: b.Intent, Advance: b.Advance, Status: b.Status, MicrocycleCount: len(b.Microcycles), StartDate: b.StartDate}
	out.Window = reviewWindow(rc, at)
	if err = s.reviewDetail(ctx, rc, &out); err != nil {
		return MicrocycleReview{}, err
	}
	if at > 0 {
		prev, err := s.reviewOne(ctx, rc, at-1)
		if err != nil {
			return MicrocycleReview{}, err
		}
		p := &ReviewPrevious{
			MicrocycleID: prev.Microcycle.ID, Position: prev.Microcycle.Position, Label: prev.Microcycle.Label, State: prev.Microcycle.State,
			SetsDelta: prev.Microcycle.SetsDelta, EffortDelta: prev.Microcycle.EffortDelta, Totals: prev.Totals,
			SingleGroup: prev.Muscles.SingleGroup, ComparedSets: prev.Effort.ComparedSets, MeanDifference: prev.Effort.MeanDifference,
			RPEImputedSets: prev.Effort.RPEImputedSets,
		}
		if prev.Muscles.Fractional != nil {
			p.Fractional = prev.Muscles.Fractional.Groups
		}
		out.Previous = p
	}
	out.Notes = append([]string{}, reviewNotes...)
	ids := make([]int64, 0, len(out.missing))
	for id := range out.missing {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		out.Notes = append(out.Notes, fmt.Sprintf("Routine %q (plan %d) of the rotation no longer exists, so its planned sets are not counted.", out.missing[id], id))
	}
	return out, nil
}

// reviewBlock resolves the block: the one asked for, the one of the
// microcycle asked for, the active one, or the most recently closed one.
func (s *Service) reviewBlock(ctx context.Context, ps PeriodizationStore, in ReviewInput) (Block, error) {
	switch {
	case in.BlockID > 0:
		return ps.GetBlock(ctx, in.BlockID)
	case in.MicrocycleID > 0:
		id, err := ps.BlockOfMicrocycle(ctx, in.MicrocycleID)
		if err != nil {
			return Block{}, err
		}
		return ps.GetBlock(ctx, id)
	}
	b, active, err := ps.ActiveBlock(ctx)
	if err != nil || active {
		return b, err
	}
	blocks, err := ps.Blocks(ctx)
	if err != nil {
		return Block{}, err
	}
	var best *Block
	for i := range blocks {
		c := &blocks[i]
		if c.Status != BlockDone {
			continue
		}
		if best == nil || c.EndedOn > best.EndedOn || (c.EndedOn == best.EndedOn && c.ID > best.ID) {
			best = c
		}
	}
	if best == nil {
		return Block{}, fmt.Errorf("%w: no active or closed block to review; pass block_id", ErrNotFound)
	}
	return ps.GetBlock(ctx, best.ID)
}

// defaultMicrocycle is the last closed microcycle, else the current one, else
// the first.
func defaultMicrocycle(v BlockView) (int, string) {
	for i := len(v.Microcycles) - 1; i >= 0; i-- {
		if !remaining(v.Microcycles[i].State) {
			return i, ReviewLastClosed
		}
	}
	for i, m := range v.Microcycles {
		if m.State == StateCurrent {
			return i, ReviewCurrent
		}
	}
	return 0, ReviewFirst
}

func (s *Service) groupMap(ctx context.Context) (map[string]string, error) {
	groups, err := s.store.ExerciseGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(groups))
	for _, g := range groups {
		out[g.Exercise] = g.MuscleGroup
	}
	return out, nil
}

func (s *Service) workingSets(ctx context.Context, rc *reviewContext, sessionID int64) ([]Set, error) {
	if sets, ok := rc.working[sessionID]; ok {
		return sets, nil
	}
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	sets := []Set{}
	for _, st := range sess.Sets {
		if st.Category != SetWarmup {
			sets = append(sets, st)
		}
	}
	rc.working[sessionID] = sets
	return sets, nil
}

// reviewOne counts one microcycle: rotation, sessions, exercises, muscles and
// effort.
func (s *Service) reviewOne(ctx context.Context, rc *reviewContext, at int) (MicrocycleReview, error) {
	b, mv := rc.block, rc.view.Microcycles[at]
	m := b.Microcycles[at]
	out := MicrocycleReview{
		Microcycle: mv, Rotation: []ReviewRoutine{}, Sessions: []ReviewSession{}, Exercises: []ReviewExercise{},
		ExtraWork: []ReviewExtra{}, DropOff: []ReviewDropOff{}, OffPlan: []ReviewOffPlan{}, Feedback: []ReviewFeedback{},
		missing: map[int64]string{}, exercises: map[string]*exTally{},
	}
	ex := func(name string) *exTally {
		t := out.exercises[name]
		if t == nil {
			t = &exTally{}
			out.exercises[name] = t
		}
		return t
	}
	closed := !remaining(mv.State)
	// Rotation against the sessions placed, matched by routine as due does.
	used := make([]bool, len(mv.Sessions))
	for _, r := range mv.Rotation {
		row := ReviewRoutine{PlanID: r.PlanID, PlanName: r.PlanName, Status: RoutinePending}
		for i, l := range mv.Sessions {
			if !used[i] && r.PlanID != 0 && l.PlanID == r.PlanID {
				used[i], row.Status, row.SessionID = true, RoutineDone, l.SessionID
				break
			}
		}
		if row.Status != RoutineDone && closed {
			row.Status = RoutineSkipped
		}
		switch row.Status {
		case RoutineDone:
			out.Totals.RotationDone++
		case RoutineSkipped:
			out.Totals.RotationSkipped++
		default:
			out.Totals.RotationPending++
		}
		out.Rotation = append(out.Rotation, row)
		if row.Status == RoutineDone || r.PlanID == 0 {
			continue
		}
		// A routine with no session: as it is today, with the modifiers.
		plan, err := s.store.GetPlan(ctx, r.PlanID)
		if errors.Is(err, ErrNotFound) {
			out.missing[r.PlanID] = r.PlanName
			continue
		}
		if err != nil {
			return MicrocycleReview{}, err
		}
		for _, it := range prescribe(b, m, plan.ID, plan.Name, planBase(plan)).Items {
			ex(it.Exercise).planned += it.Sets
			out.Totals.PlannedSets += it.Sets
		}
	}
	out.Totals.RotationPlanned = len(mv.Rotation)
	effort := &out.Effort
	for i, l := range mv.Sessions {
		progress, err := s.store.SessionProgress(ctx, l.SessionID)
		if err != nil {
			return MicrocycleReview{}, err
		}
		sets, err := s.workingSets(ctx, rc, l.SessionID)
		if err != nil {
			return MicrocycleReview{}, err
		}
		row := ReviewSession{SessionID: l.SessionID, Date: l.Date, PlanID: l.PlanID, PlanName: l.PlanName, InRotation: used[i]}
		if p, err := s.SessionPrescription(ctx, l.SessionID); err == nil && p != nil {
			row.SetsDelta, row.EffortDelta = p.SetsDelta, p.EffortDelta
		}
		planned := map[string]PlanProgress{}
		for _, it := range progress {
			if it.TargetSets > 0 {
				planned[it.Exercise] = it
				ex(it.Exercise).planned += it.TargetSets
				row.PlannedSets += it.TargetSets
			}
		}
		extraHere := map[string]bool{}
		for _, st := range sets {
			t := ex(st.Exercise)
			t.doneSI += st.SI
			row.WorkingSets++
			if st.RPEImputed {
				effort.RPEImputedSets++
			} else if st.RPE <= 0 {
				effort.RPEMissingSets++
			}
			it, ok := planned[st.Exercise]
			if !ok {
				t.extra++
				row.ExtraSets++
				if !extraHere[st.Exercise] {
					extraHere[st.Exercise] = true
					t.extraSessions++
				}
				effort.SetsWithoutTarget++
				continue
			}
			t.done++
			if it.TargetRPE <= 0 {
				effort.SetsWithoutTarget++
				continue
			}
			if st.RPEImputed || st.RPE <= 0 {
				continue
			}
			t.compared++
			t.targetSum += it.TargetRPE
			t.loggedSum += st.RPE
		}
		out.Totals.PlannedSets += row.PlannedSets
		out.Totals.WorkingSetsDone += row.WorkingSets - row.ExtraSets
		out.Totals.ExtraSets += row.ExtraSets
		if !used[i] {
			out.Totals.SessionsOutsideRotation++
		}
		if len(out.Sessions) < reviewMaxSessions {
			out.Sessions = append(out.Sessions, row)
		}
	}
	out.Totals.Sessions, out.SessionsTotal = len(mv.Sessions), len(mv.Sessions)
	// Per exercise, planned and done, then extra work.
	names := make([]string, 0, len(out.exercises))
	for name := range out.exercises {
		names = append(names, name)
	}
	sort.Strings(names)
	var targetSum, loggedSum float64
	for _, name := range names {
		t := out.exercises[name]
		if t.planned > 0 || t.done > 0 {
			out.Exercises = append(out.Exercises, ReviewExercise{Exercise: name, MuscleGroup: rc.groups[name], PlannedSets: t.planned, WorkingSetsDone: t.done})
		}
		if t.extra > 0 {
			out.ExtraWork = append(out.ExtraWork, ReviewExtra{Exercise: name, MuscleGroup: rc.groups[name], WorkingSets: t.extra, Sessions: t.extraSessions})
		}
		if t.compared > 0 {
			effort.ComparedSets += t.compared
			targetSum += t.targetSum
			loggedSum += t.loggedSum
			n := float64(t.compared)
			effort.Exercises = append(effort.Exercises, ReviewExerciseEffort{Exercise: name, ComparedSets: t.compared,
				MeanTargetRPE: round2(t.targetSum / n), MeanLoggedRPE: round2(t.loggedSum / n), MeanDifference: round2((t.loggedSum - t.targetSum) / n)})
		}
	}
	sort.SliceStable(out.Exercises, func(i, j int) bool {
		a, b := out.Exercises[i], out.Exercises[j]
		if a.PlannedSets != b.PlannedSets {
			return a.PlannedSets > b.PlannedSets
		}
		return a.WorkingSetsDone > b.WorkingSetsDone
	})
	sort.SliceStable(out.ExtraWork, func(i, j int) bool { return out.ExtraWork[i].WorkingSets > out.ExtraWork[j].WorkingSets })
	sort.SliceStable(effort.Exercises, func(i, j int) bool { return effort.Exercises[i].ComparedSets > effort.Exercises[j].ComparedSets })
	out.ExercisesTotal, out.ExtraTotal = len(out.Exercises), len(out.ExtraWork)
	out.Exercises = out.Exercises[:min(len(out.Exercises), reviewMaxExercises)]
	out.ExtraWork = out.ExtraWork[:min(len(out.ExtraWork), reviewMaxExtra)]
	effort.Exercises = effort.Exercises[:min(len(effort.Exercises), reviewMaxExercises)]
	if effort.Exercises == nil {
		effort.Exercises = []ReviewExerciseEffort{}
	}
	if effort.ComparedSets > 0 {
		n := float64(effort.ComparedSets)
		effort.MeanTargetRPE, effort.MeanLoggedRPE = ptr(round2(targetSum/n)), ptr(round2(loggedSum/n))
		effort.MeanDifference = ptr(round2((loggedSum - targetSum) / n))
	}
	out.Muscles = reviewMuscles(rc, names, out.exercises)
	return out, nil
}

// reviewMuscles is sets per muscle group: the single-group count from each
// exercise's group, and the fractional count from the library.
func reviewMuscles(rc *reviewContext, names []string, tallies map[string]*exTally) ReviewMuscles {
	single := map[string]*ReviewMuscle{}
	for _, name := range names {
		t := tallies[name]
		g := rc.groups[name]
		row := single[g]
		if row == nil {
			row = &ReviewMuscle{MuscleGroup: g}
			single[g] = row
		}
		row.PlannedSets += float64(t.planned)
		row.DoneSets += float64(t.done + t.extra)
	}
	out := ReviewMuscles{SingleGroup: sortedMuscles(single)}
	if !rc.hasLib {
		return out
	}
	planned, done := newTally(), newTally()
	for _, name := range names {
		t := tallies[name]
		if t.planned > 0 {
			planned.add(rc.lib, ExerciseVolume{Exercise: name, MuscleGroup: rc.groups[name], Sets: t.planned})
		}
		if t.done+t.extra > 0 {
			done.add(rc.lib, ExerciseVolume{Exercise: name, MuscleGroup: rc.groups[name], Sets: t.done + t.extra, TotalSI: t.doneSI})
		}
	}
	pg, pu := planned.result()
	dg, du := done.result()
	groups := map[string]*ReviewMuscle{}
	for _, g := range pg {
		groups[g.MuscleGroup] = &ReviewMuscle{MuscleGroup: g.MuscleGroup, PlannedSets: g.Sets}
	}
	for _, g := range dg {
		row := groups[g.MuscleGroup]
		if row == nil {
			row = &ReviewMuscle{MuscleGroup: g.MuscleGroup}
			groups[g.MuscleGroup] = row
		}
		row.DoneSets = g.Sets
	}
	uncertain := map[[3]string]*ReviewUncertain{}
	var keys [][3]string
	for _, list := range [][]UncertainCredit{pu, du} {
		for _, u := range list {
			k := [3]string{u.Exercise, u.Role, u.SourceMuscle}
			if uncertain[k] == nil {
				uncertain[k] = &ReviewUncertain{Exercise: u.Exercise, SourceMuscle: u.SourceMuscle, Role: u.Role, Candidates: u.Candidates}
				keys = append(keys, k)
			}
		}
	}
	for _, u := range pu {
		uncertain[[3]string{u.Exercise, u.Role, u.SourceMuscle}].PlannedSets = u.Sets
	}
	for _, u := range du {
		uncertain[[3]string{u.Exercise, u.Role, u.SourceMuscle}].DoneSets = u.Sets
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		if a[1] != b[1] {
			return a[1] == RolePrimary
		}
		return a[2] < b[2]
	})
	f := &ReviewFractional{Method: FractionalMethod, Source: FractionalSource, Groups: sortedMuscles(groups), Uncertain: []ReviewUncertain{}, UncertainTotal: len(keys)}
	for _, k := range keys[:min(len(keys), reviewMaxUncertain)] {
		f.Uncertain = append(f.Uncertain, *uncertain[k])
	}
	out.Fractional = f
	return out
}

func sortedMuscles(m map[string]*ReviewMuscle) []ReviewMuscle {
	out := make([]ReviewMuscle, 0, len(m))
	for _, r := range m {
		out = append(out, ReviewMuscle{MuscleGroup: r.MuscleGroup, PlannedSets: round1(r.PlannedSets), DoneSets: round1(r.DoneSets)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.PlannedSets != b.PlannedSets {
			return a.PlannedSets > b.PlannedSets
		}
		if a.DoneSets != b.DoneSets {
			return a.DoneSets > b.DoneSets
		}
		return a.MuscleGroup < b.MuscleGroup
	})
	return out
}

// reviewWindow is the microcycle's dates. Calendar: its own. Sequence: from
// the previous microcycle's close (or the block's start) to its own close (or
// today). Either way it widens to cover every session placed in it.
func reviewWindow(rc *reviewContext, at int) ReviewWindow {
	b, mv := rc.block, rc.view.Microcycles[at]
	var w ReviewWindow
	switch {
	case mv.StartDate != "":
		w = ReviewWindow{From: mv.StartDate, To: mv.EndDate, Basis: "the microcycle's calendar dates"}
	default:
		w.From, w.To = b.StartDate, mv.ClosedOn
		w.Basis = "from the block's start"
		if at > 0 && b.Microcycles[at-1].ClosedOn != "" {
			w.From, w.Basis = addDays(b.Microcycles[at-1].ClosedOn, 1), "from the day after the previous microcycle's close"
		}
		if w.To == "" {
			w.To, w.Basis = rc.today, w.Basis+" to today"
		} else {
			w.Basis += " to this microcycle's close"
		}
	}
	for _, l := range mv.Sessions {
		if w.From == "" || l.Date < w.From {
			w.From = l.Date
		}
		if l.Date > w.To {
			w.To = l.Date
		}
	}
	if w.From == "" || w.From > w.To {
		w.From = w.To
	}
	if w.To != "" && daysBetween(w.From, w.To) >= BodyweightMaxDays {
		w.From = addDays(w.To, 1-BodyweightMaxDays)
	}
	if len(mv.Sessions) > 0 {
		w.Basis += ", widened to every session placed in it"
	}
	return w
}

// reviewDetail adds what only the reviewed microcycle shows: drop-off,
// off-plan sessions, feedback and bodyweight.
func (s *Service) reviewDetail(ctx context.Context, rc *reviewContext, out *MicrocycleReview) error {
	feedback := map[string]*struct {
		n                int
		fat, pump, recov float64
	}{}
	for _, l := range out.Microcycle.Sessions {
		analyses, err := s.PerformanceDropOff(ctx, l.SessionID, "")
		if err != nil {
			return err
		}
		for _, a := range analyses {
			out.DropOffTotal++
			if len(out.DropOff) >= reviewMaxDropOff {
				continue
			}
			out.DropOff = append(out.DropOff, compactDropOff(l, a))
		}
		fb, err := s.store.SessionFeedback(ctx, l.SessionID)
		if err != nil {
			return err
		}
		for _, f := range fb {
			agg := feedback[f.MuscleGroup]
			if agg == nil {
				agg = &struct {
					n                int
					fat, pump, recov float64
				}{}
				feedback[f.MuscleGroup] = agg
			}
			agg.n++
			agg.fat += float64(f.Fatigue)
			agg.pump += float64(f.Pump)
			agg.recov += float64(f.Recovery)
		}
	}
	groups := make([]string, 0, len(feedback))
	for g := range feedback {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		a := feedback[g]
		n := float64(a.n)
		out.Feedback = append(out.Feedback, ReviewFeedback{MuscleGroup: g, Sessions: a.n, Fatigue: round2(a.fat / n), Pump: round2(a.pump / n), Recovery: round2(a.recov / n)})
	}
	for _, l := range rc.view.OffPlan {
		if l.Date < out.Window.From || l.Date > out.Window.To {
			continue
		}
		out.Totals.OffPlanSessions++
		if len(out.OffPlan) >= reviewMaxOffPlan {
			continue
		}
		sets, err := s.workingSets(ctx, rc, l.SessionID)
		if err != nil {
			return err
		}
		out.OffPlan = append(out.OffPlan, ReviewOffPlan{SessionID: l.SessionID, Date: l.Date, PlanName: l.PlanName, WorkingSets: len(sets)})
	}
	out.OffPlanTotal = out.Totals.OffPlanSessions
	out.Bodyweight = ReviewBodyweight{From: out.Window.From, To: out.Window.To}
	if out.Window.To == "" {
		return nil
	}
	h, err := s.BodyweightHistory(ctx, out.Window.From, out.Window.To)
	if err != nil {
		return err
	}
	if h.Summary != nil {
		sum := h.Summary
		out.Bodyweight.Count, out.Bodyweight.FirstDate, out.Bodyweight.LastDate = sum.Count, sum.First.Date, sum.Last.Date
		out.Bodyweight.FirstKG, out.Bodyweight.LastKG, out.Bodyweight.ChangeKG = ptr(sum.First.WeightKG), ptr(sum.Last.WeightKG), ptr(sum.ChangeKG)
	}
	return nil
}

func compactDropOff(l SessionLink, a DropOffAnalysis) ReviewDropOff {
	d := ReviewDropOff{SessionID: l.SessionID, Date: l.Date, Exercise: a.Exercise, Comparable: a.Comparable, Excluded: []ReviewExcluded{}, Models: []ModelSummary{}}
	excluded := map[string]int{}
	var reasons []string
	for _, st := range a.Sets {
		if st.Comparable {
			d.ComparableSets++
			continue
		}
		if excluded[st.ExcludedReason] == 0 {
			reasons = append(reasons, st.ExcludedReason)
		}
		excluded[st.ExcludedReason]++
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		d.Excluded = append(d.Excluded, ReviewExcluded{Reason: r, Sets: excluded[r]})
	}
	if !a.Comparable {
		d.NotComparable = fmt.Sprintf("fewer than two comparable sets (%d logged, %d excluded)", len(a.Sets), len(a.Sets)-d.ComparableSets)
		return d
	}
	d.Models = append(d.Models, a.Models...)
	return d
}
