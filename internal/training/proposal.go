package training

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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

// Who decided a proposal: the person, a session-limited answer the person gave
// earlier (a grant or a denial), or the account's accept-all grant. "session"
// and "always" only appear on proposals decided before migration 018.
const (
	DecidedByPerson       = "person"
	DecidedBySessionGrant = "session_grant"
	DecidedByDenial       = "session_denial"
	DecidedByAcceptAll    = "accept_all"
	DecidedBySession      = "session"
	DecidedByAlways       = "always"
)

// A person's answers to a proposal, the option kinds of HCS-0005 section 3.2.
// The once answers decide one proposal and are never stored (R005); the always
// answers are stored limited to the proposal's session (R006, R007).
const (
	AnswerAllowOnce    = "allow_once"
	AnswerRejectOnce   = "reject_once"
	AnswerAllowAlways  = "allow_always"
	AnswerRejectAlways = "reject_always"
)

// Effects of a stored answer: a grant allows, a denial rejects. A denial wins
// over a grant (R007).
const (
	EffectAllow = "allow"
	EffectDeny  = "deny"
)

// SessionWindow bounds every session-limited answer. For an external MCP client
// it is the session itself: the endpoint is stateless, so its calls carry no
// session of their own and one session is the 12 hours after the answer.
const SessionWindow = 12 * time.Hour

const maxProposalPayload = 64 << 10

// Agent sessions. A check is made inside one of these, or inside none.
const (
	// ExternalSession is every call of an external MCP client (ChatGPT, Claude):
	// a stateless endpoint gives them no session id, so their session is a time
	// window, which SessionWindow ends.
	ExternalSession = "mcp"
	// CoachSessionPrefix starts the session of one in-app coach conversation.
	CoachSessionPrefix = "coach:"
)

type agentSessionKey struct{}

// WithAgentSession records which agent session a request belongs to. Only the
// code that knows the channel the request came through may call it; a request
// without one is outside every session (HCS-0005 R008).
func WithAgentSession(ctx context.Context, session string) context.Context {
	return context.WithValue(ctx, agentSessionKey{}, session)
}

// AgentSessionFrom returns the request's agent session, "" for none.
func AgentSessionFrom(ctx context.Context) string {
	session, _ := ctx.Value(agentSessionKey{}).(string)
	return session
}

// Proposal is one change an agent asked for. The payload is what the agent sent;
// the result is what applying it returned. Session is the agent session it was
// asked in, "" for none.
type Proposal struct {
	ID         int64           `json:"id"`
	Tool       string          `json:"tool"`
	ActionType string          `json:"action_type"`
	Payload    json.RawMessage `json:"payload"`
	Session    string          `json:"session,omitempty"`
	Status     string          `json:"status"`
	DecidedBy  string          `json:"decided_by,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	DecidedAt  time.Time       `json:"decided_at,omitzero"`
	// Undoable says it is accepted with an undo image and was not undone yet.
	Undoable bool `json:"undoable,omitempty"`
	// UndoneBy is the history entry that undid it, 0 while it stands.
	UndoneBy int64 `json:"undone_by,omitempty"`
}

// ProposalFilter selects proposals: pending ones, or answered ones (history).
type ProposalFilter struct {
	Pending bool
	Limit   int
}

// AnswerRule is a stored always answer: a grant or a denial for one action type,
// limited to the session it was given in and to SessionWindow. Origin is the
// decision record of the answer that created it.
type AnswerRule struct {
	ID         int64
	ActionType string
	Effect     string
	Session    string
	ExpiresAt  time.Time
	Origin     int64
}

// matches is HCS-0005's match: same action type and same session. A rule never
// matches a check without a session (R008).
func (r AnswerRule) matches(actionType, session string, now time.Time) bool {
	return session != "" && r.Session == session && r.ActionType == actionType && now.Before(r.ExpiresAt)
}

// Permissions are a user's standing answers to agent proposals. AcceptAll is an
// account-level host grant: set only from the person's own settings, it allows
// every action type in every session, and each proposal is still recorded.
type Permissions struct {
	AcceptAll bool
	Rules     []AnswerRule
}

// Decision record outcomes, decider kinds and channels (HCS-0005 section 3.3).
const (
	OutcomeAllow   = "allow"
	OutcomeDeny    = "deny"
	OutcomeRevoked = "revoked"

	DeciderSubject = "subject"
	DeciderGrant   = "grant"
	DeciderDenial  = "denial"
	DeciderPolicy  = "policy"

	// ChannelWeb is the person's own channel: the signed-in web app (review page
	// and chat cards), never text from the model or a tool (R015).
	ChannelWeb = "web"
	// ChannelGrantStore is a decision a stored grant or denial made.
	ChannelGrantStore = "grant-store"

	// AcceptAllGrant is the decider id of the account's accept-all grant.
	AcceptAllGrant = "accept_all"
	// AccountHolder is the decider id of the person who holds the account.
	AccountHolder = "account"
)

// DecisionRecord says who allowed or denied an agent write, when and through
// which channel. It never holds the proposal's payload or result (R011): the
// operation is only the proposal's id. Permission is the action type; the entry
// is always this server.
type DecisionRecord struct {
	ID          int64
	Time        time.Time
	Permission  string
	Outcome     string
	Operation   string
	Session     string
	DeciderKind string
	DeciderID   string
	Channel     string
	Answer      string
}

// ProposalOutcome is the end of applying a proposal.
type ProposalOutcome struct {
	Status    string
	DecidedBy string
	Result    json.RawMessage
	Error     string
	At        time.Time
	// UndoImage is what undoing it needs; empty when it cannot be undone.
	UndoImage json.RawMessage
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

// Propose records an agent's change in the request's agent session and decides
// it as HCS-0005 does: a matching denial rejects it, otherwise the accept-all
// grant or a matching grant applies it at once through apply, otherwise it waits
// for the person. Every decision gets a decision record before it takes effect;
// a failed apply is recorded and its error returned.
func (s *Service) Propose(ctx context.Context, in ProposalInput, apply ApplyFunc) (Proposal, error) {
	if strings.TrimSpace(in.Tool) == "" || strings.TrimSpace(in.ActionType) == "" || len(in.Payload) > maxProposalPayload || !json.Valid(in.Payload) {
		return Proposal{}, ErrValidation
	}
	perms, err := s.store.Permissions(ctx)
	if err != nil {
		return Proposal{}, err
	}
	now := s.clock()
	session := AgentSessionFrom(ctx)
	p, err := s.store.CreateProposal(ctx, Proposal{Tool: in.Tool, ActionType: in.ActionType, Payload: in.Payload, Session: session, Status: ProposalPending, CreatedAt: now})
	if err != nil {
		return p, err
	}
	var grant, denial *AnswerRule
	for i, r := range perms.Rules {
		if !r.matches(in.ActionType, session, now) {
			continue
		}
		if r.Effect == EffectDeny && denial == nil {
			denial = &perms.Rules[i]
		}
		if r.Effect == EffectAllow && grant == nil {
			grant = &perms.Rules[i]
		}
	}
	record := DecisionRecord{Permission: p.ActionType, Operation: operationID(p.ID), Session: session, Channel: ChannelGrantStore}
	switch {
	case denial != nil:
		record.Outcome, record.DeciderKind, record.DeciderID = OutcomeDeny, DeciderDenial, ruleID(denial.ID)
		rejected, _, err := s.reject(ctx, p.ID, DecidedByDenial, record)
		return rejected, err
	case perms.AcceptAll:
		record.Outcome, record.DeciderKind, record.DeciderID = OutcomeAllow, DeciderGrant, AcceptAllGrant
		return s.applyProposal(ctx, p.ID, p.Payload, DecidedByAcceptAll, record, nil, apply)
	case grant != nil:
		record.Outcome, record.DeciderKind, record.DeciderID = OutcomeAllow, DeciderGrant, ruleID(grant.ID)
		return s.applyProposal(ctx, p.ID, p.Payload, DecidedBySessionGrant, record, nil, apply)
	}
	return p, nil
}

func operationID(proposalID int64) string { return "proposal:" + strconv.FormatInt(proposalID, 10) }
func ruleID(id int64) string              { return "rule:" + strconv.FormatInt(id, 10) }

// AcceptProposal is the person allowing a pending proposal: allow_once applies
// it and stores nothing; allow_always also stores a grant for its action type,
// limited to the session it was asked in. A proposal asked outside any session
// can only be answered once.
func (s *Service) AcceptProposal(ctx context.Context, id int64, answer string, apply Applier) (Proposal, error) {
	if answer == "" {
		answer = AnswerAllowOnce
	}
	if answer != AnswerAllowOnce && answer != AnswerAllowAlways {
		return Proposal{}, ErrValidation
	}
	if apply == nil {
		return Proposal{}, ErrValidation
	}
	p, err := s.pendingForAnswer(ctx, id, answer)
	if err != nil {
		return p, err
	}
	record := personRecord(p, OutcomeAllow, answer)
	var rule *AnswerRule
	if answer == AnswerAllowAlways {
		rule = &AnswerRule{ActionType: p.ActionType, Effect: EffectAllow, Session: p.Session, ExpiresAt: s.clock().Add(SessionWindow)}
	}
	return s.applyProposal(ctx, id, p.Payload, DecidedByPerson, record, rule, func(ctx context.Context) (json.RawMessage, error) {
		return apply(ctx, s, p.Tool, p.Payload)
	})
}

// RejectProposal is the person rejecting a pending proposal; nothing is changed.
// reject_always also stores a denial for its action type, limited like a grant,
// which rejects every later proposal it matches even when a grant matches too.
func (s *Service) RejectProposal(ctx context.Context, id int64, answer string) (Proposal, error) {
	if answer == "" {
		answer = AnswerRejectOnce
	}
	if answer != AnswerRejectOnce && answer != AnswerRejectAlways {
		return Proposal{}, ErrValidation
	}
	p, err := s.pendingForAnswer(ctx, id, answer)
	if err != nil {
		return p, err
	}
	rejected, origin, err := s.reject(ctx, id, DecidedByPerson, personRecord(p, OutcomeDeny, answer))
	if err != nil || answer != AnswerRejectAlways {
		return rejected, err
	}
	_, err = s.store.AddRule(ctx, AnswerRule{ActionType: p.ActionType, Effect: EffectDeny, Session: p.Session, ExpiresAt: s.clock().Add(SessionWindow), Origin: origin})
	return rejected, err
}

// pendingForAnswer loads a proposal the person is answering and refuses an
// always answer for one asked outside any session, which no stored answer could
// ever match.
func (s *Service) pendingForAnswer(ctx context.Context, id int64, answer string) (Proposal, error) {
	p, err := s.store.GetProposal(ctx, id)
	if err != nil {
		return Proposal{}, err
	}
	if p.Status != ProposalPending {
		return p, ErrNotPending
	}
	if (answer == AnswerAllowAlways || answer == AnswerRejectAlways) && p.Session == "" {
		return Proposal{}, ErrValidation
	}
	return p, nil
}

// personRecord is the record of the person's own answer. Only the account's
// holder can answer its proposals, and the record is stored under the account,
// so the decider is the account holder.
func personRecord(p Proposal, outcome, answer string) DecisionRecord {
	return DecisionRecord{Permission: p.ActionType, Outcome: outcome, Operation: operationID(p.ID), Session: p.Session,
		DeciderKind: DeciderSubject, DeciderID: AccountHolder, Channel: ChannelWeb, Answer: answer}
}

// reject rejects a pending proposal and records the decision, returning the
// record's id so a reject_always can name it as its origin.
func (s *Service) reject(ctx context.Context, id int64, decidedBy string, record DecisionRecord) (Proposal, int64, error) {
	record.Time = s.clock()
	p, err := s.store.RejectProposal(ctx, id, decidedBy, record.Time)
	if err != nil {
		return Proposal{}, 0, err
	}
	recordID, err := s.store.RecordDecision(ctx, record)
	return p, recordID, err
}

// applyProposal claims a pending proposal, records the allow decision, stores
// the grant an allow_always creates and only then applies the change. A record
// or grant that cannot be written denies the operation (HCS-0005 R012). The
// change is applied with its undo image, so the person can undo it later.
func (s *Service) applyProposal(ctx context.Context, id int64, payload json.RawMessage, decidedBy string, record DecisionRecord, grant *AnswerRule, apply ApplyFunc) (Proposal, error) {
	// Claiming first means two answers to one proposal cannot both apply it.
	if err := s.store.ClaimProposal(ctx, id); err != nil {
		return Proposal{}, err
	}
	record.Time = s.clock()
	recordID, err := s.store.RecordDecision(ctx, record)
	if err == nil && grant != nil {
		grant.Origin = recordID
		_, err = s.store.AddRule(ctx, *grant)
	}
	if err != nil {
		p, finishErr := s.store.FinishProposal(ctx, id, ProposalOutcome{Status: ProposalFailed, DecidedBy: decidedBy, Error: publicError(err), At: s.clock()})
		return p, errors.Join(err, finishErr)
	}
	result, image, applyErr := s.capture(ctx, payload, apply)
	out := ProposalOutcome{Status: ProposalAccepted, DecidedBy: decidedBy, Result: result, At: s.clock(), UndoImage: image}
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
	for _, known := range []error{ErrValidation, ErrNotFound, ErrIdempotencyConflict, ErrStale, ErrNotPending} {
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

// Permissions returns the standing answers, leaving out rules whose window ended.
func (s *Service) Permissions(ctx context.Context) (Permissions, error) {
	perms, err := s.store.Permissions(ctx)
	if err != nil {
		return Permissions{}, err
	}
	now := s.clock()
	active := perms.Rules[:0]
	for _, r := range perms.Rules {
		if now.Before(r.ExpiresAt) {
			active = append(active, r)
		}
	}
	perms.Rules = active
	return perms, nil
}

// SetAcceptAll turns the account's accept-all grant on or off. Only the person's
// own settings call it.
func (s *Service) SetAcceptAll(ctx context.Context, on bool) error {
	return s.store.SetAcceptAll(ctx, on)
}

// ForgetRule revokes a stored grant or denial: every proposal after it is decided
// without it (R003). The revocation gets its own decision record (R010).
func (s *Service) ForgetRule(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrValidation
	}
	rule, err := s.store.ForgetRule(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.store.RecordDecision(ctx, DecisionRecord{Time: s.clock(), Permission: rule.ActionType, Outcome: OutcomeRevoked, Session: rule.Session,
		DeciderKind: DeciderSubject, DeciderID: AccountHolder, Channel: ChannelWeb})
	return err
}

// Decisions lists the decision records newest first.
func (s *Service) Decisions(ctx context.Context, limit int) ([]DecisionRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.store.Decisions(ctx, limit)
}
