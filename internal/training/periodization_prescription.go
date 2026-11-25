package training

import (
	"context"
	"errors"
	"log"
	"math"
	"time"
)

// PrescriptionRule states how a microcycle's modifiers turn a routine into
// the day's prescription, so every output that shows one also says how.
const PrescriptionRule = "sets = base sets + sets_delta, kept within 1-20; target RPE = base target RPE + effort_delta, kept within 1-10, only where the routine sets a target RPE"

// Prescription is a routine with a microcycle's modifiers applied: where they
// came from, the deltas, and per exercise the base value next to the result.
// A session keeps the one applied when it started; the routine never changes.
type Prescription struct {
	BlockID            int64            `json:"block_id"`
	MicrocycleID       int64            `json:"microcycle_id"`
	MicrocyclePosition int              `json:"microcycle_position"`
	MicrocycleLabel    string           `json:"microcycle_label,omitempty"`
	PlanID             int64            `json:"plan_id"`
	PlanName           string           `json:"plan_name"`
	SetsDelta          int              `json:"sets_delta"`
	EffortDelta        float64          `json:"effort_delta"`
	Rule               string           `json:"rule"`
	Items              []PrescribedItem `json:"items"`
}

// PrescribedItem is one exercise: the routine's value, the result, and
// whether the result was held at a bound. TargetRPE is 0 when the routine
// sets none, and then no effort delta applies.
type PrescribedItem struct {
	Exercise      string  `json:"exercise"`
	BaseSets      int     `json:"base_sets"`
	Sets          int     `json:"sets"`
	SetsClamped   bool    `json:"sets_clamped,omitempty"`
	BaseTargetRPE float64 `json:"base_target_rpe,omitempty"`
	TargetRPE     float64 `json:"target_rpe,omitempty"`
	RPEClamped    bool    `json:"rpe_clamped,omitempty"`
}

// PrescriptionStore is the optional port that writes a prescription into a
// session's copy of its routine and reads back the one a session kept.
type PrescriptionStore interface {
	// ApplyPrescription sets each item's sets and target RPE on the session's
	// copy and records the prescription with the session, in one transaction.
	ApplyPrescription(ctx context.Context, sessionID int64, p Prescription) error
	// SessionPrescription is the prescription a session kept; false when none.
	SessionPrescription(ctx context.Context, sessionID int64) (Prescription, bool, error)
}

// hasModifiers tells whether a microcycle changes anything at all.
func hasModifiers(m Microcycle) bool { return m.SetsDelta != 0 || m.EffortDelta != 0 }

// prescribe applies a microcycle's modifiers to a routine's items. Sets stay
// within 1 and the domain maximum; a target RPE moves only where one is set
// and stays within 1-10.
func prescribe(b Block, m Microcycle, planID int64, planName string, base []PrescribedItem) Prescription {
	p := Prescription{
		BlockID: b.ID, MicrocycleID: m.ID, MicrocyclePosition: m.Position, MicrocycleLabel: m.Label,
		PlanID: planID, PlanName: planName, SetsDelta: m.SetsDelta, EffortDelta: m.EffortDelta,
		Rule: PrescriptionRule, Items: []PrescribedItem{},
	}
	for _, it := range base {
		out := PrescribedItem{Exercise: it.Exercise, BaseSets: it.BaseSets, BaseTargetRPE: it.BaseTargetRPE}
		sets := it.BaseSets + m.SetsDelta
		switch {
		case sets < 1:
			out.Sets, out.SetsClamped = 1, true
		case sets > maxTargetSets:
			out.Sets, out.SetsClamped = maxTargetSets, true
		default:
			out.Sets = sets
		}
		if it.BaseTargetRPE > 0 {
			rpe := math.Round((it.BaseTargetRPE+m.EffortDelta)*100) / 100
			switch {
			case rpe < 1:
				out.TargetRPE, out.RPEClamped = 1, true
			case rpe > 10:
				out.TargetRPE, out.RPEClamped = 10, true
			default:
				out.TargetRPE = rpe
			}
		}
		p.Items = append(p.Items, out)
	}
	return p
}

// maxTargetSets is the most sets one exercise of a routine holds.
const maxTargetSets = 20

// StartPrescribedSession starts a session from a routine, or a free-form one
// with planID 0, places it in the active block, and when it lands in a
// microcycle with modifiers applies them to the session's copy. It returns
// the prescription applied, nil when none was. Applying is best effort: a
// failure is logged and the session keeps the routine's values.
func (s *Service) StartPrescribedSession(ctx context.Context, date string, planID int64) (Session, *Prescription, error) {
	if date == "" {
		date = s.clock().In(s.clock().Location()).Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return Session{}, nil, ErrValidation
	}
	if planID < 0 {
		return Session{}, nil, ErrValidation
	}
	session, err := s.store.Start(ctx, Session{Date: date, PlanID: planID})
	if err != nil {
		return Session{}, nil, err
	}
	s.placeAfterWrite(ctx, session.ID)
	p, err := s.prescribeSession(ctx, session)
	if err != nil {
		log.Printf("periodization: modifiers left unapplied after a start: %v", err)
		return session, nil, nil
	}
	return session, p, nil
}

// prescribeSession applies the modifiers of the microcycle a just-started
// session landed in. Nothing happens for a free-form or off-plan session, an
// unplaced one, or a microcycle without modifiers.
func (s *Service) prescribeSession(ctx context.Context, session Session) (*Prescription, error) {
	if session.PlanID == 0 {
		return nil, nil
	}
	ps, ok := s.store.(PeriodizationStore)
	if !ok {
		return nil, nil
	}
	rs, ok := s.store.(PrescriptionStore)
	if !ok {
		return nil, nil
	}
	link, err := ps.SessionLink(ctx, session.ID)
	if err != nil || !link.Placed || link.OffPlan || link.MicrocycleID == 0 {
		return nil, err
	}
	b, err := ps.GetBlock(ctx, link.BlockID)
	if err != nil {
		return nil, err
	}
	m, ok := microcycleOf(b, link.MicrocycleID)
	if !ok || !hasModifiers(m) {
		return nil, nil
	}
	progress, err := s.store.SessionProgress(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	base := []PrescribedItem{}
	for _, it := range progress {
		if it.TargetSets > 0 {
			base = append(base, PrescribedItem{Exercise: it.Exercise, BaseSets: it.TargetSets, BaseTargetRPE: it.TargetRPE})
		}
	}
	p := prescribe(b, m, session.PlanID, session.PlanName, base)
	if err := rs.ApplyPrescription(ctx, session.ID, p); err != nil {
		return nil, err
	}
	return &p, nil
}

func microcycleOf(b Block, id int64) (Microcycle, bool) {
	for _, m := range b.Microcycles {
		if m.ID == id {
			return m, true
		}
	}
	return Microcycle{}, false
}

// SessionPrescription is the prescription a session kept when it started; nil
// when none was applied or the store keeps none.
func (s *Service) SessionPrescription(ctx context.Context, sessionID int64) (*Prescription, error) {
	if sessionID <= 0 {
		return nil, ErrValidation
	}
	rs, ok := s.store.(PrescriptionStore)
	if !ok {
		return nil, nil
	}
	p, found, err := rs.SessionPrescription(ctx, sessionID)
	if err != nil || !found {
		return nil, err
	}
	return &p, nil
}

// nextPrescription is the first routine due in the current microcycle with
// its modifiers applied to the routine as it is today. A routine deleted
// since it joined the rotation has nothing to show.
func (s *Service) nextPrescription(ctx context.Context, b Block, current *MicrocycleView) (*Prescription, error) {
	if current == nil || len(current.Due) == 0 || current.Due[0].PlanID == 0 {
		return nil, nil
	}
	m, ok := microcycleOf(b, current.ID)
	if !ok {
		return nil, nil
	}
	plan, err := s.store.GetPlan(ctx, current.Due[0].PlanID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := prescribe(b, m, plan.ID, plan.Name, planBase(plan))
	return &p, nil
}

func planBase(plan Plan) []PrescribedItem {
	base := []PrescribedItem{}
	for _, it := range plan.Items {
		base = append(base, PrescribedItem{Exercise: it.Exercise, BaseSets: it.TargetSets, BaseTargetRPE: it.TargetRPE})
	}
	return base
}
