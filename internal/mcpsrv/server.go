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
	Date   string `json:"date,omitempty" jsonschema:"Training date in exact YYYY-MM-DD format."`
	PlanID int64  `json:"plan_id,omitempty" jsonschema:"Optional plan to follow. Omit for a free-form session."`
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
	Technique string  `json:"technique,omitempty" jsonschema:"Optional intensity technique applied to the set, e.g. 'drop set', 'rest-pause'. Omit for a normal straight set."`
}
type LogInput struct {
	Exercise  string  `json:"exercise" jsonschema:"Non-empty exercise name; it is trimmed and lowercased before storage."`
	WeightKG  float64 `json:"weight_kg" jsonschema:"Weight in kilograms; must be strictly positive."`
	Reps      int     `json:"reps" jsonschema:"Repetition count; must be strictly positive."`
	RPE       float64 `json:"rpe" jsonschema:"Numeric rate of perceived exertion from 1 through 10; determines the set's SI."`
	Count     int     `json:"count,omitempty" jsonschema:"How many identical sets to record; defaults to 1, accepts up to 20. Use it for '3x10 at 80kg'."`
	Technique string  `json:"technique,omitempty" jsonschema:"Optional intensity technique applied to the set, e.g. 'drop set', 'rest-pause', 'myo-reps', 'sin parar'. Omit for a normal straight set."`
}
type LogOut struct {
	Session training.Session `json:"session"`
	Sets    []training.Set   `json:"sets"`
	TotalSI float64          `json:"total_si"`
}
type SetOut struct {
	Set     training.Set `json:"set"`
	TotalSI float64      `json:"total_si"`
}
type UpdateInput struct {
	SetID     int64    `json:"set_id" jsonschema:"Positive ID of the existing set to update."`
	Exercise  *string  `json:"exercise,omitempty" jsonschema:"Non-empty replacement exercise name; it is trimmed and lowercased before storage."`
	WeightKG  *float64 `json:"weight_kg,omitempty" jsonschema:"Replacement weight in kilograms; must be strictly positive."`
	Reps      *int     `json:"reps,omitempty" jsonschema:"Replacement repetition count; must be strictly positive."`
	RPE       *float64 `json:"rpe,omitempty" jsonschema:"Replacement numeric RPE from 1 through 10; recalculates the set's SI."`
	Technique *string  `json:"technique,omitempty" jsonschema:"Replacement intensity technique. Pass an empty string to clear it back to a normal set."`
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
type PlanItemInput struct {
	Exercise   string  `json:"exercise" jsonschema:"Non-empty exercise name; trimmed and lowercased to match stored sets."`
	TargetSets int     `json:"target_sets" jsonschema:"Planned number of sets; 1 through 20."`
	RepMin     int     `json:"rep_min,omitempty" jsonschema:"Lower bound of the target rep range, e.g. 10 for '10 to 12'."`
	RepMax     int     `json:"rep_max,omitempty" jsonschema:"Upper bound of the target rep range, e.g. 12 for '10 to 12'."`
	TargetRPE  float64 `json:"target_rpe,omitempty" jsonschema:"Prescribed RPE from 1 through 10. Omit if the plan does not prescribe one."`
	Superset   string  `json:"superset,omitempty" jsonschema:"Label grouping exercises done back to back, e.g. 'A'. Items sharing a label are one superset; omit for a standalone exercise."`
	Notes      string  `json:"notes,omitempty" jsonschema:"Free-text intent for this exercise: cues, tempo, whether to push the last set. Travels into the session and is readable while training."`
}
type CreatePlanInput struct {
	Name  string          `json:"name" jsonschema:"Name of the plan, e.g. 'Empuje A'."`
	Notes string          `json:"notes,omitempty" jsonschema:"Optional free-text notes about the plan."`
	Items []PlanItemInput `json:"items" jsonschema:"Ordered exercises to perform; order is preserved as the session order."`
}
type PlanOut struct {
	Plan training.Plan `json:"plan"`
}
type PlansOut struct {
	Plans []training.Plan `json:"plans"`
}
type PlanInput struct {
	PlanID int64 `json:"plan_id" jsonschema:"Positive ID of the plan."`
}
type UpdatePlanInput struct {
	PlanID int64   `json:"plan_id" jsonschema:"Positive ID of the plan to edit."`
	Name   *string `json:"name,omitempty" jsonschema:"New name. Omit to leave unchanged."`
	Notes  *string `json:"notes,omitempty" jsonschema:"New notes for the whole routine: intent, phase of the block, what to watch. Pass an empty string to clear. Omit to leave unchanged."`
}
type PlanItemEditInput struct {
	PlanID     int64   `json:"plan_id" jsonschema:"Positive ID of the plan to edit."`
	Exercise   string  `json:"exercise" jsonschema:"Non-empty exercise name; trimmed and lowercased to match stored sets."`
	TargetSets int     `json:"target_sets" jsonschema:"Planned number of sets; 1 through 20."`
	RepMin     int     `json:"rep_min,omitempty" jsonschema:"Lower bound of the target rep range."`
	RepMax     int     `json:"rep_max,omitempty" jsonschema:"Upper bound of the target rep range."`
	TargetRPE  float64 `json:"target_rpe,omitempty" jsonschema:"Prescribed RPE from 1 through 10."`
	Superset   string  `json:"superset,omitempty" jsonschema:"Label grouping exercises done back to back."`
	Notes      string  `json:"notes,omitempty" jsonschema:"Free-text intent for this exercise."`
}
type PlanItemRefInput struct {
	PlanID   int64  `json:"plan_id" jsonschema:"Positive ID of the plan to edit."`
	Exercise string `json:"exercise" jsonschema:"Exercise currently in the plan."`
}
type PlanMoveInput struct {
	PlanID    int64  `json:"plan_id" jsonschema:"Positive ID of the plan to edit."`
	Exercise  string `json:"exercise" jsonschema:"Exercise currently in the plan."`
	Direction string `json:"direction" jsonschema:"Where to move it: 'up' or 'down'. Order is the order the session is performed in."`
}
type PlanDeleteOut struct {
	PlanID int64 `json:"plan_id"`
}
type ProgressOut struct {
	SessionID int64                   `json:"session_id"`
	PlanName  string                  `json:"plan_name,omitempty"`
	Progress  []training.PlanProgress `json:"progress"`
}
type SessionItemInput struct {
	SessionID  int64   `json:"session_id" jsonschema:"Positive ID of the session to adjust."`
	Exercise   string  `json:"exercise" jsonschema:"Non-empty exercise name; trimmed and lowercased to match stored sets."`
	TargetSets int     `json:"target_sets" jsonschema:"Planned number of sets for this session; 1 through 20."`
	RepMin     int     `json:"rep_min,omitempty" jsonschema:"Lower bound of the target rep range."`
	RepMax     int     `json:"rep_max,omitempty" jsonschema:"Upper bound of the target rep range."`
	TargetRPE  float64 `json:"target_rpe,omitempty" jsonschema:"Target RPE from 1 through 10."`
	Superset   string  `json:"superset,omitempty" jsonschema:"Label grouping exercises done back to back. Omit for a standalone exercise."`
	Notes      string  `json:"notes,omitempty" jsonschema:"Free-text intent for this exercise in today's session."`
}
type AdjustItemInput struct {
	SessionID  int64    `json:"session_id" jsonschema:"Positive ID of the session to adjust."`
	Exercise   string   `json:"exercise" jsonschema:"Exercise already in this session's plan."`
	TargetSets *int     `json:"target_sets,omitempty" jsonschema:"New planned set count; 1 through 20. Omit to leave unchanged."`
	RepMin     *int     `json:"rep_min,omitempty" jsonschema:"New lower bound of the rep range. Omit to leave unchanged."`
	RepMax     *int     `json:"rep_max,omitempty" jsonschema:"New upper bound of the rep range. Omit to leave unchanged."`
	TargetRPE  *float64 `json:"target_rpe,omitempty" jsonschema:"New target RPE from 1 through 10. Omit to leave unchanged."`
	Skipped    *bool    `json:"skipped,omitempty" jsonschema:"Mark the exercise as consciously skipped today, or unmark it."`
}
type SwapItemInput struct {
	SessionID   int64  `json:"session_id" jsonschema:"Positive ID of the session to adjust."`
	Exercise    string `json:"exercise" jsonschema:"Exercise currently in the session's plan."`
	Replacement string `json:"replacement" jsonschema:"Exercise to use instead, keeping the same prescription."`
}
type RemoveItemInput struct {
	SessionID int64  `json:"session_id" jsonschema:"Positive ID of the session to adjust."`
	Exercise  string `json:"exercise" jsonschema:"Exercise to drop from this session's plan."`
}
type SaveAsPlanInput struct {
	SessionID int64  `json:"session_id" jsonschema:"Positive ID of the session to turn into a plan."`
	Name      string `json:"name" jsonschema:"Name for the new plan."`
}
type ItemOut struct {
	SessionID int64  `json:"session_id"`
	Exercise  string `json:"exercise"`
}
type FeedbackInput struct {
	SessionID   int64  `json:"session_id" jsonschema:"Positive ID of the session being rated."`
	MuscleGroup string `json:"muscle_group" jsonschema:"Muscle group trained; must be one of the supported values."`
	Fatigue     int    `json:"fatigue" jsonschema:"How much fatigue the muscle took, 0 to 3. 0 = barely noticed it, 3 = severe."`
	Pump        int    `json:"pump" jsonschema:"How much pump the muscle got, 0 to 3. 0 = none, 3 = extreme."`
	Recovery    int    `json:"recovery" jsonschema:"How sore and unrecovered it felt afterwards, 0 to 3. 0 = nothing, 3 = still wrecked days later."`
}
type FeedbackOut struct {
	SessionID int64               `json:"session_id"`
	Feedback  []training.Feedback `json:"feedback"`
}
type RecommendOut struct {
	Recommendations []training.SetChange `json:"recommendations"`
}
type LoadInput struct {
	Exercise string `json:"exercise" jsonschema:"Non-empty exercise name; trimmed and lowercased to match stored sets."`
}
type LoadSuggestionOut struct {
	Suggestion training.LoadSuggestion `json:"suggestion"`
}
type NoteInput struct {
	Exercise string `json:"exercise" jsonschema:"Non-empty exercise name; trimmed and lowercased to match stored sets."`
	Note     string `json:"note" jsonschema:"Setup reminder, e.g. 'asiento en 4, agarre ancho'. Pass an empty string to remove it."`
}
type NotesOut struct {
	Notes []training.ExerciseNote `json:"notes"`
}
type DeleteSessionOut struct {
	SessionID   int64 `json:"session_id"`
	DeletedSets int   `json:"deleted_sets"`
}
type HistoryInput struct {
	Exercise string `json:"exercise" jsonschema:"Non-empty exercise name; it is trimmed and lowercased to match stored sets."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum sets to return; defaults to 50 and accepts 1 through 500."`
}
type HistoryOut struct {
	History training.ExerciseHistory `json:"history"`
}
type WeeklyOut struct {
	Weeks []training.WeeklyVolume `json:"weeks"`
}
type GroupInput struct {
	Exercise    string `json:"exercise" jsonschema:"Non-empty exercise name; it is trimmed and lowercased to match stored sets."`
	MuscleGroup string `json:"muscle_group" jsonschema:"Muscle group the exercise trains; must be one of the supported values."`
}
type GroupOut struct {
	Exercise    string `json:"exercise"`
	MuscleGroup string `json:"muscle_group"`
}
type GroupsOut struct {
	Groups []training.ExerciseGroup `json:"groups"`
}
type VolumeInput struct {
	From string `json:"from,omitempty" jsonschema:"Inclusive earliest training date in exact YYYY-MM-DD format."`
	To   string `json:"to,omitempty" jsonschema:"Inclusive latest training date in exact YYYY-MM-DD format."`
}
type VolumeOut struct {
	Volume []training.GroupVolume `json:"volume"`
}

func New(service *training.Service) *Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "training-mcp", Version: "0.1.0"}, nil)
	startSchema := mustInputSchema[StartInput]()
	startSchema.Properties["date"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	mcp.AddTool(s, &mcp.Tool{Name: "start_session", Description: "Start a training session on an optional date, optionally following a plan. Omit date to use the server's current local date.", InputSchema: startSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in StartInput) (*mcp.CallToolResult, SessionOut, error) {
		v, err := service.StartPlannedSession(ctx, in.Date, in.PlanID)
		return nil, SessionOut{Session: v}, toolError(err)
	})
	addSchema := mustInputSchema[AddInput]()
	setPositiveID(addSchema.Properties["session_id"])
	addSchema.Properties["exercise"].Pattern = `.*\S.*`
	addSchema.Properties["weight_kg"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	addSchema.Properties["reps"].Minimum = jsonschema.Ptr(1.0)
	setRPERange(addSchema.Properties["rpe"])
	mcp.AddTool(s, &mcp.Tool{Name: "add_set", Description: "Add a set to an existing training session and return the set plus the session's recalculated total SI.", InputSchema: addSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in AddInput) (*mcp.CallToolResult, SetOut, error) {
		v, total, err := service.AddSet(ctx, training.AddSetInput{SessionID: in.SessionID, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, Technique: in.Technique})
		return nil, SetOut{Set: v, TotalSI: total}, toolError(err)
	})
	logSchema := mustInputSchema[LogInput]()
	logSchema.Properties["exercise"].Pattern = `.*\S.*`
	logSchema.Properties["weight_kg"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	logSchema.Properties["reps"].Minimum = jsonschema.Ptr(1.0)
	setRPERange(logSchema.Properties["rpe"])
	logSchema.Properties["count"].Default = json.RawMessage("1")
	logSchema.Properties["count"].Minimum = jsonschema.Ptr(1.0)
	logSchema.Properties["count"].Maximum = jsonschema.Ptr(20.0)
	mcp.AddTool(s, &mcp.Tool{Name: "log_set", Description: "Record one or more identical sets into today's session, creating that session if it does not exist yet. This is the simplest way to log training: it needs no session id. Prefer it over start_session plus add_set.", InputSchema: logSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in LogInput) (*mcp.CallToolResult, LogOut, error) {
		session, sets, err := service.LogSet(ctx, in.Exercise, in.WeightKG, in.Reps, in.RPE, in.Count, in.Technique)
		return nil, LogOut{Session: session, Sets: sets, TotalSI: session.TotalSI}, toolError(err)
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
	setNonNullableType(updateSchema.Properties["technique"], "string")
	mcp.AddTool(s, &mcp.Tool{Name: "update_set", Description: "Update one or more fields of an existing set. Omitted fields remain unchanged; RPE changes recalculate SI.", InputSchema: updateSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateInput) (*mcp.CallToolResult, SetOut, error) {
		v, total, err := service.UpdateSet(ctx, in.SetID, training.SetPatch{Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, Technique: in.Technique})
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
	createPlanSchema := mustInputSchema[CreatePlanInput]()
	createPlanSchema.Properties["name"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "create_plan", Description: "Create a reusable workout plan: an ordered list of exercises with a target set count and an optional rep range and RPE. Load is not planned; it is filled in at the gym.", InputSchema: createPlanSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreatePlanInput) (*mcp.CallToolResult, PlanOut, error) {
		plan := training.Plan{Name: in.Name, Notes: in.Notes}
		for _, it := range in.Items {
			plan.Items = append(plan.Items, training.PlanItem{
				Exercise: it.Exercise, TargetSets: it.TargetSets,
				RepMin: it.RepMin, RepMax: it.RepMax, TargetRPE: it.TargetRPE,
				Superset: it.Superset, Notes: it.Notes,
			})
		}
		v, err := service.CreatePlan(ctx, plan)
		return nil, PlanOut{Plan: v}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "list_plans", Description: "List every saved workout plan with its total planned set count."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, PlansOut, error) {
		v, err := service.ListPlans(ctx)
		return nil, PlansOut{Plans: v}, toolError(err)
	})
	planSchema := mustInputSchema[PlanInput]()
	setPositiveID(planSchema.Properties["plan_id"])
	mcp.AddTool(s, &mcp.Tool{Name: "get_plan", Description: "Get one plan with its ordered exercises, target sets, rep ranges and RPEs.", InputSchema: planSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in PlanInput) (*mcp.CallToolResult, PlanOut, error) {
		v, err := service.GetPlan(ctx, in.PlanID)
		return nil, PlanOut{Plan: v}, toolError(err)
	})
	updatePlanSchema := mustInputSchema[UpdatePlanInput]()
	setPositiveID(updatePlanSchema.Properties["plan_id"])
	updatePlanSchema.MinProperties = jsonschema.Ptr(2)
	setNonNullableType(updatePlanSchema.Properties["name"], "string")
	setNonNullableType(updatePlanSchema.Properties["notes"], "string")
	mcp.AddTool(s, &mcp.Tool{Name: "update_plan", Description: "Edit a plan's name or notes without touching its exercises. Plan notes are free text describing the routine's intent, and are returned by get_plan and list_plans.", InputSchema: updatePlanSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in UpdatePlanInput) (*mcp.CallToolResult, PlanOut, error) {
		if err := service.UpdatePlan(ctx, in.PlanID, training.PlanPatch{Name: in.Name, Notes: in.Notes}); err != nil {
			return nil, PlanOut{}, toolError(err)
		}
		v, err := service.GetPlan(ctx, in.PlanID)
		return nil, PlanOut{Plan: v}, toolError(err)
	})

	planItemSchema := mustInputSchema[PlanItemEditInput]()
	setPositiveID(planItemSchema.Properties["plan_id"])
	planItemSchema.Properties["exercise"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "set_plan_exercise", Description: "Add an exercise to a saved plan, or replace its prescription. Editing a template never changes sessions already started from it.", InputSchema: planItemSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in PlanItemEditInput) (*mcp.CallToolResult, PlanOut, error) {
		err := service.SetPlanItem(ctx, in.PlanID, training.PlanItem{
			Exercise: in.Exercise, TargetSets: in.TargetSets, RepMin: in.RepMin,
			RepMax: in.RepMax, TargetRPE: in.TargetRPE, Superset: in.Superset, Notes: in.Notes,
		})
		if err != nil {
			return nil, PlanOut{}, toolError(err)
		}
		v, err := service.GetPlan(ctx, in.PlanID)
		return nil, PlanOut{Plan: v}, toolError(err)
	})
	planRefSchema := mustInputSchema[PlanItemRefInput]()
	setPositiveID(planRefSchema.Properties["plan_id"])
	planRefSchema.Properties["exercise"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "remove_plan_exercise", Description: "Remove an exercise from a saved plan and close the gap in its order.", InputSchema: planRefSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in PlanItemRefInput) (*mcp.CallToolResult, PlanOut, error) {
		if err := service.RemovePlanItem(ctx, in.PlanID, in.Exercise); err != nil {
			return nil, PlanOut{}, toolError(err)
		}
		v, err := service.GetPlan(ctx, in.PlanID)
		return nil, PlanOut{Plan: v}, toolError(err)
	})
	planMoveSchema := mustInputSchema[PlanMoveInput]()
	setPositiveID(planMoveSchema.Properties["plan_id"])
	planMoveSchema.Properties["exercise"].Pattern = `.*\S.*`
	planMoveSchema.Properties["direction"].Enum = []any{"up", "down"}
	mcp.AddTool(s, &mcp.Tool{Name: "move_plan_exercise", Description: "Move an exercise one place up or down within a plan. The order is the order the session is performed in.", InputSchema: planMoveSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in PlanMoveInput) (*mcp.CallToolResult, PlanOut, error) {
		delta := 1
		if in.Direction == "up" {
			delta = -1
		}
		if err := service.MovePlanItem(ctx, in.PlanID, in.Exercise, delta); err != nil {
			return nil, PlanOut{}, toolError(err)
		}
		v, err := service.GetPlan(ctx, in.PlanID)
		return nil, PlanOut{Plan: v}, toolError(err)
	})

	deletePlanSchema := mustInputSchema[PlanInput]()
	setPositiveID(deletePlanSchema.Properties["plan_id"])
	mcp.AddTool(s, &mcp.Tool{Name: "delete_plan", Description: "Permanently delete a plan. Sessions that followed it keep their recorded plan name.", InputSchema: deletePlanSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in PlanInput) (*mcp.CallToolResult, PlanDeleteOut, error) {
		err := service.DeletePlan(ctx, in.PlanID)
		return nil, PlanDeleteOut{PlanID: in.PlanID}, toolError(err)
	})
	progressSchema := mustInputSchema[SessionInput]()
	setPositiveID(progressSchema.Properties["session_id"])
	mcp.AddTool(s, &mcp.Tool{Name: "session_progress", Description: "For a session following a plan: planned versus completed sets per exercise, with the prescribed rep range and RPE. Exercises done off-plan are listed with target_sets 0.", InputSchema: progressSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, ProgressOut, error) {
		v, err := service.SessionProgress(ctx, in.SessionID)
		out := ProgressOut{SessionID: in.SessionID, Progress: v}
		if sess, e := service.GetSession(ctx, in.SessionID); e == nil {
			out.PlanName = sess.PlanName
		}
		return nil, out, toolError(err)
	})

	setItemSchema := mustInputSchema[SessionItemInput]()
	setPositiveID(setItemSchema.Properties["session_id"])
	setItemSchema.Properties["exercise"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "add_session_exercise", Description: "Add an exercise to today's session plan, or replace its prescription. Adjusting a session never edits the plan it was started from.", InputSchema: setItemSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SessionItemInput) (*mcp.CallToolResult, ItemOut, error) {
		err := service.SetSessionItem(ctx, in.SessionID, training.PlanItem{
			Exercise: in.Exercise, TargetSets: in.TargetSets,
			RepMin: in.RepMin, RepMax: in.RepMax, TargetRPE: in.TargetRPE,
			Superset: in.Superset, Notes: in.Notes,
		})
		return nil, ItemOut{SessionID: in.SessionID, Exercise: in.Exercise}, toolError(err)
	})
	adjustSchema := mustInputSchema[AdjustItemInput]()
	setPositiveID(adjustSchema.Properties["session_id"])
	adjustSchema.Properties["exercise"].Pattern = `.*\S.*`
	adjustSchema.MinProperties = jsonschema.Ptr(3)
	setNonNullableType(adjustSchema.Properties["target_sets"], "integer")
	setNonNullableType(adjustSchema.Properties["rep_min"], "integer")
	setNonNullableType(adjustSchema.Properties["rep_max"], "integer")
	setNonNullableType(adjustSchema.Properties["target_rpe"], "number")
	setNonNullableType(adjustSchema.Properties["skipped"], "boolean")
	mcp.AddTool(s, &mcp.Tool{Name: "adjust_session_exercise", Description: "Change one exercise of today's session: its set count, rep range, RPE, or whether it is skipped. Omitted fields stay as they are. Use when the session does not go as planned.", InputSchema: adjustSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in AdjustItemInput) (*mcp.CallToolResult, ItemOut, error) {
		err := service.AdjustSessionItem(ctx, in.SessionID, in.Exercise, training.SessionItemPatch{
			TargetSets: in.TargetSets, RepMin: in.RepMin, RepMax: in.RepMax,
			TargetRPE: in.TargetRPE, Skipped: in.Skipped,
		})
		return nil, ItemOut{SessionID: in.SessionID, Exercise: in.Exercise}, toolError(err)
	})
	swapSchema := mustInputSchema[SwapItemInput]()
	setPositiveID(swapSchema.Properties["session_id"])
	swapSchema.Properties["exercise"].Pattern = `.*\S.*`
	swapSchema.Properties["replacement"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "swap_session_exercise", Description: "Substitute one exercise for another in today's session, keeping the same prescription. Use when the planned machine is taken or the movement does not feel right.", InputSchema: swapSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SwapItemInput) (*mcp.CallToolResult, ItemOut, error) {
		err := service.SwapSessionItem(ctx, in.SessionID, in.Exercise, in.Replacement)
		return nil, ItemOut{SessionID: in.SessionID, Exercise: in.Replacement}, toolError(err)
	})
	removeItemSchema := mustInputSchema[RemoveItemInput]()
	setPositiveID(removeItemSchema.Properties["session_id"])
	removeItemSchema.Properties["exercise"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "remove_session_exercise", Description: "Drop an exercise from today's session plan. Sets already logged against it are kept and reappear as off-plan work.", InputSchema: removeItemSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in RemoveItemInput) (*mcp.CallToolResult, ItemOut, error) {
		err := service.RemoveSessionItem(ctx, in.SessionID, in.Exercise)
		return nil, ItemOut{SessionID: in.SessionID, Exercise: in.Exercise}, toolError(err)
	})
	saveAsPlanSchema := mustInputSchema[SaveAsPlanInput]()
	setPositiveID(saveAsPlanSchema.Properties["session_id"])
	saveAsPlanSchema.Properties["name"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "save_session_as_plan", Description: "Turn a session — including any in-session adjustments and off-plan work — into a new reusable plan. Skipped exercises are left out.", InputSchema: saveAsPlanSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SaveAsPlanInput) (*mcp.CallToolResult, PlanOut, error) {
		v, err := service.SaveSessionAsPlan(ctx, in.SessionID, in.Name)
		return nil, PlanOut{Plan: v}, toolError(err)
	})

	loadSchema := mustInputSchema[LoadInput]()
	loadSchema.Properties["exercise"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "suggest_load", Description: "Suggest the next working weight for an exercise, judged against the rep range and RPE it is prescribed at. Advisory only: it explains its reasoning and never changes a plan. Sets carrying an intensity technique are ignored, since they are not comparable to straight sets.", InputSchema: loadSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoadInput) (*mcp.CallToolResult, LoadSuggestionOut, error) {
		v, err := service.SuggestLoad(ctx, in.Exercise)
		return nil, LoadSuggestionOut{Suggestion: v}, toolError(err)
	})

	noteSchema := mustInputSchema[NoteInput]()
	noteSchema.Properties["exercise"].Pattern = `.*\S.*`
	mcp.AddTool(s, &mcp.Tool{Name: "set_exercise_note", Description: "Store a persistent setup reminder for an exercise, such as seat height or grip width. It is shown every time that exercise is logged. An empty note removes it.", InputSchema: noteSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in NoteInput) (*mcp.CallToolResult, NotesOut, error) {
		if err := service.SetExerciseNote(ctx, in.Exercise, in.Note); err != nil {
			return nil, NotesOut{}, toolError(err)
		}
		v, err := service.ExerciseNotes(ctx)
		return nil, NotesOut{Notes: v}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "list_exercise_notes", Description: "List every exercise that has a setup note."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, NotesOut, error) {
		v, err := service.ExerciseNotes(ctx)
		return nil, NotesOut{Notes: v}, toolError(err)
	})

	feedbackSchema := mustInputSchema[FeedbackInput]()
	setPositiveID(feedbackSchema.Properties["session_id"])
	feedbackSchema.Properties["muscle_group"].Enum = muscleGroupEnum()
	for _, dim := range []string{"fatigue", "pump", "recovery"} {
		feedbackSchema.Properties[dim].Minimum = jsonschema.Ptr(0.0)
		feedbackSchema.Properties[dim].Maximum = jsonschema.Ptr(3.0)
	}
	mcp.AddTool(s, &mcp.Tool{Name: "record_feedback", Description: "Rate how one muscle group responded to a session: fatigue, pump and recovery, each 0 to 3. Only rate groups actually trained. This drives next week's set-count recommendation.", InputSchema: feedbackSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in FeedbackInput) (*mcp.CallToolResult, FeedbackOut, error) {
		err := service.RecordFeedback(ctx, in.SessionID, training.Feedback{
			MuscleGroup: in.MuscleGroup, Fatigue: in.Fatigue, Pump: in.Pump, Recovery: in.Recovery,
		})
		if err != nil {
			return nil, FeedbackOut{SessionID: in.SessionID}, toolError(err)
		}
		all, err := service.SessionFeedback(ctx, in.SessionID)
		return nil, FeedbackOut{SessionID: in.SessionID, Feedback: all}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "volume_recommendation", Description: "Set-count change per muscle group for next week, derived from the most recent feedback and the volume it responded to. Follows the volume-landmark logic of the source spreadsheet."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, RecommendOut, error) {
		v, err := service.VolumeRecommendation(ctx)
		return nil, RecommendOut{Recommendations: v}, toolError(err)
	})

	deleteSessionSchema := mustInputSchema[SessionInput]()
	setPositiveID(deleteSessionSchema.Properties["session_id"])
	mcp.AddTool(s, &mcp.Tool{Name: "delete_session", Description: "Permanently delete a training session and every set in it. Irreversible; returns how many sets were destroyed. Use to remove an empty or mistaken session.", InputSchema: deleteSessionSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, DeleteSessionOut, error) {
		n, err := service.DeleteSession(ctx, in.SessionID)
		return nil, DeleteSessionOut{SessionID: in.SessionID, DeletedSets: n}, toolError(err)
	})
	historySchema := mustInputSchema[HistoryInput]()
	historySchema.Properties["exercise"].Pattern = `.*\S.*`
	historySchema.Properties["limit"].Default = json.RawMessage("50")
	historySchema.Properties["limit"].Minimum = jsonschema.Ptr(1.0)
	historySchema.Properties["limit"].Maximum = jsonschema.Ptr(500.0)
	mcp.AddTool(s, &mcp.Tool{Name: "exercise_history", Description: "All recorded sets of one exercise, newest first, with each set's estimated 1RM, plus the best set ever recorded for it. Use this to judge progression instead of reading whole sessions.", InputSchema: historySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in HistoryInput) (*mcp.CallToolResult, HistoryOut, error) {
		v, err := service.ExerciseHistory(ctx, in.Exercise, in.Limit)
		return nil, HistoryOut{History: v}, toolError(err)
	})
	weeklySchema := mustInputSchema[VolumeInput]()
	weeklySchema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	weeklySchema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	mcp.AddTool(s, &mcp.Tool{Name: "weekly_volume", Description: "SI and set count per muscle group per training week, newest week first. Week start is the Monday of that week. Use this to see whether a muscle group's stimulus is rising or falling over time.", InputSchema: weeklySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in VolumeInput) (*mcp.CallToolResult, WeeklyOut, error) {
		v, err := service.WeeklyVolume(ctx, training.ListFilter{From: in.From, To: in.To})
		return nil, WeeklyOut{Weeks: v}, toolError(err)
	})

	groupSchema := mustInputSchema[GroupInput]()
	groupSchema.Properties["exercise"].Pattern = `.*\S.*`
	groupSchema.Properties["muscle_group"].Enum = muscleGroupEnum()
	mcp.AddTool(s, &mcp.Tool{Name: "set_exercise_group", Description: "Assign an exercise to one muscle group so its sets count toward that group's volume. Re-assigning replaces the previous group.", InputSchema: groupSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in GroupInput) (*mcp.CallToolResult, GroupOut, error) {
		err := service.SetExerciseGroup(ctx, in.Exercise, in.MuscleGroup)
		return nil, GroupOut{Exercise: in.Exercise, MuscleGroup: in.MuscleGroup}, toolError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "list_exercise_groups", Description: "List every exercise that has a muscle group assigned, ordered by group."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, GroupsOut, error) {
		v, err := service.ExerciseGroups(ctx)
		return nil, GroupsOut{Groups: v}, toolError(err)
	})
	volumeSchema := mustInputSchema[VolumeInput]()
	volumeSchema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	volumeSchema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	mcp.AddTool(s, &mcp.Tool{Name: "volume_by_muscle", Description: "Total SI and set count per muscle group, optionally within an inclusive date range. Sets whose exercise has no group yet are reported under an empty muscle_group.", InputSchema: volumeSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in VolumeInput) (*mcp.CallToolResult, VolumeOut, error) {
		v, err := service.VolumeByGroup(ctx, training.ListFilter{From: in.From, To: in.To})
		return nil, VolumeOut{Volume: v}, toolError(err)
	})

	// Stateless: the server keeps no per-client session state, so restarting the
	// container no longer invalidates a live conversation. These tools are plain
	// request/response with no server-initiated notifications, so nothing is lost.
	h := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	return &Server{mcp: s, handler: h}
}
func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) ToolNames() []string {
	return []string{"log_set", "start_session", "add_set", "update_set", "delete_set", "delete_session",
		"get_session", "list_sessions", "exercise_history", "weekly_volume",
		"set_exercise_group", "list_exercise_groups", "volume_by_muscle",
		"create_plan", "list_plans", "get_plan", "update_plan", "delete_plan", "session_progress",
		"set_plan_exercise", "remove_plan_exercise", "move_plan_exercise",
		"add_session_exercise", "adjust_session_exercise", "swap_session_exercise",
		"remove_session_exercise", "save_session_as_plan",
		"record_feedback", "volume_recommendation",
		"set_exercise_note", "list_exercise_notes", "suggest_load"}
}
func muscleGroupEnum() []any {
	out := make([]any, 0, len(training.MuscleGroups))
	for _, g := range training.MuscleGroups {
		out = append(out, g)
	}
	return out
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
