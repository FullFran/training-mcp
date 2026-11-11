package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProposalRef says what became of a write: every agent write is a proposal the
// person may have to accept first.
type ProposalRef struct {
	Status     string `json:"status" jsonschema:"'accepted': the change is applied and the other fields show the result. 'pending': the change waits for the person to accept it in the app; nothing is applied yet, the other fields are empty, and it is validated only when accepted. 'rejected': the person already said to reject this kind of change in this session; nothing is applied and the other fields are empty."`
	ProposalID int64  `json:"proposal_id" jsonschema:"ID of the proposal that records this change."`
}

func (p *ProposalRef) setProposal(r ProposalRef) { *p = r }

// pendingNote ends every write tool's description, so an agent never reports a
// pending change as done.
const pendingNote = " Every write is a proposal: the result's status is 'accepted' when it was applied, 'pending' when it waits for the person to accept it in the app, or 'rejected' when the person already rejected this kind of change for this session. A pending or rejected change is not applied; say so instead of reporting it as done."

// writeAction applies a stored proposal of one tool.
type writeAction func(ctx context.Context, svc *training.Service, payload json.RawMessage) (json.RawMessage, error)

// writes registers write tools and remembers how to apply each one's proposals,
// so accepting later goes through the very code the tool runs.
type writes struct {
	server  *mcp.Server
	service training.ServiceFor
	actions map[string]writeAction
	// coachSessions resolves an engine bearer to its HCS-0005 session (the
	// stored conversation it serves). Nil: the session is the bearer's hash.
	coachSessions atomic.Pointer[CoachSessions]
}

// CoachSessions resolves a live engine bearer to the session of the coach
// conversation it serves; false when the bearer is unknown.
type CoachSessions func(token string) (string, bool)

// SetCoachSessions makes coach proposals belong to the stored conversation
// they were made in, the same session connector approvals use.
func (s *Server) SetCoachSessions(f CoachSessions) {
	if f == nil {
		s.writes.coachSessions.Store(nil)
		return
	}
	s.writes.coachSessions.Store(&f)
}

func addWrite[In, Out any, PO interface {
	*Out
	setProposal(ProposalRef)
}](w *writes, t *mcp.Tool, actionType string, apply func(context.Context, *training.Service, In) (Out, error)) {
	addWriteBy[In, Out, PO](w, t, func(In) string { return actionType }, apply)
}

// addWriteBy is addWrite for a tool whose action type depends on its input.
func addWriteBy[In, Out any, PO interface {
	*Out
	setProposal(ProposalRef)
}](w *writes, t *mcp.Tool, actionType func(In) string, apply func(context.Context, *training.Service, In) (Out, error)) {
	t.Description += pendingNote
	w.actions[t.Name] = func(ctx context.Context, svc *training.Service, payload json.RawMessage) (json.RawMessage, error) {
		var in In
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, training.ErrValidation
		}
		out, err := apply(ctx, svc, in)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	addTool(w.server, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var out Out
		svc := w.service(ctx)
		ctx = training.WithAgentSession(ctx, agentSession(req, w.coachSessions.Load()))
		payload, err := json.Marshal(in)
		if err != nil {
			return nil, out, toolError(err)
		}
		p, err := svc.Propose(ctx, training.ProposalInput{Tool: t.Name, ActionType: actionType(in), Payload: payload},
			func(ctx context.Context) (json.RawMessage, error) {
				v, err := apply(ctx, svc, in)
				if err != nil {
					return nil, err
				}
				out = v
				return json.Marshal(v)
			})
		if err != nil {
			return nil, out, toolError(err)
		}
		status := "pending"
		switch p.Status {
		case training.ProposalAccepted:
			status = "accepted"
		case training.ProposalRejected:
			status = "rejected"
		}
		PO(&out).setProposal(ProposalRef{Status: status, ProposalID: p.ID})
		return nil, out, nil
	})
}

// agentSession is the session a tool call belongs to, for HCS-0005's
// session-limited answers. The in-app coach's engine reaches /mcp with a bearer
// minted for one engine process; the coach resolves it to the stored
// conversation that process serves, so a chat keeps its answers across
// processes. A bearer the coach cannot place (no stored conversation) is its
// own session, derived from the bearer (hashed, never stored as is). Every
// other HTTP call comes from an external MCP client on a stateless endpoint,
// which carries no session id, so all of them share the external session that
// the 12-hour window ends. A call that did not come over HTTP has no session,
// and no stored answer matches it.
func agentSession(req *mcp.CallToolRequest, sessions *CoachSessions) string {
	if req == nil || req.Extra == nil || req.Extra.Header == nil {
		return ""
	}
	bearer, ok := strings.CutPrefix(req.Extra.Header.Get("Authorization"), "Bearer ")
	if bearer = strings.TrimSpace(bearer); ok && engine.LooksLikeToken(bearer) {
		if sessions != nil {
			if session, found := (*sessions)(bearer); found && strings.HasPrefix(session, training.CoachSessionPrefix) {
				return session
			}
		}
		sum := sha256.Sum256([]byte(bearer))
		return training.CoachSessionPrefix + hex.EncodeToString(sum[:12])
	}
	return training.ExternalSession
}

// Apply applies a stored proposal through its tool's code. It is the Applier the
// review page accepts proposals with.
func (s *Server) Apply(ctx context.Context, svc *training.Service, tool string, payload json.RawMessage) (json.RawMessage, error) {
	apply, ok := s.writes.actions[tool]
	if !ok {
		return nil, fmt.Errorf("%w: no write tool %q", training.ErrValidation, tool)
	}
	return apply(ctx, svc, payload)
}

// WriteTools lists the tools whose calls become proposals.
func (s *Server) WriteTools() []string {
	out := make([]string, 0, len(s.writes.actions))
	for name := range s.writes.actions {
		out = append(out, name)
	}
	return out
}
