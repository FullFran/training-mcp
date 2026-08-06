package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	mcp     *mcp.Server
	handler *mcp.StreamableHTTPHandler
}
type StartInput struct {
	Date string `json:"date,omitempty" jsonschema:"Training date in exact YYYY-MM-DD format."`
}
type SessionOut struct {
	Session training.Session `json:"session"`
}
type AddInput struct {
	SessionID int64   `json:"session_id" jsonschema:"Positive ID of the existing training session."`
	Exercise  string  `json:"exercise" jsonschema:"Non-empty exercise name; it is trimmed and lowercased before storage."`
	WeightKG  float64 `json:"weight_kg" jsonschema:"Weight in kilograms; must be strictly positive."`
	Reps      int     `json:"reps" jsonschema:"Repetition count; must be strictly positive."`
	RPE       float64 `json:"rpe" jsonschema:"Numeric rate of perceived exertion from 1 through 10; determines the set's SI."`
}
type SetOut struct {
	Set     training.Set `json:"set"`
	TotalSI float64      `json:"total_si"`
}
type UpdateInput struct {
	SetID    int64    `json:"set_id" jsonschema:"Positive ID of the existing set to update."`
	Exercise *string  `json:"exercise,omitempty" jsonschema:"Non-empty replacement exercise name; it is trimmed and lowercased before storage."`
	WeightKG *float64 `json:"weight_kg,omitempty" jsonschema:"Replacement weight in kilograms; must be strictly positive."`
	Reps     *int     `json:"reps,omitempty" jsonschema:"Replacement repetition count; must be strictly positive."`
	RPE      *float64 `json:"rpe,omitempty" jsonschema:"Replacement numeric RPE from 1 through 10; recalculates the set's SI."`
}
type DeleteInput struct {
	SetID int64 `json:"set_id" jsonschema:"Positive ID of the existing set to delete."`
}
type DeleteOut struct {
	SessionID int64   `json:"session_id"`
	TotalSI   float64 `json:"total_si"`
	Remaining int     `json:"remaining"`
}
type SessionInput struct {
	SessionID int64 `json:"session_id" jsonschema:"Positive ID of the existing training session to retrieve."`
}
type ListInput struct {
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum sessions to return; defaults to 20 and accepts 1 through 100."`
	From  string `json:"from,omitempty" jsonschema:"Inclusive earliest training date in exact YYYY-MM-DD format."`
	To    string `json:"to,omitempty" jsonschema:"Inclusive latest training date in exact YYYY-MM-DD format."`
}
type ListOut struct {
	Sessions []training.SessionSummary `json:"sessions"`
}

func New(service *training.Service) *Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "training-mcp", Version: "0.1.0"}, nil)
	startSchema := mustInputSchema[StartInput]()
	startSchema.Properties["date"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	mcp.AddTool(s, &mcp.Tool{Name: "start_session", Description: "Start a training session on an optional date. Omit date to use the server's current local date.", InputSchema: startSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in StartInput) (*mcp.CallToolResult, SessionOut, error) {
		v, err := service.StartSession(ctx, in.Date)
		return nil, SessionOut{Session: v}, toolError(err)
	})
	addSchema := mustInputSchema[AddInput]()
	setPositiveID(addSchema.Properties["session_id"])
	addSchema.Properties["exercise"].Pattern = `.*\S.*`
	addSchema.Properties["weight_kg"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	addSchema.Properties["reps"].Minimum = jsonschema.Ptr(1.0)
	setRPERange(addSchema.Properties["rpe"])
	mcp.AddTool(s, &mcp.Tool{Name: "add_set", Description: "Add a set to an existing training session and return the set plus the session's recalculated total SI.", InputSchema: addSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in AddInput) (*mcp.CallToolResult, SetOut, error) {
		v, total, err := service.AddSet(ctx, training.AddSetInput{SessionID: in.SessionID, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE})
		return nil, SetOut{Set: v, TotalSI: total}, toolError(err)
	})
	updateSchema := mustInputSchema[UpdateInput]()
	updateSchema.MinProperties = jsonschema.Ptr(2)
	setPositiveID(updateSchema.Properties["set_id"])
	setNonNullableType(updateSchema.Properties["exercise"], "string")
	updateSchema.Properties["exercise"].Pattern = `.*\S.*`
	setNonNullableType(updateSchema.Properties["weight_kg"], "number")
	updateSchema.Properties["weight_kg"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	setNonNullableType(updateSchema.Properties["reps"], "integer")
	updateSchema.Properties["reps"].Minimum = jsonschema.Ptr(1.0)
	setNonNullableType(updateSchema.Properties["rpe"], "number")
	setRPERange(updateSchema.Properties["rpe"])
	mcp.AddTool(s, &mcp.Tool{Name: "update_set", Description: "Update one or more fields of an existing set. Omitted fields remain unchanged; RPE changes recalculate SI.", InputSchema: updateSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateInput) (*mcp.CallToolResult, SetOut, error) {
		v, total, err := service.UpdateSet(ctx, in.SetID, training.SetPatch{Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE})
		return nil, SetOut{Set: v, TotalSI: total}, toolError(err)
	})
	deleteSchema := mustInputSchema[DeleteInput]()
	setPositiveID(deleteSchema.Properties["set_id"])
	mcp.AddTool(s, &mcp.Tool{Name: "delete_set", Description: "Permanently delete an existing set and compact the remaining set positions to a dense sequence.", InputSchema: deleteSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteInput) (*mcp.CallToolResult, DeleteOut, error) {
		sid, total, n, err := service.DeleteSet(ctx, in.SetID)
		return nil, DeleteOut{SessionID: sid, TotalSI: total, Remaining: n}, toolError(err)
	})
	sessionSchema := mustInputSchema[SessionInput]()
	setPositiveID(sessionSchema.Properties["session_id"])
	mcp.AddTool(s, &mcp.Tool{Name: "get_session", Description: "Get an existing training session with its ordered sets and total SI.", InputSchema: sessionSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, SessionOut, error) {
		v, err := service.GetSession(ctx, in.SessionID)
		return nil, SessionOut{Session: v}, toolError(err)
	})
	listSchema := mustInputSchema[ListInput]()
	listSchema.Properties["limit"].Default = json.RawMessage("20")
	listSchema.Properties["limit"].Minimum = jsonschema.Ptr(1.0)
	listSchema.Properties["limit"].Maximum = jsonschema.Ptr(100.0)
	listSchema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	listSchema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	mcp.AddTool(s, &mcp.Tool{Name: "list_sessions", Description: "List training sessions newest first, optionally filtered by inclusive date bounds. From must not be after to.", InputSchema: listSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOut, error) {
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
func mustInputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	return schema
}
func setPositiveID(schema *jsonschema.Schema) { schema.Minimum = jsonschema.Ptr(1.0) }
func setNonNullableType(schema *jsonschema.Schema, schemaType string) {
	schema.Type = schemaType
	schema.Types = nil
}
func setRPERange(schema *jsonschema.Schema) {
	schema.Minimum = jsonschema.Ptr(1.0)
	schema.Maximum = jsonschema.Ptr(10.0)
}
func toolError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, training.ErrValidation) || errors.Is(err, training.ErrNotFound) {
		return err
	}
	return fmt.Errorf("internal tool error")
}
