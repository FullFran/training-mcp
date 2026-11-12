package training

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Undo. Accepting a proposal records every row it changed, as it was before and
// right after (its undo image). Undoing puts the before rows back, only when
// nothing has built on the change since:
//
//   - every row the proposal changed is still exactly as it left it, so a set
//     edited, a plan renamed or a memory rewritten afterwards refuses the undo;
//   - nothing added later refers to a row the undo would remove, such as a set
//     logged in a session the proposal started, or a session started from a plan
//     it created;
//   - every row it would bring back still has its place, so a set logged where a
//     deleted one stood refuses its return.
//
// A refusal changes nothing and says which kind of row stands in the way. Only
// the person undoes, from the web app; no tool lets an agent do it.

// ErrNotUndoable is undoing a proposal that holds no change to undo: one not
// accepted, accepted before undo existed, or an undo itself.
var ErrNotUndoable = errors.New("this proposal cannot be undone")

// ErrUndone is undoing a proposal that was already undone.
var ErrUndone = errors.New("this proposal was already undone")

// Why an undo is refused.
const (
	// UndoChanged: a row the proposal changed was changed again afterwards.
	UndoChanged = "changed"
	// UndoDepended: something added later refers to a row the undo would remove.
	UndoDepended = "depended"
	// UndoOccupied: a row the undo would bring back lost its place to a later one.
	UndoOccupied = "occupied"
)

// UndoConflict refuses an undo because later changes depend on the undone one.
// Table names the kind of row in the way: sets, sessions, plans, plan_items,
// session_items, session_feedback, exercise_notes, exercise_groups,
// volume_landmarks, users (the rest target), idempotency_keys, imported_sets,
// coach_memory, coach_memory_revisions or coach_memory_relations.
type UndoConflict struct {
	Reason string
	Table  string
}

func (e *UndoConflict) Error() string {
	switch e.Reason {
	case UndoDepended:
		return fmt.Sprintf("cannot undo: a later change uses a row of %s it would remove", e.Table)
	case UndoOccupied:
		return fmt.Sprintf("cannot undo: a later row of %s took the place of one it would restore", e.Table)
	}
	return fmt.Sprintf("cannot undo: a row of %s changed after it", e.Table)
}

// The history entry an undo is recorded as.
const (
	UndoTool   = "undo"
	UndoAction = "proposal.undo"
)

// UndoScope names the sessions a change may touch beyond adding rows: those its
// payload names by session_id, and the sessions of the sets it names by set_id.
// Every other kind of row is captured whole, and rows added are found by id.
type UndoScope struct {
	SessionIDs []int64
	SetIDs     []int64
}

// UndoStore is the part of a store that can undo. A store without it applies
// proposals as before, and none of them can be undone.
type UndoStore interface {
	// CaptureUndo runs apply and returns its result with the undo image of what
	// it changed. A failure to read the state before refuses the change; a
	// failure after it keeps the change, with no image.
	CaptureUndo(ctx context.Context, scope UndoScope, apply ApplyFunc) (result, image json.RawMessage, err error)
	// UndoProposal restores the state before an accepted proposal, records entry
	// as the history entry that did it and record as its decision, all or
	// nothing. It sets record.Operation to the entry's operation.
	UndoProposal(ctx context.Context, id int64, entry Proposal, record DecisionRecord) (Proposal, error)
}

// OperationID is the operation a decision record names for a proposal.
func OperationID(proposalID int64) string { return operationID(proposalID) }

// undoScope reads the sessions and sets a payload names.
func undoScope(payload json.RawMessage) UndoScope {
	var fields struct {
		SessionID int64 `json:"session_id"`
		SetID     int64 `json:"set_id"`
	}
	var scope UndoScope
	if json.Unmarshal(payload, &fields) != nil {
		return scope
	}
	if fields.SessionID > 0 {
		scope.SessionIDs = append(scope.SessionIDs, fields.SessionID)
	}
	if fields.SetID > 0 {
		scope.SetIDs = append(scope.SetIDs, fields.SetID)
	}
	return scope
}

// capture applies a proposal's change, recording its undo image when the store
// can undo.
func (s *Service) capture(ctx context.Context, payload json.RawMessage, apply ApplyFunc) (json.RawMessage, json.RawMessage, error) {
	us, ok := s.store.(UndoStore)
	if !ok {
		result, err := apply(ctx)
		return result, nil, err
	}
	return us.CaptureUndo(ctx, undoScope(payload), apply)
}

// UndoProposal is the person undoing an accepted proposal from the web app. It
// is their own action, so it applies at once, as its own history entry with a
// decision record. It is refused with an UndoConflict when later changes depend
// on the undone one.
func (s *Service) UndoProposal(ctx context.Context, id int64) (Proposal, error) {
	if id <= 0 {
		return Proposal{}, ErrValidation
	}
	us, ok := s.store.(UndoStore)
	if !ok {
		return Proposal{}, ErrNotUndoable
	}
	now := s.clock()
	payload, err := json.Marshal(map[string]int64{"proposal_id": id})
	if err != nil {
		return Proposal{}, err
	}
	entry := Proposal{Tool: UndoTool, ActionType: UndoAction, Payload: payload, Status: ProposalAccepted, DecidedBy: DecidedByPerson, CreatedAt: now, DecidedAt: now}
	record := DecisionRecord{Time: now, Permission: UndoAction, Outcome: OutcomeAllow, DeciderKind: DeciderSubject, DeciderID: AccountHolder, Channel: ChannelWeb}
	return us.UndoProposal(ctx, id, entry, record)
}
