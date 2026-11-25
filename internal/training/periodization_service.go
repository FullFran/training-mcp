package training

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"
	"time"
)

// errNoPeriodization is what the periodization use cases answer on a store
// that does not keep blocks. Production stores always do.
var errNoPeriodization = errors.New("periodization is not available on this store")

func (s *Service) periodization() (PeriodizationStore, error) {
	ps, ok := s.store.(PeriodizationStore)
	if !ok {
		return nil, errNoPeriodization
	}
	return ps, nil
}

func (s *Service) today() string { return s.clock().Format("2006-01-02") }

// BlockInput creates a block (ID 0) or edits one. Nil fields stay as they
// are; a create needs only Intent and MicrocycleCount.
type BlockInput struct {
	ID int64
	// GoalID links the block to a goal; 0 unlinks it.
	GoalID         *int64
	Intent         *string
	Advance        *string
	MicrocycleDays *int
	// MicrocycleCount grows the block with pending microcycles at its end or
	// shrinks it by dropping remaining ones that hold no session.
	MicrocycleCount *int
	// Status is sketch or planned; activating and closing are AdvanceBlock's.
	Status *string
	// Rotation is the block's routines, in order (plan ids).
	Rotation    *[]int64
	Microcycles []MicrocycleInput
}

// MicrocycleInput edits one remaining microcycle, found by position.
type MicrocycleInput struct {
	Position int
	Label    *string
	// SetsDelta and EffortDelta are stored modifiers; 0 clears them.
	SetsDelta   *int
	EffortDelta *float64
	// Rotation gives the microcycle routines of its own (an empty list: none
	// planned); InheritRotation drops them so it follows the block again.
	Rotation        *[]int64
	InheritRotation bool
}

// AdvanceInput moves a block by hand: activate it, complete or skip its
// current microcycle (sequence only), or close it.
type AdvanceInput struct {
	BlockID int64
	Action  string
	// StartDate is the first day of an activated block; empty means today.
	StartDate string
}

// AssignInput places a session in a microcycle of any block, or off-plan.
type AssignInput struct {
	SessionID    int64
	MicrocycleID int64
	OffPlan      bool
}

// MicrocycleView is a microcycle as read on a day: its state, its dates when
// the block runs by calendar, the routines it rotates (its own or the block's)
// and the sessions placed in it.
type MicrocycleView struct {
	ID          int64   `json:"id"`
	Position    int     `json:"position"`
	Label       string  `json:"label,omitempty"`
	SetsDelta   int     `json:"sets_delta,omitempty"`
	EffortDelta float64 `json:"effort_delta,omitempty"`
	// OwnRotation tells whether Rotation is the microcycle's own or the block's.
	OwnRotation bool            `json:"own_rotation"`
	Rotation    []RotationEntry `json:"rotation"`
	Status      string          `json:"status"`
	State       string          `json:"state"`
	ClosedOn    string          `json:"closed_on,omitempty"`
	StartDate   string          `json:"start_date,omitempty"`
	EndDate     string          `json:"end_date,omitempty"`
	Sessions    []SessionLink   `json:"sessions"`
	// SessionsDone counts the sessions placed; SessionsPlanned is the rotation's
	// length; Due lists the routines of the rotation no session matched yet.
	SessionsDone    int             `json:"sessions_done"`
	SessionsPlanned int             `json:"sessions_planned"`
	Due             []RotationEntry `json:"due"`
}

// BlockView is a block with every microcycle and the sessions that fell
// outside them.
type BlockView struct {
	ID              int64            `json:"id"`
	GoalID          int64            `json:"goal_id,omitempty"`
	Intent          string           `json:"intent"`
	Advance         string           `json:"advance"`
	MicrocycleDays  int              `json:"microcycle_days"`
	MicrocycleCount int              `json:"microcycle_count"`
	Status          string           `json:"status"`
	StartDate       string           `json:"start_date,omitempty"`
	EndDate         string           `json:"end_date,omitempty"`
	EndedOn         string           `json:"ended_on,omitempty"`
	Rotation        []RotationEntry  `json:"rotation"`
	Microcycles     []MicrocycleView `json:"microcycles"`
	OffPlan         []SessionLink    `json:"off_plan_sessions"`
}

// BlockSummary is a block in a list.
type BlockSummary struct {
	ID                   int64  `json:"id"`
	GoalID               int64  `json:"goal_id,omitempty"`
	Intent               string `json:"intent"`
	Advance              string `json:"advance"`
	MicrocycleDays       int    `json:"microcycle_days"`
	MicrocycleCount      int    `json:"microcycle_count"`
	Status               string `json:"status"`
	StartDate            string `json:"start_date,omitempty"`
	EndDate              string `json:"end_date,omitempty"`
	EndedOn              string `json:"ended_on,omitempty"`
	MicrocyclesDone      int    `json:"microcycles_done"`
	MicrocyclesSkipped   int    `json:"microcycles_skipped"`
	MicrocyclesRemaining int    `json:"microcycles_remaining"`
	// CurrentPosition is the microcycle the next session counts in; 0 when none.
	CurrentPosition int `json:"current_position,omitempty"`
	OffPlanSessions int `json:"off_plan_sessions"`
}

// BlockList is every goal and block of the account.
type BlockList struct {
	Goals  []Goal         `json:"goals"`
	Blocks []BlockSummary `json:"blocks"`
}

// TrainingStatus is where the person stands today: active goals, the active
// block and its current microcycle. Modifiers are shown, never applied.
type TrainingStatus struct {
	Today   string          `json:"today"`
	Goals   []Goal          `json:"goals"`
	Block   *BlockSummary   `json:"block,omitempty"`
	Current *MicrocycleView `json:"current_microcycle,omitempty"`
}

// --- dates -------------------------------------------------------------------

func parseDay(v string) time.Time {
	d, _ := time.Parse("2006-01-02", v)
	return d
}

func addDays(v string, n int) string { return parseDay(v).AddDate(0, 0, n).Format("2006-01-02") }

func daysBetween(from, to string) int {
	return int(math.Round(parseDay(to).Sub(parseDay(from)).Hours() / 24))
}

// calendarPosition is the microcycle position whose dates cover day: 0 before
// the start, past the count after the end.
func calendarPosition(b Block, day string) int {
	if b.StartDate == "" || day < b.StartDate {
		return 0
	}
	return daysBetween(b.StartDate, day)/b.MicrocycleDays + 1
}

// --- states ------------------------------------------------------------------

// microcycleStates reads every microcycle's state on day.
func microcycleStates(b Block, day string) []string {
	out := make([]string, len(b.Microcycles))
	current := false
	pos := calendarPosition(b, day)
	for i, m := range b.Microcycles {
		switch {
		case m.Status == MicrocycleDone:
			out[i] = StateDone
		case m.Status == MicrocycleSkipped:
			out[i] = StateSkipped
		case b.Status == BlockDone:
			out[i] = StateElapsed
		case b.Status != BlockActive:
			out[i] = StateUpcoming
		case b.Advance == AdvanceCalendar && pos > 0 && m.Position < pos:
			out[i] = StateElapsed
		case b.Advance == AdvanceCalendar && m.Position == pos:
			out[i] = StateCurrent
		case b.Advance == AdvanceCalendar:
			out[i] = StateUpcoming
		case !current:
			out[i], current = StateCurrent, true
		default:
			out[i] = StateUpcoming
		}
	}
	return out
}

func remaining(state string) bool { return state == StateCurrent || state == StateUpcoming }

func effectiveRotation(b Block, m Microcycle) []RotationEntry {
	if m.OwnRotation {
		return m.Rotation
	}
	return b.Rotation
}

// due lists the routines of a rotation that no placed session matched, at
// most as many as are still to do.
func due(rotation []RotationEntry, sessions []SessionLink) []RotationEntry {
	out := []RotationEntry{}
	left := len(rotation) - len(sessions)
	if left <= 0 {
		return out
	}
	used := make([]bool, len(sessions))
	for _, r := range rotation {
		matched := false
		for i, s := range sessions {
			if !used[i] && r.PlanID != 0 && s.PlanID == r.PlanID {
				used[i], matched = true, true
				break
			}
		}
		if !matched && len(out) < left {
			out = append(out, r)
		}
	}
	return out
}

func (s *Service) view(ctx context.Context, ps PeriodizationStore, b Block) (BlockView, error) {
	sessions, err := ps.BlockSessions(ctx, b.ID)
	if err != nil {
		return BlockView{}, err
	}
	byMicrocycle := map[int64][]SessionLink{}
	v := BlockView{
		ID: b.ID, GoalID: b.GoalID, Intent: b.Intent, Advance: b.Advance, MicrocycleDays: b.MicrocycleDays,
		MicrocycleCount: len(b.Microcycles), Status: b.Status, StartDate: b.StartDate, EndedOn: b.EndedOn,
		Rotation: nonNil(b.Rotation), Microcycles: []MicrocycleView{}, OffPlan: []SessionLink{},
	}
	for _, l := range sessions {
		if l.OffPlan || l.MicrocycleID == 0 {
			v.OffPlan = append(v.OffPlan, l)
			continue
		}
		byMicrocycle[l.MicrocycleID] = append(byMicrocycle[l.MicrocycleID], l)
	}
	calendar := b.Advance == AdvanceCalendar && b.StartDate != ""
	if calendar {
		v.EndDate = addDays(b.StartDate, len(b.Microcycles)*b.MicrocycleDays-1)
	}
	st := microcycleStates(b, s.today())
	for i, m := range b.Microcycles {
		rot := effectiveRotation(b, m)
		placed := byMicrocycle[m.ID]
		mv := MicrocycleView{
			ID: m.ID, Position: m.Position, Label: m.Label, SetsDelta: m.SetsDelta, EffortDelta: m.EffortDelta,
			OwnRotation: m.OwnRotation, Rotation: nonNil(rot), Status: m.Status, State: st[i], ClosedOn: m.ClosedOn,
			Sessions: nonNilLinks(placed), SessionsDone: len(placed), SessionsPlanned: len(rot), Due: due(rot, placed),
		}
		if calendar {
			mv.StartDate = addDays(b.StartDate, (m.Position-1)*b.MicrocycleDays)
			mv.EndDate = addDays(mv.StartDate, b.MicrocycleDays-1)
		}
		v.Microcycles = append(v.Microcycles, mv)
	}
	return v, nil
}

func summary(v BlockView) BlockSummary {
	out := BlockSummary{
		ID: v.ID, GoalID: v.GoalID, Intent: v.Intent, Advance: v.Advance, MicrocycleDays: v.MicrocycleDays,
		MicrocycleCount: v.MicrocycleCount, Status: v.Status, StartDate: v.StartDate, EndDate: v.EndDate,
		EndedOn: v.EndedOn, OffPlanSessions: len(v.OffPlan),
	}
	for _, m := range v.Microcycles {
		switch {
		case m.State == StateDone:
			out.MicrocyclesDone++
		case m.State == StateSkipped:
			out.MicrocyclesSkipped++
		case remaining(m.State):
			out.MicrocyclesRemaining++
		}
		if m.State == StateCurrent && out.CurrentPosition == 0 {
			out.CurrentPosition = m.Position
		}
	}
	return out
}

func nonNil(r []RotationEntry) []RotationEntry {
	if r == nil {
		return []RotationEntry{}
	}
	return r
}

func nonNilLinks(l []SessionLink) []SessionLink {
	if l == nil {
		return []SessionLink{}
	}
	return l
}

// --- reads -------------------------------------------------------------------

func (s *Service) GetBlock(ctx context.Context, id int64) (BlockView, error) {
	ps, err := s.periodization()
	if err != nil {
		return BlockView{}, err
	}
	if id <= 0 {
		return BlockView{}, ErrValidation
	}
	b, err := ps.GetBlock(ctx, id)
	if err != nil {
		return BlockView{}, err
	}
	return s.view(ctx, ps, b)
}

func (s *Service) ListBlocks(ctx context.Context) (BlockList, error) {
	ps, err := s.periodization()
	if err != nil {
		return BlockList{}, err
	}
	goals, err := ps.Goals(ctx)
	if err != nil {
		return BlockList{}, err
	}
	blocks, err := ps.Blocks(ctx)
	if err != nil {
		return BlockList{}, err
	}
	out := BlockList{Goals: append([]Goal{}, goals...), Blocks: []BlockSummary{}}
	for _, b := range blocks {
		v, err := s.view(ctx, ps, b)
		if err != nil {
			return BlockList{}, err
		}
		out.Blocks = append(out.Blocks, summary(v))
	}
	return out, nil
}

func (s *Service) TrainingStatus(ctx context.Context) (TrainingStatus, error) {
	ps, err := s.periodization()
	if err != nil {
		return TrainingStatus{}, err
	}
	out := TrainingStatus{Today: s.today(), Goals: []Goal{}}
	goals, err := ps.Goals(ctx)
	if err != nil {
		return TrainingStatus{}, err
	}
	for _, g := range goals {
		if g.Status == GoalActive {
			out.Goals = append(out.Goals, g)
		}
	}
	b, active, err := ps.ActiveBlock(ctx)
	if err != nil || !active {
		return out, err
	}
	v, err := s.view(ctx, ps, b)
	if err != nil {
		return TrainingStatus{}, err
	}
	sum := summary(v)
	out.Block = &sum
	for i := range v.Microcycles {
		if v.Microcycles[i].State == StateCurrent {
			out.Current = &v.Microcycles[i]
			break
		}
	}
	return out, nil
}

// --- goals -------------------------------------------------------------------

func (s *Service) SetGoal(ctx context.Context, in GoalInput) (Goal, error) {
	ps, err := s.periodization()
	if err != nil {
		return Goal{}, err
	}
	g := Goal{Status: GoalActive}
	switch {
	case in.ID < 0:
		return Goal{}, ErrValidation
	case in.ID > 0:
		if g, err = ps.GetGoal(ctx, in.ID); err != nil {
			return Goal{}, err
		}
	case in.Text == nil:
		return Goal{}, ErrValidation
	}
	if in.Text != nil {
		g.Text = strings.TrimSpace(*in.Text)
	}
	if in.TargetDate != nil {
		g.TargetDate = strings.TrimSpace(*in.TargetDate)
	}
	if in.Status != nil {
		g.Status = *in.Status
	}
	if g.Text == "" || len(g.Text) > maxGoalTextLen || (g.TargetDate != "" && !validDate(g.TargetDate)) {
		return Goal{}, ErrValidation
	}
	if g.Status != GoalActive && g.Status != GoalAchieved && g.Status != GoalDropped {
		return Goal{}, ErrValidation
	}
	return ps.SaveGoal(ctx, g)
}

// --- blocks ------------------------------------------------------------------

// rotationOf resolves plan ids to rotation entries, snapshotting each name.
func (s *Service) rotationOf(ctx context.Context, ids []int64) ([]RotationEntry, error) {
	if len(ids) > MaxRotation {
		return nil, ErrValidation
	}
	out := make([]RotationEntry, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrValidation
		}
		p, err := s.store.GetPlan(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, RotationEntry{PlanID: p.ID, PlanName: p.Name})
	}
	return out, nil
}

func sameRotation(a, b []RotationEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SaveBlock creates a block or edits one forward only: a done block is never
// edited, a microcycle edit must target a remaining microcycle, a shrink drops
// only remaining microcycles that hold no session, and a new block rotation is
// first frozen into the closed or elapsed microcycles that followed it.
func (s *Service) SaveBlock(ctx context.Context, in BlockInput) (BlockView, error) {
	ps, err := s.periodization()
	if err != nil {
		return BlockView{}, err
	}
	today := s.today()
	var b Block
	var st []string
	switch {
	case in.ID < 0:
		return BlockView{}, ErrValidation
	case in.ID == 0:
		if in.Intent == nil || in.MicrocycleCount == nil {
			return BlockView{}, ErrValidation
		}
		b = Block{Advance: AdvanceSequence, MicrocycleDays: 7, Status: BlockSketch}
	default:
		if b, err = ps.GetBlock(ctx, in.ID); err != nil {
			return BlockView{}, err
		}
		if b.Status == BlockDone {
			return BlockView{}, ErrValidation
		}
		st = microcycleStates(b, today)
	}
	if in.Intent != nil {
		b.Intent = strings.TrimSpace(*in.Intent)
		if b.Intent == "" || len(b.Intent) > maxBlockIntentLen {
			return BlockView{}, ErrValidation
		}
	}
	if in.GoalID != nil {
		switch {
		case *in.GoalID < 0:
			return BlockView{}, ErrValidation
		case *in.GoalID > 0:
			if _, err := ps.GetGoal(ctx, *in.GoalID); err != nil {
				return BlockView{}, err
			}
		}
		b.GoalID = *in.GoalID
	}
	if in.Status != nil {
		if (*in.Status != BlockSketch && *in.Status != BlockPlanned) || b.Status == BlockActive {
			return BlockView{}, ErrValidation
		}
		b.Status = *in.Status
	}
	active := b.Status == BlockActive
	// Mode and length. An active block keeps the microcycle it is in: to
	// calendar, the first pending one starts today; back to sequence, the
	// pending ones whose dates passed are done; a new length keeps the start
	// of today's microcycle.
	advance, days := b.Advance, b.MicrocycleDays
	if in.Advance != nil {
		if *in.Advance != AdvanceSequence && *in.Advance != AdvanceCalendar {
			return BlockView{}, ErrValidation
		}
		advance = *in.Advance
	}
	if in.MicrocycleDays != nil {
		if *in.MicrocycleDays < 1 || *in.MicrocycleDays > MaxMicrocycleDays {
			return BlockView{}, ErrValidation
		}
		days = *in.MicrocycleDays
	}
	if active {
		switch {
		case b.Advance == AdvanceCalendar && advance == AdvanceSequence:
			for i := range b.Microcycles {
				m := &b.Microcycles[i]
				if m.Status == MicrocyclePending && st[i] == StateElapsed {
					m.Status = MicrocycleDone
					m.ClosedOn = addDays(b.StartDate, m.Position*b.MicrocycleDays-1)
					st[i] = StateDone
				}
			}
		case b.Advance == AdvanceSequence && advance == AdvanceCalendar:
			first := len(b.Microcycles) + 1
			for _, m := range b.Microcycles {
				if m.Status == MicrocyclePending {
					first = m.Position
					break
				}
			}
			b.StartDate = addDays(today, -(first-1)*days)
		case advance == AdvanceCalendar && days != b.MicrocycleDays && b.StartDate != "" && today >= b.StartDate:
			pos := calendarPosition(b, today)
			current := addDays(b.StartDate, (pos-1)*b.MicrocycleDays)
			b.StartDate = addDays(current, -(pos-1)*days)
		}
	}
	b.Advance, b.MicrocycleDays = advance, days
	// Rotation: the closed and elapsed microcycles that followed the block's
	// keep the one they ran.
	if in.Rotation != nil {
		rot, err := s.rotationOf(ctx, *in.Rotation)
		if err != nil {
			return BlockView{}, err
		}
		if !sameRotation(rot, b.Rotation) {
			for i := range b.Microcycles {
				m := &b.Microcycles[i]
				if !remaining(st[i]) && !m.OwnRotation {
					m.OwnRotation, m.Rotation = true, append([]RotationEntry(nil), b.Rotation...)
				}
			}
			b.Rotation = rot
		}
	}
	// Length.
	if in.MicrocycleCount != nil {
		n := *in.MicrocycleCount
		if n < 1 || n > MaxMicrocycles {
			return BlockView{}, ErrValidation
		}
		if n < len(b.Microcycles) {
			var placed []SessionLink
			if b.ID != 0 {
				if placed, err = ps.BlockSessions(ctx, b.ID); err != nil {
					return BlockView{}, err
				}
			}
			for i := n; i < len(b.Microcycles); i++ {
				if !remaining(st[i]) {
					return BlockView{}, ErrValidation
				}
				for _, l := range placed {
					if l.MicrocycleID == b.Microcycles[i].ID {
						return BlockView{}, ErrValidation
					}
				}
			}
			b.Microcycles, st = b.Microcycles[:n], st[:n]
		}
		for len(b.Microcycles) < n {
			b.Microcycles = append(b.Microcycles, Microcycle{Position: len(b.Microcycles) + 1, Status: MicrocyclePending})
			st = append(st, StateUpcoming)
		}
	}
	// Microcycle edits, remaining microcycles only.
	for _, e := range in.Microcycles {
		if e.Position < 1 || e.Position > len(b.Microcycles) || !remaining(st[e.Position-1]) || (e.InheritRotation && e.Rotation != nil) {
			return BlockView{}, ErrValidation
		}
		m := &b.Microcycles[e.Position-1]
		if e.Label != nil {
			label := strings.TrimSpace(*e.Label)
			if len(label) > maxMicrocycleLabelLen {
				return BlockView{}, ErrValidation
			}
			m.Label = label
		}
		if e.SetsDelta != nil {
			if *e.SetsDelta < -MaxSetsDelta || *e.SetsDelta > MaxSetsDelta {
				return BlockView{}, ErrValidation
			}
			m.SetsDelta = *e.SetsDelta
		}
		if e.EffortDelta != nil {
			d := *e.EffortDelta
			if math.IsNaN(d) || d < -MaxEffortDelta || d > MaxEffortDelta {
				return BlockView{}, ErrValidation
			}
			m.EffortDelta = d
		}
		if e.Rotation != nil {
			rot, err := s.rotationOf(ctx, *e.Rotation)
			if err != nil {
				return BlockView{}, err
			}
			m.OwnRotation, m.Rotation = true, rot
		}
		if e.InheritRotation {
			m.OwnRotation, m.Rotation = false, nil
		}
	}
	saved, err := ps.SaveBlock(ctx, b)
	if err != nil {
		return BlockView{}, err
	}
	return s.view(ctx, ps, saved)
}

// ShrinksBlock tells whether an edit drops microcycles, which makes it a
// deleting change. Inputs that fail validation are left to SaveBlock.
func (s *Service) ShrinksBlock(ctx context.Context, in BlockInput) (bool, error) {
	if in.ID <= 0 || in.MicrocycleCount == nil {
		return false, nil
	}
	ps, err := s.periodization()
	if err != nil {
		return false, err
	}
	b, err := ps.GetBlock(ctx, in.ID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil && *in.MicrocycleCount < len(b.Microcycles), err
}

// AdvanceBlock activates a block, completes or skips the current microcycle
// of a sequence block, or closes a block. Only one block is active at a time.
func (s *Service) AdvanceBlock(ctx context.Context, in AdvanceInput) (BlockView, error) {
	ps, err := s.periodization()
	if err != nil {
		return BlockView{}, err
	}
	if in.BlockID <= 0 {
		return BlockView{}, ErrValidation
	}
	b, err := ps.GetBlock(ctx, in.BlockID)
	if err != nil {
		return BlockView{}, err
	}
	today := s.today()
	if in.StartDate != "" && (in.Action != BlockActivate || !validDate(in.StartDate)) {
		return BlockView{}, ErrValidation
	}
	switch in.Action {
	case BlockActivate:
		if b.Status != BlockSketch && b.Status != BlockPlanned {
			return BlockView{}, ErrValidation
		}
		if _, active, err := ps.ActiveBlock(ctx); err != nil {
			return BlockView{}, err
		} else if active {
			return BlockView{}, ErrValidation
		}
		b.Status, b.StartDate = BlockActive, in.StartDate
		if b.StartDate == "" {
			b.StartDate = today
		}
	case BlockCompleteCurrent, BlockSkipCurrent:
		if b.Status != BlockActive || b.Advance != AdvanceSequence {
			return BlockView{}, ErrValidation
		}
		i := firstPending(b)
		if i < 0 {
			return BlockView{}, ErrValidation
		}
		b.Microcycles[i].Status, b.Microcycles[i].ClosedOn = MicrocycleDone, today
		if in.Action == BlockSkipCurrent {
			b.Microcycles[i].Status = MicrocycleSkipped
		}
	case BlockClose:
		if b.Status == BlockDone {
			return BlockView{}, ErrValidation
		}
		b.Status, b.EndedOn = BlockDone, today
	default:
		return BlockView{}, ErrValidation
	}
	saved, err := ps.SaveBlock(ctx, b)
	if err != nil {
		return BlockView{}, err
	}
	return s.view(ctx, ps, saved)
}

func firstPending(b Block) int {
	for i, m := range b.Microcycles {
		if m.Status == MicrocyclePending {
			return i
		}
	}
	return -1
}

// --- placing sessions --------------------------------------------------------

// AssignSessionBlock moves a session's link by hand: into a microcycle of any
// of the account's blocks, or off-plan (kept in the block it was in, else the
// active one). It never closes or opens a microcycle.
func (s *Service) AssignSessionBlock(ctx context.Context, in AssignInput) (SessionLink, error) {
	ps, err := s.periodization()
	if err != nil {
		return SessionLink{}, err
	}
	if in.SessionID <= 0 || in.MicrocycleID < 0 || (in.MicrocycleID > 0) == in.OffPlan {
		return SessionLink{}, ErrValidation
	}
	link, err := ps.SessionLink(ctx, in.SessionID)
	if err != nil {
		return SessionLink{}, err
	}
	if in.OffPlan {
		link.MicrocycleID, link.OffPlan = 0, true
		if link.BlockID == 0 {
			b, active, err := ps.ActiveBlock(ctx)
			if err != nil {
				return SessionLink{}, err
			}
			if active {
				link.BlockID = b.ID
			}
		}
	} else {
		block, err := ps.BlockOfMicrocycle(ctx, in.MicrocycleID)
		if err != nil {
			return SessionLink{}, err
		}
		link.BlockID, link.MicrocycleID, link.OffPlan = block, in.MicrocycleID, false
	}
	if err := ps.LinkSession(ctx, link, nil); err != nil {
		return SessionLink{}, err
	}
	return ps.SessionLink(ctx, in.SessionID)
}

// placeAfterWrite places a session after a write that already committed. It is
// best effort: logging a set never fails or loses data because of
// periodization, so a failure is logged server-side (no personal data) and the
// session stays unplaced, for the next write or assign_session_block to place.
func (s *Service) placeAfterWrite(ctx context.Context, sessionID int64) {
	if err := s.placeSession(ctx, sessionID); err != nil {
		log.Printf("periodization: session left unplaced after a write: %v", err)
	}
}

// placeSession places a session nobody placed yet in the active block, if
// there is one. Sequence: the first pending microcycle with routines left
// (one that is full closes on the way), and it closes as soon as its last
// routine is done; calendar: the microcycle whose dates cover the session. A
// session that fits nowhere is kept off-plan, never rejected. Without an
// active block, or on a store without periodization, nothing changes.
func (s *Service) placeSession(ctx context.Context, sessionID int64) error {
	ps, ok := s.store.(PeriodizationStore)
	if !ok {
		return nil
	}
	b, active, err := ps.ActiveBlock(ctx)
	if err != nil || !active {
		return err
	}
	link, err := ps.SessionLink(ctx, sessionID)
	if err != nil || link.Placed {
		return err
	}
	link.BlockID, link.MicrocycleID, link.OffPlan = b.ID, 0, true
	var changed *Block
	switch b.Advance {
	case AdvanceCalendar:
		if pos := calendarPosition(b, link.Date); pos >= 1 && pos <= len(b.Microcycles) {
			link.MicrocycleID, link.OffPlan = b.Microcycles[pos-1].ID, false
		}
	default:
		if b.StartDate != "" && link.Date < b.StartDate {
			break
		}
		placed, err := ps.BlockSessions(ctx, b.ID)
		if err != nil {
			return err
		}
		count := map[int64]int{}
		for _, l := range placed {
			count[l.MicrocycleID]++
		}
		today := s.today()
		for i := range b.Microcycles {
			m := &b.Microcycles[i]
			if m.Status != MicrocyclePending {
				continue
			}
			planned := len(effectiveRotation(b, *m))
			if planned > 0 && count[m.ID] >= planned {
				m.Status, m.ClosedOn, changed = MicrocycleDone, today, &b
				continue
			}
			link.MicrocycleID, link.OffPlan = m.ID, false
			if planned > 0 && count[m.ID]+1 >= planned {
				m.Status, m.ClosedOn, changed = MicrocycleDone, today, &b
			}
			break
		}
	}
	return ps.LinkSession(ctx, link, changed)
}
