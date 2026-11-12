package training

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Correcting past sets typed in lb but stored as kg. Before units, a set lifted
// with 135 lb was logged as 135 (kg). The correction stores the number again as
// lb, the same conversion a set typed in lb gets (ToKG), and marks the set so it
// is never converted twice. The person previews a scope (one session, one
// exercise between two dates, or chosen sets), then confirms the sets to fix;
// an agent proposes the same through a write tool. Either way it is applied as
// a proposal, so the change is in the history and can be undone.

// MaxLbCorrection is the most sets one correction converts.
const MaxLbCorrection = 500

// LbCorrectionTool and LbCorrectionAction name the change in the history.
const (
	LbCorrectionTool   = "correct_lb_sets"
	LbCorrectionAction = "set.correct_lb"
)

// ErrLbCorrected refuses a correction of a set already corrected from lb.
var ErrLbCorrected = fmt.Errorf("%w: a set was already corrected from lb", ErrValidation)

// ErrLbChanged refuses a correction of a set whose weight changed since the
// person previewed it.
var ErrLbChanged = fmt.Errorf("%w: a set changed since the preview", ErrValidation)

// LbScope picks the sets to preview: one session, one exercise between two
// dates (inclusive), or sets by id. Exactly one kind.
type LbScope struct {
	SessionID int64   `json:"session_id,omitempty"`
	Exercise  string  `json:"exercise,omitempty"`
	From      string  `json:"from,omitempty"`
	To        string  `json:"to,omitempty"`
	SetIDs    []int64 `json:"set_ids,omitempty"`
}

// LbSet is one stored set as a correction sees it.
type LbSet struct {
	SetID     int64   `json:"set_id"`
	SessionID int64   `json:"session_id"`
	Date      string  `json:"date"`
	Exercise  string  `json:"exercise"`
	Reps      int     `json:"reps"`
	OldKG     float64 `json:"old_kg"`
	NewKG     float64 `json:"new_kg"`
	// Corrected marks a set already corrected: it is listed, never converted.
	Corrected bool `json:"corrected,omitempty"`
}

// LbPreview lists every set in a scope with its weight now and after the
// correction; Count is how many would change.
type LbPreview struct {
	Sets      []LbSet `json:"sets"`
	Count     int     `json:"count"`
	Corrected int     `json:"already_corrected"`
}

// LbCorrection is what a correction did.
type LbCorrection struct {
	Sets  []LbSet `json:"sets"`
	Count int     `json:"count"`
}

func (sc LbScope) normalize() (LbScope, error) {
	sc.Exercise = strings.ToLower(strings.TrimSpace(sc.Exercise))
	kinds := 0
	if sc.SessionID != 0 {
		kinds++
		if sc.SessionID < 0 {
			return sc, ErrValidation
		}
	}
	if sc.Exercise != "" || sc.From != "" || sc.To != "" {
		kinds++
		if sc.Exercise == "" || !validDate(sc.From) || !validDate(sc.To) || sc.From > sc.To {
			return sc, fmt.Errorf("%w: an exercise needs from and to dates, from not after to", ErrValidation)
		}
	}
	if len(sc.SetIDs) > 0 {
		kinds++
		if len(sc.SetIDs) > MaxLbCorrection {
			return sc, fmt.Errorf("%w: at most %d sets", ErrValidation, MaxLbCorrection)
		}
		seen := map[int64]bool{}
		for _, id := range sc.SetIDs {
			if id <= 0 || seen[id] {
				return sc, fmt.Errorf("%w: set ids must be positive and distinct", ErrValidation)
			}
			seen[id] = true
		}
	}
	if kinds != 1 {
		return sc, fmt.Errorf("%w: choose one session, one exercise with dates, or sets", ErrValidation)
	}
	return sc, nil
}

// LbCorrectionPreview lists the person's sets in a scope with their weight now
// and as corrected. It changes nothing. Another person's ids match nothing.
func (s *Service) LbCorrectionPreview(ctx context.Context, scope LbScope) (LbPreview, error) {
	scope, err := scope.normalize()
	if err != nil {
		return LbPreview{}, err
	}
	if scope.SessionID > 0 {
		// Another person's session is missing, as it is everywhere.
		if _, err := s.GetSession(ctx, scope.SessionID); err != nil {
			return LbPreview{}, err
		}
	}
	sets, err := s.store.LbSets(ctx, scope)
	if err != nil {
		return LbPreview{}, err
	}
	if len(scope.SetIDs) > 0 && len(sets) != len(scope.SetIDs) {
		return LbPreview{}, ErrNotFound
	}
	out := LbPreview{Sets: []LbSet{}}
	if len(sets) > MaxLbCorrection {
		return out, fmt.Errorf("%w: more than %d sets; choose a shorter range", ErrValidation, MaxLbCorrection)
	}
	for _, set := range sets {
		set.NewKG = set.OldKG
		if set.Corrected {
			out.Corrected++
		} else {
			set.NewKG = ToKG(set.OldKG, UnitLB)
			out.Count++
		}
		out.Sets = append(out.Sets, set)
	}
	return out, nil
}

// CorrectLbSets converts the stored weight of the person's chosen sets from lb
// to kg, all or nothing: an id that is not theirs is ErrNotFound, and a set
// already corrected refuses the whole correction with ErrLbCorrected. Only
// weight_kg and the correction mark change.
func (s *Service) CorrectLbSets(ctx context.Context, setIDs []int64) (LbCorrection, error) {
	return s.CorrectLbSetsFrom(ctx, setIDs, nil)
}

// CorrectLbSetsFrom is CorrectLbSets for a correction the person previewed:
// previewed holds the stored kg each set showed, and a set that no longer
// weighs it (or has none) refuses the whole correction with ErrLbChanged, so
// a set edited after the preview is never converted from its newer value.
// A nil previewed checks nothing, as an agent's correction.
func (s *Service) CorrectLbSetsFrom(ctx context.Context, setIDs []int64, previewed map[int64]float64) (LbCorrection, error) {
	scope, err := LbScope{SetIDs: setIDs}.normalize()
	if err != nil {
		return LbCorrection{}, err
	}
	sets, err := s.store.LbSets(ctx, scope)
	if err != nil {
		return LbCorrection{}, err
	}
	if len(sets) != len(scope.SetIDs) {
		return LbCorrection{}, ErrNotFound
	}
	for i := range sets {
		if sets[i].Corrected {
			return LbCorrection{}, ErrLbCorrected
		}
		if previewed != nil {
			if old, ok := previewed[sets[i].SetID]; !ok || RoundWeight(old) != sets[i].OldKG {
				return LbCorrection{}, ErrLbChanged
			}
		}
		sets[i].NewKG = ToKG(sets[i].OldKG, UnitLB)
	}
	if err := s.store.CorrectLbSets(ctx, sets, s.clock()); err != nil {
		return LbCorrection{}, err
	}
	return LbCorrection{Sets: sets, Count: len(sets)}, nil
}

// ApplyAsPerson records a change the person makes in the app as a proposal they
// decided at once, and applies it with its undo image, so it shows in the
// history and can be undone like an accepted proposal.
func (s *Service) ApplyAsPerson(ctx context.Context, in ProposalInput, apply ApplyFunc) (Proposal, error) {
	if strings.TrimSpace(in.Tool) == "" || strings.TrimSpace(in.ActionType) == "" || len(in.Payload) > maxProposalPayload || !json.Valid(in.Payload) {
		return Proposal{}, ErrValidation
	}
	p, err := s.store.CreateProposal(ctx, Proposal{Tool: in.Tool, ActionType: in.ActionType, Payload: in.Payload, Status: ProposalPending, CreatedAt: s.clock()})
	if err != nil {
		return p, err
	}
	return s.applyProposal(ctx, p.ID, p.Payload, DecidedByPerson, personRecord(p, OutcomeAllow, AnswerAllowOnce), nil, apply)
}
