package mcpsrv

import (
	"context"
	"fmt"
	"net/http"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	mcp     *mcp.Server
	handler *mcp.StreamableHTTPHandler
}
type StartInput struct {
	Date string `json:"date,omitempty"`
}
type SessionOut struct {
	Session training.Session `json:"session"`
}
type AddInput struct {
	SessionID int64   `json:"session_id"`
	Exercise  string  `json:"exercise"`
	WeightKG  float64 `json:"weight_kg"`
	Reps      int     `json:"reps"`
	RPE       float64 `json:"rpe"`
}
type SetOut struct {
	Set     training.Set `json:"set"`
	TotalSI float64      `json:"total_si"`
}
type UpdateInput struct {
	SetID    int64    `json:"set_id"`
	Exercise *string  `json:"exercise,omitempty"`
	WeightKG *float64 `json:"weight_kg,omitempty"`
	Reps     *int     `json:"reps,omitempty"`
	RPE      *float64 `json:"rpe,omitempty"`
}
type DeleteInput struct {
	SetID int64 `json:"set_id"`
}
type DeleteOut struct {
	SessionID int64   `json:"session_id"`
	TotalSI   float64 `json:"total_si"`
	Remaining int     `json:"remaining"`
}
type SessionInput struct {
	SessionID int64 `json:"session_id"`
}
type ListInput struct {
	Limit int    `json:"limit,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}
type ListOut struct {
	Sessions []training.SessionSummary `json:"sessions"`
}

func New(service *training.Service) *Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "training-mcp", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "start_session", Description: "Start a training session"}, func(ctx context.Context, _ *mcp.CallToolRequest, in StartInput) (*mcp.CallToolResult, SessionOut, error) {
		v, err := service.StartSession(ctx, in.Date)
		return nil, SessionOut{Session: v}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "add_set", Description: "Add a training set"}, func(ctx context.Context, _ *mcp.CallToolRequest, in AddInput) (*mcp.CallToolResult, SetOut, error) {
		v, total, err := service.AddSet(ctx, training.AddSetInput{SessionID: in.SessionID, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE})
		return nil, SetOut{Set: v, TotalSI: total}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "update_set", Description: "Correct a training set"}, func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateInput) (*mcp.CallToolResult, SetOut, error) {
		v, total, err := service.UpdateSet(ctx, in.SetID, training.SetPatch{Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE})
		return nil, SetOut{Set: v, TotalSI: total}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "delete_set", Description: "Delete a training set"}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteInput) (*mcp.CallToolResult, DeleteOut, error) {
		sid, total, n, err := service.DeleteSet(ctx, in.SetID)
		return nil, DeleteOut{SessionID: sid, TotalSI: total, Remaining: n}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_session", Description: "Get a training session"}, func(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, SessionOut, error) {
		v, err := service.GetSession(ctx, in.SessionID)
		return nil, SessionOut{Session: v}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "list_sessions", Description: "List training sessions"}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOut, error) {
		v, err := service.ListSessions(ctx, training.ListFilter{Limit: in.Limit, From: in.From, To: in.To})
		return nil, ListOut{Sessions: v}, toolError(err)
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	return &Server{mcp: s, handler: h}
}
func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) ToolNames() []string {
	return []string{"start_session", "add_set", "update_set", "delete_set", "get_session", "list_sessions"}
}
func toolError(err error) error {
	if err == nil {
		return nil
	}
	if err == training.ErrValidation || err == training.ErrNotFound {
		return err
	}
	return fmt.Errorf("internal tool error")
}
