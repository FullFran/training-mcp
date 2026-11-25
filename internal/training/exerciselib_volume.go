package training

import (
	"context"
	"math"
	"sort"
)

// Fractional volume. Beside the per-group count, where every set counts once
// toward its exercise's single group, each working set credits every primary
// muscle of its exercise's library entry 1 and every secondary muscle 0.5.

// FractionalMethod and FractionalSource are stated in every fractional view.
const (
	FractionalMethod = "Each working set credits 1 to each primary muscle group and 0.5 to each secondary muscle group of its exercise's library entry (the person's alias, own entry, curated entry, then the public one). total_si is the set's SI times the same credit, an extension of this server: the source weighs sets, not SI. An exercise with no library entry credits 1 to its single exercise group (empty muscle_group when it has none), exactly as the per-group count does. Uncertain muscles credit no group; they are listed under uncertain."
	FractionalSource = "Pelland et al., Sports Medicine 2025, doi:10.1007/s40279-025-02344-w: a direct set counts 1 and an indirect set 0.5, the counting method with the strongest relative evidence in its meta-regressions."
)

// FractionalGroup is the fractional credit of one muscle group.
type FractionalGroup struct {
	MuscleGroup string  `json:"muscle_group"`
	Sets        float64 `json:"sets"`
	TotalSI     float64 `json:"total_si"`
}

// UncertainCredit is credit an uncertain muscle would carry, kept out of
// every group until the entry is corrected.
type UncertainCredit struct {
	Exercise     string   `json:"exercise"`
	Entry        string   `json:"entry"`
	Layer        string   `json:"layer"`
	SourceMuscle string   `json:"source_muscle"`
	Role         string   `json:"role"`
	Candidates   []string `json:"candidates"`
	Sets         float64  `json:"sets"`
	TotalSI      float64  `json:"total_si"`
}

// FractionalWeek is one training week's fractional view.
type FractionalWeek struct {
	WeekStart string            `json:"week_start"`
	Groups    []FractionalGroup `json:"groups"`
	Uncertain []UncertainCredit `json:"uncertain"`
}

// FractionalWeekly is the fractional view beside weekly_volume.
type FractionalWeekly struct {
	Method string           `json:"method"`
	Source string           `json:"source"`
	Weeks  []FractionalWeek `json:"weeks"`
}

// FractionalTotal is the fractional view beside volume_by_muscle.
type FractionalTotal struct {
	Method    string            `json:"method"`
	Source    string            `json:"source"`
	Groups    []FractionalGroup `json:"groups"`
	Uncertain []UncertainCredit `json:"uncertain"`
}

// fractionalTally adds up one bucket's credits.
type fractionalTally struct {
	groups    map[string]*FractionalGroup
	uncertain map[[3]string]*UncertainCredit
}

func newTally() *fractionalTally {
	return &fractionalTally{groups: map[string]*FractionalGroup{}, uncertain: map[[3]string]*UncertainCredit{}}
}

func (t *fractionalTally) add(lib personalLibrary, row ExerciseVolume) {
	credit := func(group string, c float64) {
		g := t.groups[group]
		if g == nil {
			g = &FractionalGroup{MuscleGroup: group}
			t.groups[group] = g
		}
		g.Sets += c * float64(row.Sets)
		g.TotalSI += c * row.TotalSI
	}
	found := lib.resolve(row.Exercise)
	if !found.Found {
		credit(row.MuscleGroup, PrimaryCredit)
		return
	}
	for _, m := range found.Entry.Muscles {
		if !m.Uncertain {
			credit(m.MuscleGroup, m.Credit)
			continue
		}
		key := [3]string{row.Exercise, m.SourceMuscle, m.Role}
		u := t.uncertain[key]
		if u == nil {
			u = &UncertainCredit{Exercise: row.Exercise, Entry: found.Entry.Name, Layer: found.Entry.Layer, SourceMuscle: m.SourceMuscle, Role: m.Role, Candidates: append([]string{}, m.Candidates...)}
			t.uncertain[key] = u
		}
		u.Sets += m.Credit * float64(row.Sets)
		u.TotalSI += m.Credit * row.TotalSI
	}
}

// roundCredit keeps credits to one decimal, as SI is kept.
func roundCredit(v float64) float64 { return math.Round(v*10) / 10 }

func (t *fractionalTally) result() ([]FractionalGroup, []UncertainCredit) {
	groups := make([]FractionalGroup, 0, len(t.groups))
	for _, g := range t.groups {
		groups = append(groups, FractionalGroup{MuscleGroup: g.MuscleGroup, Sets: roundCredit(g.Sets), TotalSI: roundCredit(g.TotalSI)})
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Sets != b.Sets {
			return a.Sets > b.Sets
		}
		if a.TotalSI != b.TotalSI {
			return a.TotalSI > b.TotalSI
		}
		return a.MuscleGroup < b.MuscleGroup
	})
	uncertain := make([]UncertainCredit, 0, len(t.uncertain))
	for _, u := range t.uncertain {
		v := *u
		v.Sets, v.TotalSI = roundCredit(v.Sets), roundCredit(v.TotalSI)
		uncertain = append(uncertain, v)
	}
	sort.Slice(uncertain, func(i, j int) bool {
		a, b := uncertain[i], uncertain[j]
		if a.Exercise != b.Exercise {
			return a.Exercise < b.Exercise
		}
		if a.Role != b.Role {
			return a.Role == RolePrimary
		}
		return a.SourceMuscle < b.SourceMuscle
	})
	return groups, uncertain
}

// exerciseVolume reads the per-exercise rows, or reports that the store keeps
// no library.
func (s *Service) exerciseVolume(ctx context.Context, f ListFilter) ([]ExerciseVolume, personalLibrary, bool, error) {
	ls, ok := s.store.(ExerciseLibraryStore)
	if !ok {
		return nil, personalLibrary{}, false, nil
	}
	lib, err := s.library(ctx)
	if err != nil {
		return nil, lib, true, err
	}
	rows, err := ls.ExerciseVolume(ctx, f)
	return rows, lib, true, err
}

// FractionalWeeklyVolume is the fractional view per training week, newest
// first, over the same sets and range as WeeklyVolume. It is nil when the store
// keeps no library. The range is validated by WeeklyVolume, called first.
func (s *Service) FractionalWeeklyVolume(ctx context.Context, f ListFilter) (*FractionalWeekly, error) {
	rows, lib, ok, err := s.exerciseVolume(ctx, f)
	if !ok || err != nil {
		return nil, err
	}
	tallies := map[string]*fractionalTally{}
	var weeks []string
	for _, r := range rows {
		t := tallies[r.WeekStart]
		if t == nil {
			t = newTally()
			tallies[r.WeekStart] = t
			weeks = append(weeks, r.WeekStart)
		}
		t.add(lib, r)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(weeks)))
	out := &FractionalWeekly{Method: FractionalMethod, Source: FractionalSource, Weeks: make([]FractionalWeek, 0, len(weeks))}
	for _, w := range weeks {
		groups, uncertain := tallies[w].result()
		out.Weeks = append(out.Weeks, FractionalWeek{WeekStart: w, Groups: groups, Uncertain: uncertain})
	}
	return out, nil
}

// FractionalVolume is the fractional view over the same sets and range as
// VolumeByGroup; nil when the store keeps no library.
func (s *Service) FractionalVolume(ctx context.Context, f ListFilter) (*FractionalTotal, error) {
	rows, lib, ok, err := s.exerciseVolume(ctx, f)
	if !ok || err != nil {
		return nil, err
	}
	t := newTally()
	for _, r := range rows {
		t.add(lib, r)
	}
	groups, uncertain := t.result()
	return &FractionalTotal{Method: FractionalMethod, Source: FractionalSource, Groups: groups, Uncertain: uncertain}, nil
}
