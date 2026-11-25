package websrv

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// Bloques (#41, slice 5): the phone view of goals, blocks and microcycles,
// opened from Planificar's chips. It reads and writes through the same
// training.Service calls as the MCP tools (TrainingStatus, ListBlocks,
// GetBlock, SetGoal, SaveBlock, AdvanceBlock, AssignSessionBlock), so every
// rule (forward-only edits, one active block, lengths) stays in the domain.
// The forms are the person acting, so they write directly, as Rutinas does;
// agent writes are the ones that become proposals.
//
// Nothing here suggests a length, a deload or a score: labels are the
// person's words and states are named, never coloured as good or bad.

// PeriodizationData is the blocks screen. Without ?bloque= it is the overview:
// goals, the active block in full, the other blocks and a form for a new one;
// with it, that block in full.
type PeriodizationData struct {
	Goals []training.Goal
	// Shown is the block in full: the active one on the overview, or the one
	// asked for.
	Shown *BlockPage
	// Others are the blocks that are not shown, newest first.
	Others []BlockRow
	// Single is a block's own page; the overview leaves it false.
	Single bool
	// Plans are the routines a rotation can hold.
	Plans []training.Plan
	// NewForm is the form that creates a block.
	NewForm BlockForm
}

// Empty says there is nothing planned yet: no goal and no block.
func (d PeriodizationData) Empty() bool {
	return len(d.Goals) == 0 && d.Shown == nil && len(d.Others) == 0
}

// BlockRow is a block in the list of other blocks.
type BlockRow struct {
	training.BlockSummary
	GoalText string
}

func (r BlockRow) StatusLabel() string  { return blockStatusLabel(r.Status) }
func (r BlockRow) AdvanceLabel() string { return advanceLabel(r.Advance) }

// BlockPage is one block in full, as the screen shows it.
type BlockPage struct {
	training.BlockView
	GoalText    string
	Microcycles []MicrocycleRow
	// Current is the microcycle the next session counts in, if any.
	Current *MicrocycleRow
	// Next is the first routine still due in Current.
	Next *training.RotationEntry
	// Targets are where a session of this block can be moved.
	Targets []MoveTarget
	// OffPlanRows are the block's sessions kept outside the plan.
	OffPlanRows []SessionRow
	// Form edits the block.
	Form BlockForm
}

func (p BlockPage) StatusLabel() string  { return blockStatusLabel(p.Status) }
func (p BlockPage) AdvanceLabel() string { return advanceLabel(p.Advance) }
func (p BlockPage) Active() bool         { return p.Status == training.BlockActive }
func (p BlockPage) Done() bool           { return p.Status == training.BlockDone }
func (p BlockPage) Sequence() bool       { return p.Advance == training.AdvanceSequence }
func (p BlockPage) Calendar() bool       { return p.Advance == training.AdvanceCalendar }
func (p BlockPage) Startable() bool {
	return p.Status == training.BlockSketch || p.Status == training.BlockPlanned
}

// Count is "4 microciclos".
func (p BlockPage) Count() string {
	return countOf(p.MicrocycleCount, "microciclo", "microciclos")
}

// Days is "microciclos de 7 días".
func (p BlockPage) Days() string {
	return fmt.Sprintf("microciclos de %s", countOf(p.MicrocycleDays, "día", "días"))
}

// MicrocycleRow is one microcycle on the timeline.
type MicrocycleRow struct {
	training.MicrocycleView
	Routines    []RoutineRow
	Slots       []RotationSlot
	SessionRows []SessionRow
}

func (m MicrocycleRow) StateLabel() string { return stateLabel(m.State) }
func (m MicrocycleRow) IsCurrent() bool    { return m.State == training.StateCurrent }

// Editable is a remaining microcycle: the only kind the domain lets change.
func (m MicrocycleRow) Editable() bool {
	return m.State == training.StateCurrent || m.State == training.StateUpcoming
}

// Tally is "1/2": sessions placed over routines planned.
func (m MicrocycleRow) Tally() string {
	return fmt.Sprintf("%d/%d", m.SessionsDone, m.SessionsPlanned)
}

// Modifiers is the microcycle's stored modifiers in a short line, or "".
func (m MicrocycleRow) Modifiers() string {
	var parts []string
	if m.SetsDelta != 0 {
		parts = append(parts, signed(float64(m.SetsDelta))+" "+plural(abs(m.SetsDelta), "serie", "series"))
	}
	if m.EffortDelta != 0 {
		parts = append(parts, "RPE "+signed(m.EffortDelta))
	}
	return strings.Join(parts, " · ")
}

// SetsValue and EffortValue prefill the edit form; 0 is left blank.
func (m MicrocycleRow) SetsValue() string {
	if m.SetsDelta == 0 {
		return ""
	}
	return strconv.Itoa(m.SetsDelta)
}

func (m MicrocycleRow) EffortValue() string {
	if m.EffortDelta == 0 {
		return ""
	}
	return formatNumber(m.EffortDelta)
}

// RoutineRow is one routine of a microcycle's rotation and what became of it.
type RoutineRow struct {
	training.RotationEntry
	Status string
}

// RotationSlot is one select of a rotation form, with the routine it holds.
type RotationSlot struct {
	N        int
	Selected int64
	Plans    []training.Plan
}

// SessionRow is a session of a block with the form that moves it. It carries
// the mount and the CSRF token because its template has no page around it.
type SessionRow struct {
	Base, CSRF string
	Session    training.SessionLink
	Targets    []MoveTarget
}

// BlockForm creates a block (Block nil) or edits one.
type BlockForm struct {
	Base, CSRF string
	Block      *BlockPage
	Goals      []training.Goal
	GoalID     int64
	// Status is the block's sketch or planned status; empty hides the choice,
	// as for a new block (always a sketch) or an active one.
	Status   string
	Calendar bool
	Plans    []training.Plan
	Slots    []RotationSlot
}

func (BlockForm) MaxCount() int { return training.MaxMicrocycles }
func (BlockForm) MaxDays() int  { return training.MaxMicrocycleDays }

// MoveTarget is a microcycle a session can be moved into.
type MoveTarget struct {
	ID    int64
	Label string
}

func blockStatusLabel(status string) string {
	switch status {
	case training.BlockSketch:
		return "Borrador"
	case training.BlockPlanned:
		return "Planificado"
	case training.BlockActive:
		return "Activo"
	case training.BlockDone:
		return "Terminado"
	}
	return status
}

func advanceLabel(advance string) string {
	if advance == training.AdvanceCalendar {
		return "Por calendario"
	}
	return "Por secuencia"
}

func stateLabel(state string) string {
	switch state {
	case training.StateDone:
		return "Hecho"
	case training.StateSkipped:
		return "Saltado"
	case training.StateCurrent:
		return "Actual"
	case training.StateUpcoming:
		return "Por hacer"
	case training.StateElapsed:
		return "Pasado"
	}
	return state
}

// signed writes a delta with its sign, using a true minus: "+1", "−0.5".
func signed(v float64) string {
	if v < 0 {
		return "−" + formatNumber(-v)
	}
	return "+" + formatNumber(v)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// slots lays out a rotation's selects: the routines it holds and a few blank
// ones to add more, up to the domain's maximum.
func slots(rotation []training.RotationEntry, plans []training.Plan) []RotationSlot {
	n := min(max(len(rotation)+2, 3), training.MaxRotation)
	out := make([]RotationSlot, 0, n)
	for i := range n {
		s := RotationSlot{N: i + 1, Plans: plans}
		if i < len(rotation) {
			s.Selected = rotation[i].PlanID
		}
		out = append(out, s)
	}
	return out
}

// routines reads what became of each routine of a microcycle's rotation from
// the domain's own reading: a routine still in Due was not done.
func routines(m training.MicrocycleView) []RoutineRow {
	left := append([]training.RotationEntry(nil), m.Due...)
	out := make([]RoutineRow, 0, len(m.Rotation))
	for _, r := range m.Rotation {
		row := RoutineRow{RotationEntry: r, Status: "hecha"}
		for i, d := range left {
			if d == r {
				left = append(left[:i], left[i+1:]...)
				switch m.State {
				case training.StateCurrent, training.StateUpcoming:
					row.Status = "pendiente"
				case training.StateSkipped:
					row.Status = "saltada"
				default:
					row.Status = "sin hacer"
				}
				break
			}
		}
		out = append(out, row)
	}
	return out
}

// pageForms is what every form on the screen needs besides its own fields.
type pageForms struct {
	base, csrf string
	goals      []training.Goal
	plans      []training.Plan
}

// newBlockForm is the form that creates a block: always a sketch, with no
// length filled in.
func (f pageForms) newBlockForm() BlockForm {
	return BlockForm{Base: f.base, CSRF: f.csrf, Goals: f.goals, Plans: f.plans, Slots: slots(nil, f.plans)}
}

func blockPage(v training.BlockView, f pageForms) *BlockPage {
	p := &BlockPage{BlockView: v}
	for _, g := range f.goals {
		if g.ID == v.GoalID {
			p.GoalText = g.Text
		}
	}
	for _, m := range v.Microcycles {
		label := "Microciclo " + strconv.Itoa(m.Position)
		if m.Label != "" {
			label += " · " + m.Label
		}
		p.Targets = append(p.Targets, MoveTarget{ID: m.ID, Label: label})
	}
	for _, m := range v.Microcycles {
		row := MicrocycleRow{MicrocycleView: m, Routines: routines(m), Slots: slots(m.Rotation, f.plans)}
		for _, link := range m.Sessions {
			row.SessionRows = append(row.SessionRows, SessionRow{Base: f.base, CSRF: f.csrf, Session: link, Targets: p.Targets})
		}
		p.Microcycles = append(p.Microcycles, row)
	}
	for _, link := range v.OffPlan {
		p.OffPlanRows = append(p.OffPlanRows, SessionRow{Base: f.base, CSRF: f.csrf, Session: link, Targets: p.Targets})
	}
	p.Form = BlockForm{Base: f.base, CSRF: f.csrf, Block: p, Goals: f.goals, GoalID: v.GoalID,
		Calendar: v.Advance == training.AdvanceCalendar, Plans: f.plans, Slots: slots(v.Rotation, f.plans)}
	if v.Status == training.BlockSketch || v.Status == training.BlockPlanned {
		p.Form.Status = v.Status
	}
	for i := range p.Microcycles {
		if p.Microcycles[i].IsCurrent() {
			p.Current = &p.Microcycles[i]
			for j := range p.Current.Due {
				if p.Current.Due[j].PlanID != 0 {
					p.Next = &p.Current.Due[j]
					break
				}
			}
			break
		}
	}
	return p
}

// periodizationData reads the overview (selected 0) or one block's page.
// ErrNotFound means the selected block is not the person's.
func (s *Server) periodizationData(r *http.Request, selected int64) (PeriodizationData, error) {
	ctx := r.Context()
	svc := s.service(ctx)
	out := PeriodizationData{Single: selected != 0}
	list, err := svc.ListBlocks(ctx)
	if err != nil {
		return out, err
	}
	if out.Plans, err = svc.ListPlans(ctx); err != nil {
		return out, err
	}
	out.Goals = list.Goals
	forms := pageForms{base: s.base, goals: list.Goals, plans: out.Plans}
	if s.opts.CSRFToken != nil {
		forms.csrf = s.opts.CSRFToken(r)
	}
	out.NewForm = forms.newBlockForm()
	show := selected
	if show == 0 {
		for _, b := range list.Blocks {
			if b.Status == training.BlockActive {
				show = b.ID
			}
		}
	}
	if show != 0 {
		v, err := svc.GetBlock(ctx, show)
		if err != nil {
			return out, err
		}
		out.Shown = blockPage(v, forms)
	}
	goalText := map[int64]string{}
	for _, g := range list.Goals {
		goalText[g.ID] = g.Text
	}
	for i := len(list.Blocks) - 1; i >= 0; i-- {
		if b := list.Blocks[i]; b.ID != show {
			out.Others = append(out.Others, BlockRow{BlockSummary: b, GoalText: goalText[b.GoalID]})
		}
	}
	return out, nil
}

// blocksPage is the screen; ?bloque=ID opens one block.
func (s *Server) blocksPage(w http.ResponseWriter, r *http.Request) {
	var selected int64
	if raw := r.URL.Query().Get("bloque"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		selected = id
	}
	data, err := s.periodizationData(r, selected)
	if errors.Is(err, training.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, r, "blocks.html", pageData{Base: s.base, Today: s.todayDate(), Periodization: data})
}

// errBadForm is a field that is not a number where one is needed; it is
// answered like the domain's own refusal.
var errBadForm = fmt.Errorf("%w: malformed form field", training.ErrValidation)

func formID(r *http.Request, name string) (int64, error) {
	v := strings.TrimSpace(r.PostFormValue(name))
	if v == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 0 {
		return 0, errBadForm
	}
	return id, nil
}

// optionalInt is nil for a blank field.
func optionalInt(r *http.Request, name string) (*int, error) {
	v := strings.TrimSpace(r.PostFormValue(name))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, errBadForm
	}
	return &n, nil
}

// rotationField reads a rotation's selects in order, skipping blank ones.
func rotationField(r *http.Request) ([]int64, error) {
	out := []int64{}
	for _, v := range r.PostForm["rotation"] {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, errBadForm
		}
		out = append(out, id)
	}
	return out, nil
}

// saveGoal creates a goal, or edits one when goal_id is given.
func (s *Server) saveGoal(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	id, err := formID(r, "goal_id")
	if err == nil {
		text, date := r.PostFormValue("text"), r.PostFormValue("target_date")
		_, err = s.service(r.Context()).SetGoal(r.Context(), training.GoalInput{ID: id, Text: &text, TargetDate: &date})
	}
	s.blocksAnswer(w, r, 0, err, fmt.Sprintf("Escribe el objetivo con tus palabras (hasta %d caracteres); la fecha es opcional.", 500))
}

// saveBlock creates a block (intent and number of microcycles) or edits one.
func (s *Server) saveBlock(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	in, err := blockForm(r)
	var saved training.BlockView
	if err == nil {
		saved, err = s.service(r.Context()).SaveBlock(r.Context(), in)
	}
	s.blocksAnswer(w, r, saved.ID, err, fmt.Sprintf("No se pudo guardar el bloque. La intención es obligatoria; los microciclos van de 1 a %d y sus días de 1 a %d. "+
		"Un bloque terminado ya no se edita, y para acortarlo solo se quitan microciclos que quedan por hacer y no tienen sesiones.",
		training.MaxMicrocycles, training.MaxMicrocycleDays))
}

func blockForm(r *http.Request) (training.BlockInput, error) {
	id, err := formID(r, "block_id")
	if err != nil {
		return training.BlockInput{}, err
	}
	in := training.BlockInput{ID: id}
	intent := r.PostFormValue("intent")
	in.Intent = &intent
	if in.MicrocycleCount, err = optionalInt(r, "microcycle_count"); err != nil {
		return in, err
	}
	if in.MicrocycleDays, err = optionalInt(r, "microcycle_days"); err != nil {
		return in, err
	}
	if r.PostForm.Has("goal_id") {
		goal, err := formID(r, "goal_id")
		if err != nil {
			return in, err
		}
		in.GoalID = &goal
	}
	if v := r.PostFormValue("advance"); v != "" {
		in.Advance = &v
	}
	if v := r.PostFormValue("status"); v != "" {
		in.Status = &v
	}
	if r.PostForm.Has("rotation") {
		rotation, err := rotationField(r)
		if err != nil {
			return in, err
		}
		in.Rotation = &rotation
	}
	return in, nil
}

// saveMicrocycle edits one remaining microcycle: its label, its modifiers and
// whether it follows the block's rotation or has its own.
func (s *Server) saveMicrocycle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	id, err := formID(r, "block_id")
	var saved training.BlockView
	if err == nil {
		var edit training.MicrocycleInput
		if edit, err = microcycleForm(r); err == nil {
			saved, err = s.service(r.Context()).SaveBlock(r.Context(), training.BlockInput{ID: id, Microcycles: []training.MicrocycleInput{edit}})
		}
	}
	s.blocksAnswer(w, r, saved.ID, err, fmt.Sprintf("Solo se pueden cambiar los microciclos que quedan por hacer. La etiqueta admite hasta 80 caracteres, "+
		"las series van de −%d a %d y el RPE de −%s a %s.", training.MaxSetsDelta, training.MaxSetsDelta,
		formatNumber(training.MaxEffortDelta), formatNumber(training.MaxEffortDelta)))
}

func microcycleForm(r *http.Request) (training.MicrocycleInput, error) {
	position, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("position")))
	if err != nil {
		return training.MicrocycleInput{}, errBadForm
	}
	label := r.PostFormValue("label")
	in := training.MicrocycleInput{Position: position, Label: &label}
	sets, err := optionalInt(r, "sets_delta")
	if err != nil {
		return in, err
	}
	if sets == nil {
		sets = new(int)
	}
	in.SetsDelta = sets
	effort := 0.0
	if v := strings.Replace(strings.TrimSpace(r.PostFormValue("effort_delta")), ",", ".", 1); v != "" {
		if effort, err = strconv.ParseFloat(v, 64); err != nil {
			return in, errBadForm
		}
	}
	in.EffortDelta = &effort
	if r.PostFormValue("rotation_mode") == "own" {
		rotation, err := rotationField(r)
		if err != nil {
			return in, err
		}
		in.Rotation = &rotation
	} else {
		in.InheritRotation = true
	}
	return in, nil
}

// advanceBlock activates a block, closes or skips its current microcycle, or
// closes the block.
func (s *Server) advanceBlock(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	id, err := formID(r, "block_id")
	action := r.PostFormValue("action")
	var saved training.BlockView
	if err == nil {
		saved, err = s.service(r.Context()).AdvanceBlock(r.Context(), training.AdvanceInput{
			BlockID: id, Action: action, StartDate: strings.TrimSpace(r.PostFormValue("start_date")),
		})
	}
	reason := "Ese bloque ya está terminado."
	switch action {
	case training.BlockActivate:
		reason = "No se pudo activar: ya hay otro bloque activo, este ya empezó, o la fecha no es válida."
	case training.BlockCompleteCurrent, training.BlockSkipCurrent:
		reason = "Solo se cierra o se salta el microciclo actual de un bloque activo que avanza por secuencia, mientras le queden microciclos."
	}
	s.blocksAnswer(w, r, saved.ID, err, reason)
}

// assignSession moves a session into a microcycle, or off-plan when
// microcycle_id is "off_plan".
func (s *Server) assignSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	session, err := formID(r, "session_id")
	in := training.AssignInput{SessionID: session}
	if err == nil {
		if r.PostFormValue("microcycle_id") == "off_plan" {
			in.OffPlan = true
		} else {
			in.MicrocycleID, err = formID(r, "microcycle_id")
		}
	}
	var link training.SessionLink
	if err == nil {
		link, err = s.service(r.Context()).AssignSessionBlock(r.Context(), in)
	}
	s.blocksAnswer(w, r, link.BlockID, err, "No se pudo mover la sesión: elige un microciclo o «fuera del plan».")
}

// blocksAnswer ends a write: success goes back to the block (the overview
// when it is the active one); a refusal shows the screen again with the
// reason, and a missing or another person's id the same "no longer exists".
func (s *Server) blocksAnswer(w http.ResponseWriter, r *http.Request, block int64, err error, refused string) {
	ctx := r.Context()
	switch {
	case err == nil:
		to := s.base + "/blocks"
		if block != 0 {
			if v, err := s.service(ctx).GetBlock(ctx, block); err == nil && v.Status != training.BlockActive {
				to += "?bloque=" + strconv.FormatInt(block, 10)
			}
		}
		s.redirect(w, r, to)
		return
	case errors.Is(err, training.ErrNotFound):
		s.blocksRefused(w, r, http.StatusNotFound, "Eso ya no existe: el objetivo, el bloque, la rutina o la sesión.")
	case errors.Is(err, training.ErrValidation):
		s.blocksRefused(w, r, http.StatusBadRequest, refused)
	default:
		s.fail(w, err)
	}
}

// blocksRefused shows the block the form named (the overview when it is the
// active one, missing or not the person's) with the reason above it.
func (s *Server) blocksRefused(w http.ResponseWriter, r *http.Request, status int, message string) {
	ctx := r.Context()
	var selected int64
	if id, err := formID(r, "block_id"); err == nil && id != 0 {
		if v, err := s.service(ctx).GetBlock(ctx, id); err == nil && v.Status != training.BlockActive {
			selected = id
		}
	}
	data, err := s.periodizationData(r, selected)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	s.render(w, r, "blocks.html", pageData{Base: s.base, Today: s.todayDate(), Error: message, Periodization: data})
}
