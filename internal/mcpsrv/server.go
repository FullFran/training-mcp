package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	mcp     *mcp.Server
	handler *mcp.StreamableHTTPHandler
	writes  *writes
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
	Category  string  `json:"category,omitempty" jsonschema:"Set category: 'warmup' for a lighter preparatory set done before the working weight, 'working' (the default) for everything else. Use warmup whenever the user calls a set a warm-up (calentamiento, aproximación); a warm-up is recorded and shown but never counts toward SI, volume, set counts, plan progress, best set, e1RM, suggest_load or performance estimates. Do not put warm-up in technique."`
}
type LogInput struct {
	Exercise  string  `json:"exercise" jsonschema:"Non-empty exercise name; it is trimmed and lowercased before storage."`
	WeightKG  float64 `json:"weight_kg" jsonschema:"Weight in kilograms; must be strictly positive."`
	Reps      int     `json:"reps" jsonschema:"Repetition count; must be strictly positive."`
	RPE       float64 `json:"rpe" jsonschema:"Numeric rate of perceived exertion from 1 through 10; determines the set's SI."`
	Count     int     `json:"count,omitempty" jsonschema:"How many identical sets to record; defaults to 1, accepts up to 20. Use it for '3x10 at 80kg'."`
	Technique string  `json:"technique,omitempty" jsonschema:"Optional intensity technique applied to the set, e.g. 'drop set', 'rest-pause', 'myo-reps', 'sin parar'. Omit for a normal straight set."`
	Category  string  `json:"category,omitempty" jsonschema:"Set category: 'warmup' for a lighter preparatory set done before the working weight, 'working' (the default) for everything else. Use warmup whenever the user calls a set a warm-up (calentamiento, aproximación); a warm-up is recorded and shown but never counts toward SI, volume, set counts, plan progress, best set, e1RM, suggest_load or performance estimates. Do not put warm-up in technique."`
}
type LogOut struct {
	Session training.Session `json:"session"`
	Sets    []training.Set   `json:"sets"`
	TotalSI float64          `json:"total_si"`
	ProposalRef
}
type LoggedSetInput struct {
	WeightKG  float64  `json:"weight_kg" jsonschema:"Weight in kilograms; must be strictly positive."`
	Reps      int      `json:"reps" jsonschema:"Repetition count; must be strictly positive."`
	RPE       *float64 `json:"rpe,omitempty" jsonschema:"Rate of perceived exertion from 1 through 10, only if the user stated it. Never guess: omit it otherwise, and the set counts one SI unit and is marked rpe_imputed."`
	Technique string   `json:"technique,omitempty" jsonschema:"Optional intensity technique applied to the set, e.g. 'drop set', 'rest-pause'. Omit for a normal straight set."`
	Category  string   `json:"category,omitempty" jsonschema:"Set category: 'warmup' for a lighter preparatory set done before the working weight, 'working' (the default) for everything else. Use warmup whenever the user calls a set a warm-up (calentamiento, aproximación); a warm-up is recorded and shown but never counts toward SI, volume, set counts, plan progress, best set, e1RM, suggest_load or performance estimates. Do not put warm-up in technique."`
}
type LogSetsInput struct {
	IdempotencyKey string           `json:"idempotency_key" jsonschema:"A unique value you generate once for this request, such as a UUID. Send the exact same value if you retry after an error or timeout, so nothing is recorded twice. Never reuse it for different sets."`
	Exercise       string           `json:"exercise" jsonschema:"Non-empty exercise name; it is trimmed and lowercased before storage."`
	Sets           []LoggedSetInput `json:"sets" jsonschema:"The sets in the order performed; 1 through 20. Each one may have its own weight, reps and RPE."`
}
type LogSetsOut struct {
	Session  training.Session `json:"session"`
	Sets     []training.Set   `json:"sets"`
	TotalSI  float64          `json:"total_si"`
	Replayed bool             `json:"replayed"`
	ProposalRef
}

// StartOut is start_session's result. Write results carry the proposal's status.
type StartOut struct {
	Session training.Session `json:"session"`
	ProposalRef
}
type PlanWriteOut struct {
	Plan training.Plan `json:"plan"`
	ProposalRef
}
type NotesWriteOut struct {
	Notes []training.ExerciseNote `json:"notes"`
	ProposalRef
}
type LandmarksWriteOut struct {
	Landmarks []training.Landmarks `json:"landmarks"`
	ProposalRef
}
type SetOut struct {
	Set     training.Set `json:"set"`
	TotalSI float64      `json:"total_si"`
	ProposalRef
}
type UpdateInput struct {
	SetID     int64    `json:"set_id" jsonschema:"Positive ID of the existing set to update."`
	Exercise  *string  `json:"exercise,omitempty" jsonschema:"Non-empty replacement exercise name; it is trimmed and lowercased before storage."`
	WeightKG  *float64 `json:"weight_kg,omitempty" jsonschema:"Replacement weight in kilograms; must be strictly positive."`
	Reps      *int     `json:"reps,omitempty" jsonschema:"Replacement repetition count; must be strictly positive."`
	RPE       *float64 `json:"rpe,omitempty" jsonschema:"Replacement numeric RPE from 1 through 10; recalculates the set's SI."`
	Technique *string  `json:"technique,omitempty" jsonschema:"Replacement intensity technique. Pass an empty string to clear it back to a normal set."`
	Category  *string  `json:"category,omitempty" jsonschema:"Replacement set category: 'warmup' marks the set as a warm-up, which stops counting toward SI, volume, set counts, plan progress, records and estimates; 'working' makes it count again. Use it when the user says a logged set was a warm-up, instead of writing warm-up in technique."`
}
type DeleteInput struct {
	SetID int64 `json:"set_id" jsonschema:"Positive ID of the existing set to delete."`
}
type DeleteOut struct {
	SessionID int64   `json:"session_id"`
	TotalSI   float64 `json:"total_si"`
	Remaining int     `json:"remaining"`
	ProposalRef
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
type SimulatePlanInput struct {
	PlanID          int64           `json:"plan_id,omitempty" jsonschema:"Positive ID of a saved plan to simulate. Give plan_id or items, not both."`
	Items           []PlanItemInput `json:"items,omitempty" jsonschema:"Unsaved exercises to simulate, in the same shape create_plan takes. Give plan_id or items, not both."`
	SessionsPerWeek int             `json:"sessions_per_week,omitempty" jsonschema:"How many times a week the plan is done, 1 through 7. Defaults to 1."`
}
type SimulatePlanOut struct {
	Simulation training.PlanSimulation `json:"simulation"`
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
	ProposalRef
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
	Superset   string  `json:"superset,omitempty" jsonschema:"Label grouping exercises done back to back. Omit for a standalone exercise, or to keep the current label when the exercise is already in the session."`
	Notes      string  `json:"notes,omitempty" jsonschema:"Free-text intent for this exercise in today's session. Omit to keep the current notes when the exercise is already in the session."`
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
	ProposalRef
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
	ProposalRef
}
type LandmarksInput struct {
	MuscleGroup string `json:"muscle_group" jsonschema:"Muscle group; must be one of the supported values."`
	MEV         int    `json:"mev,omitempty" jsonschema:"Minimum effective volume in weekly sets: below this the muscle stops growing. 0 means unknown."`
	MRV         int    `json:"mrv,omitempty" jsonschema:"Maximum recoverable volume in weekly sets: above this fatigue outpaces recovery. 0 means unknown."`
}
type LandmarksOut struct {
	Landmarks []training.Landmarks `json:"landmarks"`
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
type LiveWorkoutOut struct {
	Live training.LiveWorkout `json:"live"`
}
type RestTargetInput struct {
	RestSeconds int `json:"rest_seconds" jsonschema:"Target rest between sets in seconds, 30 through 900."`
}
type RestTargetOut struct {
	RestSeconds int `json:"rest_seconds"`
	ProposalRef
}
type StopRestInput struct {
	SetID       int64 `json:"set_id,omitempty" jsonschema:"The set the rest followed. Give it with rest_seconds, or omit both to stop the rest running now."`
	RestSeconds *int  `json:"rest_seconds,omitempty" jsonschema:"Seconds rested after that set. Give it with set_id."`
}

// resolve fills in the rest running now when the call names neither the set
// nor the seconds, so the proposal stores what the timer showed when it was
// stopped, not what it would show once someone accepts it.
func (in *StopRestInput) resolve(ctx context.Context, svc *training.Service) error {
	switch {
	case in.SetID == 0 && in.RestSeconds == nil:
		id, elapsed, err := svc.RunningRest(ctx)
		if err != nil {
			return err
		}
		in.SetID, in.RestSeconds = id, &elapsed
		return nil
	case in.SetID == 0 || in.RestSeconds == nil:
		return fmt.Errorf("%w: give set_id and rest_seconds together, or neither", training.ErrValidation)
	}
	return nil
}

type StopRestOut struct {
	SetID         int64 `json:"set_id"`
	RestedSeconds int   `json:"rested_seconds"`
	ProposalRef
}
type NotesOut struct {
	Notes []training.ExerciseNote `json:"notes"`
}
type DeleteSessionOut struct {
	SessionID   int64 `json:"session_id"`
	DeletedSets int   `json:"deleted_sets"`
	ProposalRef
}
type HistoryInput struct {
	Exercise string `json:"exercise" jsonschema:"Non-empty exercise name; it is trimmed and lowercased to match stored sets."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum sets to return; defaults to 50 and accepts 1 through 500."`
}
type HistoryOut struct {
	History training.ExerciseHistory `json:"history"`
}
type PerformanceInput struct {
	SessionID int64  `json:"session_id" jsonschema:"Positive id of the session to analyse."`
	Exercise  string `json:"exercise,omitempty" jsonschema:"Optional exercise name, trimmed and lowercased; omit to analyse every exercise of the session."`
}
type PerformanceOut struct {
	Analyses []training.DropOffAnalysis `json:"analyses"`
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
	ProposalRef
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

func New(service training.ServiceFor) *Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "training-mcp", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: serverInstructions})
	w := &writes{server: s, service: service, actions: map[string]writeAction{}}
	startSchema := mustInputSchema[StartInput]()
	startSchema.Properties["date"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	addWrite(w, &mcp.Tool{Name: "start_session", Description: "Start a training session on an optional date, optionally following a plan. Omit date to use the server's current local date.", InputSchema: startSchema}, "session.start", func(ctx context.Context, svc *training.Service, in StartInput) (StartOut, error) {
		v, err := svc.StartPlannedSession(ctx, in.Date, in.PlanID)
		return StartOut{Session: v}, err
	})
	addSchema := mustInputSchema[AddInput]()
	setPositiveID(addSchema.Properties["session_id"])
	addSchema.Properties["exercise"].Pattern = `.*\S.*`
	addSchema.Properties["weight_kg"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	addSchema.Properties["reps"].Minimum = jsonschema.Ptr(1.0)
	setRPERange(addSchema.Properties["rpe"])
	setCategoryEnum(addSchema.Properties["category"])
	addWrite(w, &mcp.Tool{Name: "add_set", Description: "Add a set to an existing training session and return the set plus the session's recalculated total SI.", InputSchema: addSchema}, "set.log", func(ctx context.Context, svc *training.Service, in AddInput) (SetOut, error) {
		v, total, err := svc.AddSet(ctx, training.AddSetInput{SessionID: in.SessionID, Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, Technique: in.Technique, Category: in.Category})
		return SetOut{Set: v, TotalSI: total}, err
	})
	logSchema := mustInputSchema[LogInput]()
	logSchema.Properties["exercise"].Pattern = `.*\S.*`
	logSchema.Properties["weight_kg"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	logSchema.Properties["reps"].Minimum = jsonschema.Ptr(1.0)
	setRPERange(logSchema.Properties["rpe"])
	logSchema.Properties["count"].Default = json.RawMessage("1")
	logSchema.Properties["count"].Minimum = jsonschema.Ptr(1.0)
	logSchema.Properties["count"].Maximum = jsonschema.Ptr(20.0)
	setCategoryEnum(logSchema.Properties["category"])
	addWrite(w, &mcp.Tool{Name: "log_set", Description: "Record one or more identical sets into today's session, creating that session if it does not exist yet. It needs no session id but is not safe to retry, since repeating it records the sets again. Prefer log_sets, which is.", InputSchema: logSchema}, "set.log", func(ctx context.Context, svc *training.Service, in LogInput) (LogOut, error) {
		session, sets, err := svc.LogSet(ctx, in.Exercise, in.WeightKG, in.Reps, in.RPE, in.Count, in.Technique, in.Category)
		return LogOut{Session: session, Sets: sets, TotalSI: session.TotalSI}, err
	})
	logSetsSchema := mustInputSchema[LogSetsInput]()
	logSetsSchema.Properties["idempotency_key"].Pattern = `.*\S.*`
	logSetsSchema.Properties["idempotency_key"].MaxLength = jsonschema.Ptr(128)
	logSetsSchema.Properties["exercise"].Pattern = `.*\S.*`
	logSetsSchema.Properties["sets"].MinItems = jsonschema.Ptr(1)
	logSetsSchema.Properties["sets"].MaxItems = jsonschema.Ptr(20)
	loggedSetSchema := logSetsSchema.Properties["sets"].Items
	loggedSetSchema.Properties["weight_kg"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	loggedSetSchema.Properties["reps"].Minimum = jsonschema.Ptr(1.0)
	setNonNullableType(loggedSetSchema.Properties["rpe"], "number")
	setRPERange(loggedSetSchema.Properties["rpe"])
	setCategoryEnum(loggedSetSchema.Properties["category"])
	addWrite(w, &mcp.Tool{Name: "log_sets", Description: "Record the sets of one exercise into today's session in a single call, creating the session if needed. The sets may differ, as in '12, 12, 10 and 8 reps'. All are stored or none. Omit rpe unless the user stated it. Repeating a call with the same idempotency_key returns the original result with replayed true and stores nothing new, so retrying is always safe. Prefer this over log_set.", InputSchema: logSetsSchema}, "set.log", func(ctx context.Context, svc *training.Service, in LogSetsInput) (LogSetsOut, error) {
		batch := training.LogSetsInput{Key: in.IdempotencyKey, Exercise: in.Exercise}
		for _, set := range in.Sets {
			logged := training.LoggedSet{WeightKG: set.WeightKG, Reps: set.Reps, Technique: set.Technique, Category: set.Category}
			if set.RPE != nil {
				logged.RPE = *set.RPE
			}
			batch.Sets = append(batch.Sets, logged)
		}
		v, err := svc.LogSets(ctx, batch)
		return LogSetsOut{Session: v.Session, Sets: v.Sets, TotalSI: v.Session.TotalSI, Replayed: v.Replayed}, err
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
	setNonNullableType(updateSchema.Properties["category"], "string")
	setCategoryEnum(updateSchema.Properties["category"])
	addWrite(w, &mcp.Tool{Name: "update_set", Description: "Update one or more fields of an existing set. Omitted fields remain unchanged; RPE changes recalculate SI. Set category to 'warmup' when the user says a logged set was a warm-up, or to 'working' to undo that.", InputSchema: updateSchema}, "set.update", func(ctx context.Context, svc *training.Service, in UpdateInput) (SetOut, error) {
		v, total, err := svc.UpdateSet(ctx, in.SetID, training.SetPatch{Exercise: in.Exercise, WeightKG: in.WeightKG, Reps: in.Reps, RPE: in.RPE, Technique: in.Technique, Category: in.Category})
		return SetOut{Set: v, TotalSI: total}, err
	})
	deleteSchema := mustInputSchema[DeleteInput]()
	setPositiveID(deleteSchema.Properties["set_id"])
	addWrite(w, &mcp.Tool{Name: "delete_set", Description: "Permanently delete an existing set and compact the remaining set positions to a dense sequence.", InputSchema: deleteSchema}, "set.delete", func(ctx context.Context, svc *training.Service, in DeleteInput) (DeleteOut, error) {
		sid, total, n, err := svc.DeleteSet(ctx, in.SetID)
		return DeleteOut{SessionID: sid, TotalSI: total, Remaining: n}, err
	})
	sessionSchema := mustInputSchema[SessionInput]()
	setPositiveID(sessionSchema.Properties["session_id"])
	addTool(s, &mcp.Tool{Name: "get_session", Description: "Get an existing training session with its ordered sets and total SI.", InputSchema: sessionSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, SessionOut, error) {
		v, err := service(ctx).GetSession(ctx, in.SessionID)
		return nil, SessionOut{Session: v}, toolError(err)
	})
	listSchema := mustInputSchema[ListInput]()
	listSchema.Properties["limit"].Default = json.RawMessage("20")
	listSchema.Properties["limit"].Minimum = jsonschema.Ptr(1.0)
	listSchema.Properties["limit"].Maximum = jsonschema.Ptr(100.0)
	listSchema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	listSchema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	addTool(s, &mcp.Tool{Name: "list_sessions", Description: "List training sessions newest first, optionally filtered by inclusive date bounds. From must not be after to.", InputSchema: listSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOut, error) {
		v, err := service(ctx).ListSessions(ctx, training.ListFilter{Limit: in.Limit, From: in.From, To: in.To})
		return nil, ListOut{Sessions: v}, toolError(err)
	})
	createPlanSchema := mustInputSchema[CreatePlanInput]()
	createPlanSchema.Properties["name"].Pattern = `.*\S.*`
	addWrite(w, &mcp.Tool{Name: "create_plan", Description: "Create a reusable workout plan: an ordered list of exercises with a target set count and an optional rep range and RPE. Load is not planned; it is filled in at the gym.", InputSchema: createPlanSchema}, "plan.create", func(ctx context.Context, svc *training.Service, in CreatePlanInput) (PlanWriteOut, error) {
		plan := training.Plan{Name: in.Name, Notes: in.Notes}
		for _, it := range in.Items {
			plan.Items = append(plan.Items, training.PlanItem{
				Exercise: it.Exercise, TargetSets: it.TargetSets,
				RepMin: it.RepMin, RepMax: it.RepMax, TargetRPE: it.TargetRPE,
				Superset: it.Superset, Notes: it.Notes,
			})
		}
		v, err := svc.CreatePlan(ctx, plan)
		return PlanWriteOut{Plan: v}, err
	})
	addTool(s, &mcp.Tool{Name: "list_plans", Description: "List every saved workout plan with its total planned set count."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, PlansOut, error) {
		v, err := service(ctx).ListPlans(ctx)
		return nil, PlansOut{Plans: v}, toolError(err)
	})
	planSchema := mustInputSchema[PlanInput]()
	setPositiveID(planSchema.Properties["plan_id"])
	addTool(s, &mcp.Tool{Name: "get_plan", Description: "Get one plan with its ordered exercises, target sets, rep ranges and RPEs.", InputSchema: planSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in PlanInput) (*mcp.CallToolResult, PlanOut, error) {
		v, err := service(ctx).GetPlan(ctx, in.PlanID)
		return nil, PlanOut{Plan: v}, toolError(err)
	})
	updatePlanSchema := mustInputSchema[UpdatePlanInput]()
	setPositiveID(updatePlanSchema.Properties["plan_id"])
	updatePlanSchema.MinProperties = jsonschema.Ptr(2)
	setNonNullableType(updatePlanSchema.Properties["name"], "string")
	setNonNullableType(updatePlanSchema.Properties["notes"], "string")
	addWrite(w, &mcp.Tool{Name: "update_plan", Description: "Edit a plan's name or notes without touching its exercises. Plan notes are free text describing the routine's intent, and are returned by get_plan and list_plans.", InputSchema: updatePlanSchema}, "plan.update", func(ctx context.Context, svc *training.Service, in UpdatePlanInput) (PlanWriteOut, error) {
		if err := svc.UpdatePlan(ctx, in.PlanID, training.PlanPatch{Name: in.Name, Notes: in.Notes}); err != nil {
			return PlanWriteOut{}, err
		}
		v, err := svc.GetPlan(ctx, in.PlanID)
		return PlanWriteOut{Plan: v}, err
	})

	planItemSchema := mustInputSchema[PlanItemEditInput]()
	setPositiveID(planItemSchema.Properties["plan_id"])
	planItemSchema.Properties["exercise"].Pattern = `.*\S.*`
	addWrite(w, &mcp.Tool{Name: "set_plan_exercise", Description: "Add an exercise to a saved plan, or replace its prescription. Editing a template never changes sessions already started from it.", InputSchema: planItemSchema}, "plan.exercise.set", func(ctx context.Context, svc *training.Service, in PlanItemEditInput) (PlanWriteOut, error) {
		err := svc.SetPlanItem(ctx, in.PlanID, training.PlanItem{
			Exercise: in.Exercise, TargetSets: in.TargetSets, RepMin: in.RepMin,
			RepMax: in.RepMax, TargetRPE: in.TargetRPE, Superset: in.Superset, Notes: in.Notes,
		})
		if err != nil {
			return PlanWriteOut{}, err
		}
		v, err := svc.GetPlan(ctx, in.PlanID)
		return PlanWriteOut{Plan: v}, err
	})
	planRefSchema := mustInputSchema[PlanItemRefInput]()
	setPositiveID(planRefSchema.Properties["plan_id"])
	planRefSchema.Properties["exercise"].Pattern = `.*\S.*`
	addWrite(w, &mcp.Tool{Name: "remove_plan_exercise", Description: "Remove an exercise from a saved plan and close the gap in its order.", InputSchema: planRefSchema}, "plan.exercise.remove", func(ctx context.Context, svc *training.Service, in PlanItemRefInput) (PlanWriteOut, error) {
		if err := svc.RemovePlanItem(ctx, in.PlanID, in.Exercise); err != nil {
			return PlanWriteOut{}, err
		}
		v, err := svc.GetPlan(ctx, in.PlanID)
		return PlanWriteOut{Plan: v}, err
	})
	planMoveSchema := mustInputSchema[PlanMoveInput]()
	setPositiveID(planMoveSchema.Properties["plan_id"])
	planMoveSchema.Properties["exercise"].Pattern = `.*\S.*`
	planMoveSchema.Properties["direction"].Enum = []any{"up", "down"}
	addWrite(w, &mcp.Tool{Name: "move_plan_exercise", Description: "Move an exercise one place up or down within a plan. The order is the order the session is performed in.", InputSchema: planMoveSchema}, "plan.exercise.move", func(ctx context.Context, svc *training.Service, in PlanMoveInput) (PlanWriteOut, error) {
		delta := 1
		if in.Direction == "up" {
			delta = -1
		}
		if err := svc.MovePlanItem(ctx, in.PlanID, in.Exercise, delta); err != nil {
			return PlanWriteOut{}, err
		}
		v, err := svc.GetPlan(ctx, in.PlanID)
		return PlanWriteOut{Plan: v}, err
	})

	deletePlanSchema := mustInputSchema[PlanInput]()
	setPositiveID(deletePlanSchema.Properties["plan_id"])
	addWrite(w, &mcp.Tool{Name: "delete_plan", Description: "Permanently delete a plan. Sessions that followed it keep their recorded plan name.", InputSchema: deletePlanSchema}, "plan.delete", func(ctx context.Context, svc *training.Service, in PlanInput) (PlanDeleteOut, error) {
		err := svc.DeletePlan(ctx, in.PlanID)
		return PlanDeleteOut{PlanID: in.PlanID}, err
	})
	progressSchema := mustInputSchema[SessionInput]()
	setPositiveID(progressSchema.Properties["session_id"])
	addTool(s, &mcp.Tool{Name: "session_progress", Description: "For a session following a plan: planned versus completed sets per exercise, with the prescribed rep range and RPE. Exercises done off-plan are listed with target_sets 0.", InputSchema: progressSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, ProgressOut, error) {
		v, err := service(ctx).SessionProgress(ctx, in.SessionID)
		out := ProgressOut{SessionID: in.SessionID, Progress: v}
		if sess, e := service(ctx).GetSession(ctx, in.SessionID); e == nil {
			out.PlanName = sess.PlanName
		}
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{Name: "live_workout", Description: "What is happening in today's workout right now: the session with its sets, planned versus done per exercise, the exercise planned next, the last set and when it was logged, and the rest since it against the person's target (state none, resting, ready, superset or idle, with elapsed, remaining or over-by seconds). Call it before giving feedback during a workout. Needs no session id."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, LiveWorkoutOut, error) {
		v, err := service(ctx).LiveWorkout(ctx)
		return nil, LiveWorkoutOut{Live: v}, toolError(err)
	})
	restSchema := mustInputSchema[RestTargetInput]()
	restSchema.Properties["rest_seconds"].Minimum = jsonschema.Ptr(float64(training.MinRestSeconds))
	restSchema.Properties["rest_seconds"].Maximum = jsonschema.Ptr(float64(training.MaxRestSeconds))
	addWrite(w, &mcp.Tool{Name: "set_rest_target", Description: "Set the person's target rest between sets, in seconds. The phone's rest timer and live_workout use it. Until set it is 120 seconds.", InputSchema: restSchema}, "rest.set", func(ctx context.Context, svc *training.Service, in RestTargetInput) (RestTargetOut, error) {
		if err := svc.SetRestTarget(ctx, in.RestSeconds); err != nil {
			return RestTargetOut{}, err
		}
		v, err := svc.RestTarget(ctx)
		return RestTargetOut{RestSeconds: v}, err
	})

	stopSchema := mustInputSchema[StopRestInput]()
	setPositiveID(stopSchema.Properties["set_id"])
	stopSchema.Properties["rest_seconds"].Minimum = jsonschema.Ptr(float64(0))
	stopSchema.Properties["rest_seconds"].Maximum = jsonschema.Ptr(float64(training.RestIdleAfter / time.Second))
	addWrite(w, &mcp.Tool{Name: "stop_rest", Description: "Stop the rest between sets and store how long the person rested, on the set the rest followed. With no arguments it stops the rest running now (state resting or ready in live_workout) at the seconds elapsed. To record a rest the person reports, give set_id and rest_seconds. A rest is stored once per set; live_workout then shows state stopped with rested_seconds.", InputSchema: stopSchema}, "rest.stop", func(ctx context.Context, svc *training.Service, in StopRestInput) (StopRestOut, error) {
		if in.RestSeconds == nil {
			return StopRestOut{}, training.ErrValidation
		}
		err := svc.RecordRest(ctx, in.SetID, *in.RestSeconds)
		return StopRestOut{SetID: in.SetID, RestedSeconds: *in.RestSeconds}, err
	})

	setItemSchema := mustInputSchema[SessionItemInput]()
	setPositiveID(setItemSchema.Properties["session_id"])
	setItemSchema.Properties["exercise"].Pattern = `.*\S.*`
	addWrite(w, &mcp.Tool{Name: "add_session_exercise", Description: "Add an exercise to today's session plan. If it is already there, target_sets is replaced and every optional field you omit (rep range, RPE, superset, notes) keeps its current value; sending either rep bound replaces the whole range. To clear a field, remove the exercise and add it again. Adjusting a session never edits the plan it was started from.", InputSchema: setItemSchema}, "session.exercise.set", func(ctx context.Context, svc *training.Service, in SessionItemInput) (ItemOut, error) {
		err := svc.SetSessionItem(ctx, in.SessionID, training.PlanItem{
			Exercise: in.Exercise, TargetSets: in.TargetSets,
			RepMin: in.RepMin, RepMax: in.RepMax, TargetRPE: in.TargetRPE,
			Superset: in.Superset, Notes: in.Notes,
		})
		return ItemOut{SessionID: in.SessionID, Exercise: in.Exercise}, err
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
	addWrite(w, &mcp.Tool{Name: "adjust_session_exercise", Description: "Change one exercise of today's session: its set count, rep range, RPE, or whether it is skipped. Omitted fields stay as they are. Use when the session does not go as planned.", InputSchema: adjustSchema}, "session.exercise.adjust", func(ctx context.Context, svc *training.Service, in AdjustItemInput) (ItemOut, error) {
		err := svc.AdjustSessionItem(ctx, in.SessionID, in.Exercise, training.SessionItemPatch{
			TargetSets: in.TargetSets, RepMin: in.RepMin, RepMax: in.RepMax,
			TargetRPE: in.TargetRPE, Skipped: in.Skipped,
		})
		return ItemOut{SessionID: in.SessionID, Exercise: in.Exercise}, err
	})
	swapSchema := mustInputSchema[SwapItemInput]()
	setPositiveID(swapSchema.Properties["session_id"])
	swapSchema.Properties["exercise"].Pattern = `.*\S.*`
	swapSchema.Properties["replacement"].Pattern = `.*\S.*`
	addWrite(w, &mcp.Tool{Name: "swap_session_exercise", Description: "Substitute one exercise for another in today's session, keeping the same prescription. Use when the planned machine is taken or the movement does not feel right.", InputSchema: swapSchema}, "session.exercise.swap", func(ctx context.Context, svc *training.Service, in SwapItemInput) (ItemOut, error) {
		err := svc.SwapSessionItem(ctx, in.SessionID, in.Exercise, in.Replacement)
		return ItemOut{SessionID: in.SessionID, Exercise: in.Replacement}, err
	})
	removeItemSchema := mustInputSchema[RemoveItemInput]()
	setPositiveID(removeItemSchema.Properties["session_id"])
	removeItemSchema.Properties["exercise"].Pattern = `.*\S.*`
	addWrite(w, &mcp.Tool{Name: "remove_session_exercise", Description: "Drop an exercise from today's session plan. Sets already logged against it are kept and reappear as off-plan work.", InputSchema: removeItemSchema}, "session.exercise.remove", func(ctx context.Context, svc *training.Service, in RemoveItemInput) (ItemOut, error) {
		err := svc.RemoveSessionItem(ctx, in.SessionID, in.Exercise)
		return ItemOut{SessionID: in.SessionID, Exercise: in.Exercise}, err
	})
	saveAsPlanSchema := mustInputSchema[SaveAsPlanInput]()
	setPositiveID(saveAsPlanSchema.Properties["session_id"])
	saveAsPlanSchema.Properties["name"].Pattern = `.*\S.*`
	addWrite(w, &mcp.Tool{Name: "save_session_as_plan", Description: "Turn a session — including any in-session adjustments and off-plan work — into a new reusable plan. Skipped exercises are left out.", InputSchema: saveAsPlanSchema}, "plan.create", func(ctx context.Context, svc *training.Service, in SaveAsPlanInput) (PlanWriteOut, error) {
		v, err := svc.SaveSessionAsPlan(ctx, in.SessionID, in.Name)
		return PlanWriteOut{Plan: v}, err
	})

	loadSchema := mustInputSchema[LoadInput]()
	loadSchema.Properties["exercise"].Pattern = `.*\S.*`
	addTool(s, &mcp.Tool{Name: "suggest_load", Description: "Suggest the next working weight for an exercise, judged against the rep range and RPE it is prescribed at. Advisory only: it explains its reasoning and never changes a plan. Sets carrying an intensity technique are ignored, since they are not comparable to straight sets, and so are warm-up sets.", InputSchema: loadSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoadInput) (*mcp.CallToolResult, LoadSuggestionOut, error) {
		v, err := service(ctx).SuggestLoad(ctx, in.Exercise)
		return nil, LoadSuggestionOut{Suggestion: v}, toolError(err)
	})

	noteSchema := mustInputSchema[NoteInput]()
	noteSchema.Properties["exercise"].Pattern = `.*\S.*`
	addWriteBy(w, &mcp.Tool{Name: "set_exercise_note", Description: "Store a persistent setup reminder for an exercise, such as seat height or grip width. It is shown every time that exercise is logged. An empty note removes it.", InputSchema: noteSchema}, noteAction, func(ctx context.Context, svc *training.Service, in NoteInput) (NotesWriteOut, error) {
		if err := svc.SetExerciseNote(ctx, in.Exercise, in.Note); err != nil {
			return NotesWriteOut{}, err
		}
		v, err := svc.ExerciseNotes(ctx)
		return NotesWriteOut{Notes: v}, err
	})
	addTool(s, &mcp.Tool{Name: "list_exercise_notes", Description: "List every exercise that has a setup note."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, NotesOut, error) {
		v, err := service(ctx).ExerciseNotes(ctx)
		return nil, NotesOut{Notes: v}, toolError(err)
	})

	feedbackSchema := mustInputSchema[FeedbackInput]()
	setPositiveID(feedbackSchema.Properties["session_id"])
	feedbackSchema.Properties["muscle_group"].Enum = muscleGroupEnum()
	for _, dim := range []string{"fatigue", "pump", "recovery"} {
		feedbackSchema.Properties[dim].Minimum = jsonschema.Ptr(0.0)
		feedbackSchema.Properties[dim].Maximum = jsonschema.Ptr(3.0)
	}
	addWrite(w, &mcp.Tool{Name: "record_feedback", Description: "Rate how one muscle group responded to a session: fatigue, pump and recovery, each 0 to 3. Only rate groups actually trained. This drives next week's set-count recommendation.", InputSchema: feedbackSchema}, "feedback.record", func(ctx context.Context, svc *training.Service, in FeedbackInput) (FeedbackOut, error) {
		err := svc.RecordFeedback(ctx, in.SessionID, training.Feedback{
			MuscleGroup: in.MuscleGroup, Fatigue: in.Fatigue, Pump: in.Pump, Recovery: in.Recovery,
		})
		if err != nil {
			return FeedbackOut{SessionID: in.SessionID}, err
		}
		all, err := svc.SessionFeedback(ctx, in.SessionID)
		return FeedbackOut{SessionID: in.SessionID, Feedback: all}, err
	})
	landmarksSchema := mustInputSchema[LandmarksInput]()
	landmarksSchema.Properties["muscle_group"].Enum = muscleGroupEnum()
	landmarksSchema.Properties["mev"].Minimum = jsonschema.Ptr(0.0)
	landmarksSchema.Properties["mrv"].Minimum = jsonschema.Ptr(0.0)
	addWrite(w, &mcp.Tool{Name: "set_volume_landmarks", Description: "Record a muscle group's personal weekly-set boundaries: MEV below which it stops growing, MRV above which fatigue outpaces recovery. These are individual, and volume_recommendation refuses to push past them. Leave a value at 0 if unknown.", InputSchema: landmarksSchema}, "landmarks.set", func(ctx context.Context, svc *training.Service, in LandmarksInput) (LandmarksWriteOut, error) {
		if err := svc.SetLandmarks(ctx, training.Landmarks{MuscleGroup: in.MuscleGroup, MEV: in.MEV, MRV: in.MRV}); err != nil {
			return LandmarksWriteOut{}, err
		}
		v, err := svc.Landmarks(ctx)
		return LandmarksWriteOut{Landmarks: v}, err
	})
	addTool(s, &mcp.Tool{Name: "list_volume_landmarks", Description: "List the muscle groups that have personal volume landmarks recorded."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, LandmarksOut, error) {
		v, err := service(ctx).Landmarks(ctx)
		return nil, LandmarksOut{Landmarks: v}, toolError(err)
	})
	addTool(s, &mcp.Tool{Name: "volume_recommendation", Description: "Set-count change per muscle group for next week. Weights recent feedback over older rather than reading only the last, reports how many rated sessions it is based on and the resulting confidence, declines to advise on a single rating, and never pushes volume past a recorded MEV or MRV."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, RecommendOut, error) {
		v, err := service(ctx).VolumeRecommendation(ctx)
		return nil, RecommendOut{Recommendations: v}, toolError(err)
	})

	simulateSchema := mustInputSchema[SimulatePlanInput]()
	setPositiveID(simulateSchema.Properties["plan_id"])
	simulateSchema.Properties["sessions_per_week"].Minimum = jsonschema.Ptr(1.0)
	simulateSchema.Properties["sessions_per_week"].Maximum = jsonschema.Ptr(7.0)
	simulateSchema.Properties["items"].MaxItems = jsonschema.Ptr(40)
	addTool(s, &mcp.Tool{Name: "simulate_plan", Description: "What a plan would do to a week of training, before it is created or changed. Give a saved plan_id or unsaved items, and sessions_per_week. Returns weekly working sets and SI per muscle group, each compared with the person's MEV and MRV (below_mev, within, above_mrv, no_landmarks), the weekly totals, and exercises with no muscle group. Deterministic and read-only: it stores nothing and creates no proposal. Call it before proposing create_plan or set_plan_exercise.", InputSchema: simulateSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in SimulatePlanInput) (*mcp.CallToolResult, SimulatePlanOut, error) {
		sim := training.PlanSimulationInput{PlanID: in.PlanID, SessionsPerWeek: in.SessionsPerWeek}
		for _, it := range in.Items {
			sim.Items = append(sim.Items, training.PlanItem{
				Exercise: it.Exercise, TargetSets: it.TargetSets,
				RepMin: it.RepMin, RepMax: it.RepMax, TargetRPE: it.TargetRPE,
				Superset: it.Superset, Notes: it.Notes,
			})
		}
		v, err := service(ctx).SimulatePlan(ctx, sim)
		return nil, SimulatePlanOut{Simulation: v}, toolError(err)
	})

	deleteSessionSchema := mustInputSchema[SessionInput]()
	setPositiveID(deleteSessionSchema.Properties["session_id"])
	addWrite(w, &mcp.Tool{Name: "delete_session", Description: "Permanently delete a training session and every set in it. Irreversible; returns how many sets were destroyed. Use to remove an empty or mistaken session.", InputSchema: deleteSessionSchema}, "session.delete", func(ctx context.Context, svc *training.Service, in SessionInput) (DeleteSessionOut, error) {
		n, err := svc.DeleteSession(ctx, in.SessionID)
		return DeleteSessionOut{SessionID: in.SessionID, DeletedSets: n}, err
	})
	historySchema := mustInputSchema[HistoryInput]()
	historySchema.Properties["exercise"].Pattern = `.*\S.*`
	historySchema.Properties["limit"].Default = json.RawMessage("50")
	historySchema.Properties["limit"].Minimum = jsonschema.Ptr(1.0)
	historySchema.Properties["limit"].Maximum = jsonschema.Ptr(500.0)
	addTool(s, &mcp.Tool{Name: "exercise_history", Description: "All recorded sets of one exercise, newest first, with each set's estimated 1RM, plus the best set ever recorded for it. Use this to judge progression instead of reading whole sessions.", InputSchema: historySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in HistoryInput) (*mcp.CallToolResult, HistoryOut, error) {
		v, err := service(ctx).ExerciseHistory(ctx, in.Exercise, in.Limit)
		return nil, HistoryOut{History: v}, toolError(err)
	})
	perfSchema := mustInputSchema[PerformanceInput]()
	setPositiveID(perfSchema.Properties["session_id"])
	addTool(s, &mcp.Tool{Name: "performance_dropoff", Description: "Apparent per-set performance drop within one session, per exercise, computed deterministically from load, reps and stated RPE with named estimators (epley_v1, rir_epley_v1, brzycki_v1). Lists which sets were comparable and why others were excluded (recorded warm-up, technique, no load, probable warm-up), the reference set, each model's drop and confidence, and abstains with fewer than two comparable sets. An unstated RPE never feeds the RIR model. A device-free proxy: not velocity, force or fatigue, and it changes nothing.", InputSchema: perfSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in PerformanceInput) (*mcp.CallToolResult, PerformanceOut, error) {
		v, err := service(ctx).PerformanceDropOff(ctx, in.SessionID, in.Exercise)
		if v == nil {
			v = []training.DropOffAnalysis{}
		}
		return nil, PerformanceOut{Analyses: v}, toolError(err)
	})
	weeklySchema := mustInputSchema[VolumeInput]()
	weeklySchema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	weeklySchema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	addTool(s, &mcp.Tool{Name: "weekly_volume", Description: "SI and set count per muscle group per training week, newest week first. Warm-up sets are not counted. Week start is the Monday of that week. Use this to see whether a muscle group's stimulus is rising or falling over time.", InputSchema: weeklySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in VolumeInput) (*mcp.CallToolResult, WeeklyOut, error) {
		v, err := service(ctx).WeeklyVolume(ctx, training.ListFilter{From: in.From, To: in.To})
		return nil, WeeklyOut{Weeks: v}, toolError(err)
	})

	groupSchema := mustInputSchema[GroupInput]()
	groupSchema.Properties["exercise"].Pattern = `.*\S.*`
	groupSchema.Properties["muscle_group"].Enum = muscleGroupEnum()
	addWrite(w, &mcp.Tool{Name: "set_exercise_group", Description: "Assign an exercise to one muscle group so its sets count toward that group's volume. Re-assigning replaces the previous group.", InputSchema: groupSchema}, "exercise.group.set", func(ctx context.Context, svc *training.Service, in GroupInput) (GroupOut, error) {
		err := svc.SetExerciseGroup(ctx, in.Exercise, in.MuscleGroup)
		return GroupOut{Exercise: in.Exercise, MuscleGroup: in.MuscleGroup}, err
	})
	addTool(s, &mcp.Tool{Name: "list_exercise_groups", Description: "List every exercise that has a muscle group assigned, ordered by group."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, GroupsOut, error) {
		v, err := service(ctx).ExerciseGroups(ctx)
		return nil, GroupsOut{Groups: v}, toolError(err)
	})
	volumeSchema := mustInputSchema[VolumeInput]()
	volumeSchema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	volumeSchema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	addTool(s, &mcp.Tool{Name: "volume_by_muscle", Description: "Total SI and set count per muscle group, optionally within an inclusive date range. Warm-up sets are not counted. Sets whose exercise has no group yet are reported under an empty muscle_group.", InputSchema: volumeSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in VolumeInput) (*mcp.CallToolResult, VolumeOut, error) {
		v, err := service(ctx).VolumeByGroup(ctx, training.ListFilter{From: in.From, To: in.To})
		return nil, VolumeOut{Volume: v}, toolError(err)
	})
	addMemoryTools(s, w, service)
	addLbTools(s, w, service)
	addApps(s, service)

	// Stateless: the server keeps no per-client session state, so restarting the
	// container no longer invalidates a live conversation. These tools are plain
	// request/response with no server-initiated notifications, so nothing is lost.
	h := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	return &Server{mcp: s, handler: h, writes: w}
}
func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) ToolNames() []string {
	return []string{"log_sets", "log_set", "start_session", "add_set", "update_set", "delete_set", "delete_session",
		"get_session", "list_sessions", "exercise_history", "weekly_volume",
		"set_exercise_group", "list_exercise_groups", "volume_by_muscle",
		"create_plan", "list_plans", "get_plan", "update_plan", "delete_plan", "session_progress",
		"set_plan_exercise", "remove_plan_exercise", "move_plan_exercise",
		"add_session_exercise", "adjust_session_exercise", "swap_session_exercise",
		"remove_session_exercise", "save_session_as_plan",
		"record_feedback", "volume_recommendation", "simulate_plan",
		"set_volume_landmarks", "list_volume_landmarks",
		"set_exercise_note", "list_exercise_notes", "suggest_load", "performance_dropoff",
		"preview_lb_correction", "correct_lb_sets", "render_rest_timer"}
}

// noteAction tells setting a note from removing one: an empty note deletes it.
func noteAction(in NoteInput) string {
	if strings.TrimSpace(in.Note) == "" {
		return "exercise.note.remove"
	}
	return "exercise.note.set"
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

// setCategoryEnum limits a set category to the two the store knows.
func setCategoryEnum(schema *jsonschema.Schema) {
	schema.Enum = []any{training.SetWorking, training.SetWarmup}
}
func setRPERange(schema *jsonschema.Schema) {
	schema.Minimum = jsonschema.Ptr(1.0)
	schema.Maximum = jsonschema.Ptr(10.0)
}
func toolError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, training.ErrValidation) || errors.Is(err, training.ErrNotFound) || errors.Is(err, training.ErrIdempotencyConflict) ||
		errors.Is(err, training.ErrStale) || errors.Is(err, training.ErrNotPending) {
		return err
	}
	return fmt.Errorf("internal tool error")
}
