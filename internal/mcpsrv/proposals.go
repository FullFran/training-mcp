package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProposalRef says what became of a write: every agent write is a proposal the
// person may have to accept first.
type ProposalRef struct {
	Status     string `json:"status" jsonschema:"'accepted': the change is applied and the other fields show the result. 'pending': the change waits for the person to accept it in the app; nothing is applied yet, the other fields are empty, and it is validated only when accepted."`
	ProposalID int64  `json:"proposal_id" jsonschema:"ID of the proposal that records this change."`
}

func (p *ProposalRef) setProposal(r ProposalRef) { *p = r }

// pendingNote ends every write tool's description, so an agent never reports a
// pending change as done.
const pendingNote = " Every write is a proposal: the result's status is 'accepted' when it was applied, or 'pending' when it waits for the person to accept it in the app. A pending change is not applied yet; say so instead of reporting it as done."

// writeAction applies a stored proposal of one tool.
type writeAction func(ctx context.Context, svc *training.Service, payload json.RawMessage) (json.RawMessage, error)

// writes registers write tools and remembers how to apply each one's proposals,
// so accepting later goes through the very code the tool runs.
type writes struct {
	server  *mcp.Server
	service training.ServiceFor
	actions map[string]writeAction
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
	addTool(w.server, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var out Out
		svc := w.service(ctx)
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
		if p.Status == training.ProposalAccepted {
			status = "accepted"
		}
		PO(&out).setProposal(ProposalRef{Status: status, ProposalID: p.ID})
		return nil, out, nil
	})
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
