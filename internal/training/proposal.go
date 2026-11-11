package training

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrNotPending is answering a proposal that was already answered.
var ErrNotPending = errors.New("this proposal was already answered")

// Proposal statuses. A proposal is pending until someone answers it; accepting
// moves it through applying, which only lasts while the change is being written.
const (
	ProposalPending  = "pending"
	ProposalApplying = "applying"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"
	ProposalFailed   = "failed"
)

// Who decided a proposal: the person on the review page, or a standing answer.
const (
	DecidedByPerson    = "person"
	DecidedBySession   = "session"
	DecidedByAlways    = "always"
	DecidedByAcceptAll = "accept_all"
)

// How long an accepted answer is remembered for its action type.
const (
	RememberOnce    = "once"
	RememberSession = "session"
	RememberAlways  = "always"
)

// SessionWindow is how long a "session" answer lasts. MCP calls carry no session
// of their own (the server is stateless), so a session is one training day.
const SessionWindow = 12 * time.Hour

const maxProposalPayload = 64 << 10

// Proposal is one change an agent asked for. The payload is what the agent sent;
// the result is what applying it returned.
type Proposal struct {
	ID         int64           `json:"id"`
	Tool       string          `json:"tool"`
	ActionType string          `json:"action_type"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`
	DecidedBy  string          `json:"decided_by,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	DecidedAt  time.Time       `json:"decided_at,omitzero"`
}

// ProposalFilter selects proposals: pending ones, or answered ones (history).
type ProposalFilter struct {
	Pending bool
	Limit   int
}

// ApprovalRule is a remembered answer for one action type. A session rule
// expires; an always rule does not.
type ApprovalRule struct {
	ActionType string
	Remember   string
	ExpiresAt  time.Time
}

func (r ApprovalRule) activeAt(now time.Time) bool {
	return r.Remember == RememberAlways || (r.Remember == RememberSession && now.Before(r.ExpiresAt))
}

// Permissions are a user's standing answers to agent proposals.
type Permissions struct {
	AcceptAll bool
	Rules     []ApprovalRule
}

// ProposalOutcome is the end of applying a proposal.
type ProposalOutcome struct {
	Status    string
	DecidedBy string
	Result    json.RawMessage
	Error     string
	At        time.Time
}

// ApplyFunc writes a proposal's change and returns what the write returned.
type ApplyFunc func(context.Context) (json.RawMessage, error)

// Applier applies a stored proposal through the same code its tool uses.
type Applier func(ctx context.Context, svc *Service, tool string, payload json.RawMessage) (json.RawMessage, error)

// ProposalInput is what an agent's write tool proposes.
type ProposalInput struct {
	Tool       string
	ActionType string
	Payload    json.RawMessage
}

// Propose records an agent's change. When a standing answer covers its action
// type it is applied at once through apply and recorded as accepted; otherwise it
// waits for the person. A failed apply is recorded and its error returned.
func (s *Service) Propose(ctx context.Context, in ProposalInput, apply ApplyFunc) (Proposal, error) {
	if strings.TrimSpace(in.Tool) == "" || strings.TrimSpace(in.ActionType) == "" || len(in.Payload) > maxProposalPayload || !json.Valid(in.Payload) {
		return Proposal{}, ErrValidation
	}
	perms, err := s.store.Permissions(ctx)
	if err != nil {
		return Proposal{}, err
	}
	now := s.clock()
	decidedBy := standingAnswer(perms, in.ActionType, now)
	p, err := s.store.CreateProposal(ctx, Proposal{Tool: in.Tool, ActionType: in.ActionType, Payload: in.Payload, Status: ProposalPending, CreatedAt: now})
	if err != nil || decidedBy == "" {
		return p, err
	}
	return s.applyProposal(ctx, p.ID, decidedBy, apply)
}

func standingAnswer(perms Permissions, actionType string, now time.Time) string {
	if perms.AcceptAll {
		return DecidedByAcceptAll
	}
	for _, r := range perms.Rules {
		if r.ActionType == actionType && r.activeAt(now) {
			return r.Remember
		}
	}
	return ""
}

// AcceptProposal applies a pending proposal and, unless remember is once,
// remembers the answer for its action type.
func (s *Service) AcceptProposal(ctx context.Context, id int64, remember string, apply Applier) (Proposal, error) {
	if remember == "" {
		remember = RememberOnce
	}
	if remember != RememberOnce && remember != RememberSession && remember != RememberAlways {
		return Proposal{}, ErrValidation
	}
	if apply == nil {
		return Proposal{}, ErrValidation
	}
	p, err := s.store.GetProposal(ctx, id)
	if err != nil {
		return Proposal{}, err
	}
	if p.Status != ProposalPending {
		return p, ErrNotPending
	}
	if remember != RememberOnce {
		rule := ApprovalRule{ActionType: p.ActionType, Remember: remember}
		if remember == RememberSession {
			rule.ExpiresAt = s.clock().Add(SessionWindow)
		}
		if err := s.store.SetRule(ctx, rule); err != nil {
			return Proposal{}, err
		}
	}
	return s.applyProposal(ctx, id, DecidedByPerson, func(ctx context.Context) (json.RawMessage, error) {
		return apply(ctx, s, p.Tool, p.Payload)
	})
}

// RejectProposal answers a pending proposal with no; nothing is changed.
func (s *Service) RejectProposal(ctx context.Context, id int64) (Proposal, error) {
	return s.store.RejectProposal(ctx, id, s.clock())
}

func (s *Service) applyProposal(ctx context.Context, id int64, decidedBy string, apply ApplyFunc) (Proposal, error) {
	// Claiming first means two answers to one proposal cannot both apply it.
	if err := s.store.ClaimProposal(ctx, id); err != nil {
		return Proposal{}, err
	}
	result, applyErr := apply(ctx)
	out := ProposalOutcome{Status: ProposalAccepted, DecidedBy: decidedBy, Result: result, At: s.clock()}
	if applyErr != nil {
		out = ProposalOutcome{Status: ProposalFailed, DecidedBy: decidedBy, Error: publicError(applyErr), At: s.clock()}
	}
	p, err := s.store.FinishProposal(ctx, id, out)
	if applyErr != nil {
		return p, applyErr
	}
	return p, err
}

// publicError is what may be stored and shown about a failed apply: the
// domain's own messages, never an internal one.
func publicError(err error) string {
	for _, known := range []error{ErrValidation, ErrNotFound, ErrIdempotencyConflict} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "internal error"
}

func (s *Service) GetProposal(ctx context.Context, id int64) (Proposal, error) {
	return s.store.GetProposal(ctx, id)
}

func (s *Service) ListProposals(ctx context.Context, f ProposalFilter) ([]Proposal, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	return s.store.ListProposals(ctx, f)
}

// Permissions returns the standing answers, leaving out session rules that ended.
func (s *Service) Permissions(ctx context.Context) (Permissions, error) {
	perms, err := s.store.Permissions(ctx)
	if err != nil {
		return Permissions{}, err
	}
	now := s.clock()
	active := perms.Rules[:0]
	for _, r := range perms.Rules {
		if r.activeAt(now) {
			active = append(active, r)
		}
	}
	perms.Rules = active
	return perms, nil
}

// SetAcceptAll turns on or off accepting every proposal, whatever its action type.
func (s *Service) SetAcceptAll(ctx context.Context, on bool) error {
	return s.store.SetAcceptAll(ctx, on)
}

// ForgetRule drops the remembered answer for an action type.
func (s *Service) ForgetRule(ctx context.Context, actionType string) error {
	if strings.TrimSpace(actionType) == "" {
		return ErrValidation
	}
	return s.store.ForgetRule(ctx, actionType)
}
