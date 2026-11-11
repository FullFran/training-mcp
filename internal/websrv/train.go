package websrv

import (
	"context"
	"strconv"

	"github.com/fullfran/training-mcp/internal/training"
)

// previousLimit bounds how far back an exercise's history is read to find its
// last session, enough for any realistic number of sets in one session.
const previousLimit = 40

// TrainCard is one exercise on the training screen: what the plan asks, the sets
// done today and the rows still to log, each pre-filled.
type TrainCard struct {
	training.PlanProgress
	// Logged are today's sets of the exercise, in the order logged.
	Logged []DoneRow
	// Pending are the rows still to log; the first is the live one.
	Pending []PendingRow
	// Extra is the row «+ Añadir serie» adds beyond them.
	Extra PendingRow
	// Open marks the one card shown expanded.
	Open bool
	// Planned is false for an exercise done today outside any plan.
	Planned bool
}

// DoneRow is a logged set. Warm-ups show a W and take no number, so working
// sets are numbered 1, 2, 3 and compared with last session's working sets.
type DoneRow struct {
	training.Set
	N        int
	Warmup   bool
	Previous string
}

// PendingRow is a set still to log, with the values the check would store.
type PendingRow struct {
	N        int
	Previous string
	Weight   string
	Reps     int
	RPE      string
}

// trainCards lists today's exercises as cards: the plan's in order, then any
// done outside it. A row's defaults come from the plan, else last session's
// set at that position, else the set before it; the weight, which no plan
// prescribes, is today's latest, else last session's.
func (s *Server) trainCards(ctx context.Context, data pageData) ([]TrainCard, error) {
	var cards []TrainCard
	seen := map[string]bool{}
	for _, p := range data.Progress {
		seen[p.Exercise] = true
		cards = append(cards, TrainCard{PlanProgress: p, Planned: p.TargetSets > 0})
	}
	for _, b := range data.Blocks {
		if !seen[b.Exercise] {
			seen[b.Exercise] = true
			cards = append(cards, TrainCard{PlanProgress: training.PlanProgress{Exercise: b.Exercise, DoneSets: workingCount(b.Sets)}})
		}
	}
	for i := range cards {
		c := &cards[i]
		last, err := s.lastSession(ctx, c.Exercise, data.Session.ID)
		if err != nil {
			return nil, err
		}
		var working []training.Set
		for _, set := range data.Session.Sets {
			if set.Exercise != c.Exercise {
				continue
			}
			row := DoneRow{Set: set, Warmup: set.Category == training.SetWarmup}
			if !row.Warmup {
				row.N = len(working) + 1
				row.Previous = previousAt(last, len(working))
				working = append(working, set)
			}
			c.Logged = append(c.Logged, row)
		}
		rows := 0
		switch {
		case c.Skipped:
		case c.TargetSets > 0:
			rows = max(c.TargetSets-c.DoneSets, 0)
		default:
			rows = 1
		}
		for j := 0; j < rows; j++ {
			c.Pending = append(c.Pending, pendingRow(*c, working, last, len(working)+j))
		}
		c.Extra = pendingRow(*c, working, last, len(working)+rows)
	}
	open := -1
	for i, c := range cards {
		if data.Next != nil && c.Exercise == data.Next.Exercise {
			open = i
			break
		}
	}
	if open < 0 {
		// No plan exercise left: the most recent one done, to add another set.
		for i, c := range cards {
			if len(c.Pending) > 0 {
				open = i
			}
		}
	}
	if open >= 0 {
		cards[open].Open = true
	}
	return cards, nil
}

// workingCount counts the sets that are not warm-ups.
func workingCount(sets []training.Set) int {
	n := 0
	for _, set := range sets {
		if set.Category != training.SetWarmup {
			n++
		}
	}
	return n
}

// lastSession returns an exercise's working sets from its latest session
// before today's, in the order they were done; warm-ups are left out.
func (s *Server) lastSession(ctx context.Context, exercise string, today int64) ([]training.ExerciseSet, error) {
	history, err := s.service(ctx).ExerciseHistory(ctx, exercise, previousLimit)
	if err != nil {
		return nil, err
	}
	var out []training.ExerciseSet
	session := int64(0)
	for _, set := range history.Sets { // newest first
		if set.SessionID == today {
			continue
		}
		if session == 0 {
			session = set.SessionID
		}
		if set.SessionID != session {
			break
		}
		if set.Category == training.SetWarmup {
			continue
		}
		out = append([]training.ExerciseSet{set}, out...)
	}
	return out, nil
}

func previousAt(last []training.ExerciseSet, i int) string {
	if i >= len(last) {
		return ""
	}
	return formatNumber(last[i].WeightKG) + " × " + strconv.Itoa(last[i].Reps)
}

// pendingRow pre-fills the working set at position i; working are today's
// working sets, so a warm-up never sets the next weight.
func pendingRow(c TrainCard, working []training.Set, last []training.ExerciseSet, i int) PendingRow {
	row := PendingRow{N: i + 1, Previous: previousAt(last, i), RPE: "8"}
	var before *training.Set
	if len(working) > 0 {
		before = &working[len(working)-1]
	}
	var lastSame, lastEnd *training.ExerciseSet
	if i < len(last) {
		lastSame = &last[i]
	}
	if len(last) > 0 {
		lastEnd = &last[len(last)-1]
	}
	switch {
	case before != nil:
		row.Weight = formatNumber(before.WeightKG)
	case lastSame != nil:
		row.Weight = formatNumber(lastSame.WeightKG)
	case lastEnd != nil:
		row.Weight = formatNumber(lastEnd.WeightKG)
	}
	switch {
	case c.RepMin > 0 && c.RepMax > 0:
		row.Reps = (c.RepMin + c.RepMax) / 2
	case c.RepMin > 0:
		row.Reps = c.RepMin
	case lastSame != nil:
		row.Reps = lastSame.Reps
	case before != nil:
		row.Reps = before.Reps
	default:
		row.Reps = 8
	}
	switch {
	case c.TargetRPE > 0:
		row.RPE = formatNumber(c.TargetRPE)
	case lastSame != nil && !lastSame.RPEImputed:
		row.RPE = formatNumber(lastSame.RPE)
	case before != nil && !before.RPEImputed:
		row.RPE = formatNumber(before.RPE)
	}
	return row
}

// setRowData is what the set-row template needs for one row.
type setRowData struct {
	Base     string
	Exercise string
	Row      PendingRow
}
