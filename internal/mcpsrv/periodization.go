package mcpsrv

import (
	"context"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Periodization tools (#41). One intent per tool, no generic action
// dispatcher. A microcycle's modifiers are applied by start_session to the
// session's copy of its routine; training_status shows them applied to the
// routine due next.

// Action types. Shortening a block drops microcycles, so it is a deleting
// action that accept_except_delete leaves to the person.
const (
	goalCreateAction         = "goal.create"
	goalUpdateAction         = "goal.update"
	blockCreateAction        = "block.create"
	blockUpdateAction        = "block.update"
	blockShortenAction       = "block.shorten"
	blockActivateAction      = "block.activate"
	blockCompleteAction      = "block.microcycle.complete"
	blockSkipAction          = "block.microcycle.skip"
	blockCloseAction         = "block.close"
	sessionBlockAssignAction = "session.block.assign"
)

type SetGoalInput struct {
	GoalID     int64   `json:"goal_id,omitempty" jsonschema:"ID of the goal to edit. Omit it to create a goal."`
	Text       *string `json:"text,omitempty" jsonschema:"The goal in the person's own words, up to 500 characters, e.g. 'press banca 100 kg' or 'volver a correr sin dolor'. Required to create a goal."`
	TargetDate *string `json:"target_date,omitempty" jsonschema:"Optional date to reach it by, YYYY-MM-DD. An empty string clears it."`
	Status     *string `json:"status,omitempty" jsonschema:"active (default for a new goal), achieved or dropped."`
}
type GoalOut struct {
	Goal training.Goal `json:"goal"`
	ProposalRef
}

type MicrocycleEdit struct {
	Position        int      `json:"position" jsonschema:"Position of the microcycle in the block, from 1. Only a remaining microcycle (current or upcoming) can be edited."`
	Label           *string  `json:"label,omitempty" jsonschema:"Free label, up to 80 characters, e.g. 'introducción', 'carga' or 'descarga'. An empty string clears it."`
	SetsDelta       *int     `json:"sets_delta,omitempty" jsonschema:"Sets to add (positive) or remove (negative) per exercise in this microcycle, -20 to 20; 0 clears it. A session started from a routine in this microcycle gets it on its copy, kept within 1-20 sets."`
	EffortDelta     *float64 `json:"effort_delta,omitempty" jsonschema:"RPE points to add or remove in this microcycle, -5 to 5; 0 clears it. A session started from a routine in this microcycle gets it on its copy, only where a target RPE is set, kept within 1-10."`
	Rotation        *[]int64 `json:"rotation,omitempty" jsonschema:"Routines (plan IDs) of this microcycle, in order, replacing the block's rotation for it only. An empty list plans no sessions in it."`
	InheritRotation bool     `json:"inherit_rotation,omitempty" jsonschema:"true drops this microcycle's own routines so it follows the block's rotation again."`
}

type SaveBlockInput struct {
	BlockID         int64            `json:"block_id,omitempty" jsonschema:"ID of the block to edit. Omit it to create a block, which needs only intent and microcycle_count."`
	GoalID          *int64           `json:"goal_id,omitempty" jsonschema:"Goal the block serves; 0 unlinks it."`
	Intent          *string          `json:"intent,omitempty" jsonschema:"What the block is for, in free text, up to 500 characters, e.g. 'acumular volumen en pierna'."`
	Advance         *string          `json:"advance,omitempty" jsonschema:"How the block moves from one microcycle to the next: sequence (default) when the microcycle's sessions are done, whatever the dates, or calendar by dates from the block's start. It can change mid-block: to calendar, the first pending microcycle starts today; to sequence, the pending microcycles whose dates passed are marked done."`
	MicrocycleDays  *int             `json:"microcycle_days,omitempty" jsonschema:"Days per microcycle, 1 to 28; defaults to 7. Only dates in calendar mode depend on it."`
	MicrocycleCount *int             `json:"microcycle_count,omitempty" jsonschema:"Number of microcycles, 1 to 52. Growing adds pending microcycles at the end; shrinking drops only remaining microcycles that hold no session, and waits for the person to accept it."`
	Status          *string          `json:"status,omitempty" jsonschema:"sketch (default for a new block: rough, to detail later) or planned. Use advance_block to activate or close a block."`
	Rotation        *[]int64         `json:"rotation,omitempty" jsonschema:"The block's routines (plan IDs) in the order they rotate within each microcycle, up to 14. Done and elapsed microcycles keep the rotation they ran."`
	Microcycles     []MicrocycleEdit `json:"microcycles,omitempty" jsonschema:"Edits to remaining microcycles, by position. Done, skipped and elapsed microcycles are never edited."`
	shortens        bool
}

// resolve fixes the action type before the call becomes a proposal: shrinking
// a block is a deleting change.
func (in *SaveBlockInput) resolve(ctx context.Context, svc *training.Service) error {
	shortens, err := svc.ShrinksBlock(ctx, blockInput(*in))
	in.shortens = shortens
	return err
}

func blockInput(in SaveBlockInput) training.BlockInput {
	out := training.BlockInput{ID: in.BlockID, GoalID: in.GoalID, Intent: in.Intent, Advance: in.Advance, MicrocycleDays: in.MicrocycleDays,
		MicrocycleCount: in.MicrocycleCount, Status: in.Status, Rotation: in.Rotation}
	for _, m := range in.Microcycles {
		out.Microcycles = append(out.Microcycles, training.MicrocycleInput{Position: m.Position, Label: m.Label, SetsDelta: m.SetsDelta,
			EffortDelta: m.EffortDelta, Rotation: m.Rotation, InheritRotation: m.InheritRotation})
	}
	return out
}

type BlockOut struct {
	Block training.BlockView `json:"block"`
	ProposalRef
}

type AdvanceBlockInput struct {
	BlockID   int64  `json:"block_id" jsonschema:"ID of the block."`
	Action    string `json:"action" jsonschema:"activate: start a sketch or planned block (only one block is active at a time). complete_microcycle or skip_microcycle: close the current microcycle of a sequence block as done or skipped, so the next session counts in the next one. close: end the block; it never closes by itself."`
	StartDate string `json:"start_date,omitempty" jsonschema:"First day of the block when activating it, YYYY-MM-DD; defaults to today."`
}

type AssignSessionBlockInput struct {
	SessionID    int64 `json:"session_id" jsonschema:"ID of the session."`
	MicrocycleID int64 `json:"microcycle_id,omitempty" jsonschema:"Microcycle the session counts in, from get_block. Give this or off_plan."`
	OffPlan      bool  `json:"off_plan,omitempty" jsonschema:"true keeps the session outside every microcycle (off-plan). Give this or microcycle_id."`
}
type SessionLinkOut struct {
	Link training.SessionLink `json:"link"`
	ProposalRef
}

type BlockIDInput struct {
	BlockID int64 `json:"block_id" jsonschema:"ID of the block."`
}
type GetBlockOut struct {
	Block training.BlockView `json:"block"`
}

func advanceActionType(in AdvanceBlockInput) string {
	switch in.Action {
	case training.BlockActivate:
		return blockActivateAction
	case training.BlockCompleteCurrent:
		return blockCompleteAction
	case training.BlockSkipCurrent:
		return blockSkipAction
	case training.BlockClose:
		return blockCloseAction
	}
	return "block.advance"
}

func setRotationSchema(schema *jsonschema.Schema) {
	setNonNullableType(schema, "array")
	schema.MaxItems = jsonschema.Ptr(training.MaxRotation)
	schema.Items.Minimum = jsonschema.Ptr(1.0)
}

func registerPeriodizationTools(s *mcp.Server, w *writes, service training.ServiceFor) {
	goalSchema := mustInputSchema[SetGoalInput]()
	setPositiveID(goalSchema.Properties["goal_id"])
	setNonNullableType(goalSchema.Properties["text"], "string")
	goalSchema.Properties["text"].Pattern = `.*\S.*`
	goalSchema.Properties["text"].MaxLength = jsonschema.Ptr(500)
	setNonNullableType(goalSchema.Properties["target_date"], "string")
	goalSchema.Properties["target_date"].Pattern = `^(\d{4}-\d{2}-\d{2})?$`
	setNonNullableType(goalSchema.Properties["status"], "string")
	goalSchema.Properties["status"].Enum = enumOf([]string{training.GoalActive, training.GoalAchieved, training.GoalDropped})
	addWriteBy(w, &mcp.Tool{Name: "set_goal", Description: "Create or edit a goal: what the person trains toward, in their own words, with an optional target date. Omitted fields stay as they are. Goals carry no rules; blocks may point at one.", InputSchema: goalSchema},
		func(in SetGoalInput) string {
			if in.GoalID == 0 {
				return goalCreateAction
			}
			return goalUpdateAction
		},
		func(ctx context.Context, svc *training.Service, in SetGoalInput) (GoalOut, error) {
			g, err := svc.SetGoal(ctx, training.GoalInput{ID: in.GoalID, Text: in.Text, TargetDate: in.TargetDate, Status: in.Status})
			return GoalOut{Goal: g}, err
		})

	blockSchema := mustInputSchema[SaveBlockInput]()
	setPositiveID(blockSchema.Properties["block_id"])
	setNonNullableType(blockSchema.Properties["goal_id"], "integer")
	blockSchema.Properties["goal_id"].Minimum = jsonschema.Ptr(0.0)
	setNonNullableType(blockSchema.Properties["intent"], "string")
	blockSchema.Properties["intent"].Pattern = `.*\S.*`
	blockSchema.Properties["intent"].MaxLength = jsonschema.Ptr(500)
	setNonNullableType(blockSchema.Properties["advance"], "string")
	blockSchema.Properties["advance"].Enum = enumOf([]string{training.AdvanceSequence, training.AdvanceCalendar})
	setNonNullableType(blockSchema.Properties["microcycle_days"], "integer")
	blockSchema.Properties["microcycle_days"].Minimum = jsonschema.Ptr(1.0)
	blockSchema.Properties["microcycle_days"].Maximum = jsonschema.Ptr(float64(training.MaxMicrocycleDays))
	setNonNullableType(blockSchema.Properties["microcycle_count"], "integer")
	blockSchema.Properties["microcycle_count"].Minimum = jsonschema.Ptr(1.0)
	blockSchema.Properties["microcycle_count"].Maximum = jsonschema.Ptr(float64(training.MaxMicrocycles))
	setNonNullableType(blockSchema.Properties["status"], "string")
	blockSchema.Properties["status"].Enum = enumOf([]string{training.BlockSketch, training.BlockPlanned})
	setRotationSchema(blockSchema.Properties["rotation"])
	setNonNullableType(blockSchema.Properties["microcycles"], "array")
	blockSchema.Properties["microcycles"].MaxItems = jsonschema.Ptr(training.MaxMicrocycles)
	edit := blockSchema.Properties["microcycles"].Items
	setPositiveID(edit.Properties["position"])
	edit.Properties["position"].Maximum = jsonschema.Ptr(float64(training.MaxMicrocycles))
	setNonNullableType(edit.Properties["label"], "string")
	edit.Properties["label"].MaxLength = jsonschema.Ptr(80)
	setNonNullableType(edit.Properties["sets_delta"], "integer")
	edit.Properties["sets_delta"].Minimum = jsonschema.Ptr(float64(-training.MaxSetsDelta))
	edit.Properties["sets_delta"].Maximum = jsonschema.Ptr(float64(training.MaxSetsDelta))
	setNonNullableType(edit.Properties["effort_delta"], "number")
	edit.Properties["effort_delta"].Minimum = jsonschema.Ptr(-training.MaxEffortDelta)
	edit.Properties["effort_delta"].Maximum = jsonschema.Ptr(training.MaxEffortDelta)
	setRotationSchema(edit.Properties["rotation"])
	addWriteBy(w, &mcp.Tool{Name: "save_block", Description: "Create or edit a training block: a run of microcycles (e.g. weeks) with an intent, an optional goal and a rotation of routines (plans). Nothing is imposed: a sketch needs only intent and microcycle_count, and every other detail can be added or changed later. Edits are forward only: they change the remaining microcycles, never what was done. A microcycle may have a free label and optional sets_delta and effort_delta, which a session started from a routine in that microcycle applies to its own copy of the routine (never to the routine or to past sessions). Shrinking a block waits for the person to accept it. Activate a block with advance_block.", InputSchema: blockSchema},
		func(in SaveBlockInput) string {
			switch {
			case in.BlockID == 0:
				return blockCreateAction
			case in.shortens:
				return blockShortenAction
			}
			return blockUpdateAction
		},
		func(ctx context.Context, svc *training.Service, in SaveBlockInput) (BlockOut, error) {
			b, err := svc.SaveBlock(ctx, blockInput(in))
			return BlockOut{Block: b}, err
		})

	advanceSchema := mustInputSchema[AdvanceBlockInput]()
	setPositiveID(advanceSchema.Properties["block_id"])
	advanceSchema.Properties["action"].Enum = enumOf([]string{training.BlockActivate, training.BlockCompleteCurrent, training.BlockSkipCurrent, training.BlockClose})
	advanceSchema.Properties["start_date"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	addWriteBy(w, &mcp.Tool{Name: "advance_block", Description: "Move a block by hand: activate it, complete or skip the current microcycle of a sequence block, or close it. While a block is active, every session started or logged is placed in it on its own (sequence: the next microcycle with routines left; calendar: the microcycle whose dates cover it), or kept off-plan when it fits nowhere; nothing is ever rejected.", InputSchema: advanceSchema},
		advanceActionType,
		func(ctx context.Context, svc *training.Service, in AdvanceBlockInput) (BlockOut, error) {
			b, err := svc.AdvanceBlock(ctx, training.AdvanceInput{BlockID: in.BlockID, Action: in.Action, StartDate: in.StartDate})
			return BlockOut{Block: b}, err
		})

	assignSchema := mustInputSchema[AssignSessionBlockInput]()
	setPositiveID(assignSchema.Properties["session_id"])
	setPositiveID(assignSchema.Properties["microcycle_id"])
	addWrite(w, &mcp.Tool{Name: "assign_session_block", Description: "Change where a session counts: in a microcycle of any block, or off-plan. Sessions are placed on their own while a block is active; use this to correct a placement. It never closes or reopens a microcycle.", InputSchema: assignSchema}, sessionBlockAssignAction,
		func(ctx context.Context, svc *training.Service, in AssignSessionBlockInput) (SessionLinkOut, error) {
			l, err := svc.AssignSessionBlock(ctx, training.AssignInput{SessionID: in.SessionID, MicrocycleID: in.MicrocycleID, OffPlan: in.OffPlan})
			return SessionLinkOut{Link: l}, err
		})

	addTool(s, &mcp.Tool{Name: "training_status", Description: "Where the person stands in their planning today: active goals, the active block and its current microcycle, with the routines done and still due and its modifiers (label, sets_delta, effort_delta), and next_prescription: the first routine still due with those modifiers applied, base and result per exercise. start_session applies them to the session's copy. Without an active block, block and current_microcycle are absent and training works as usual, routine by routine."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, training.TrainingStatus, error) {
		v, err := service(ctx).TrainingStatus(ctx)
		return nil, v, toolError(err)
	})
	addTool(s, &mcp.Tool{Name: "list_blocks", Description: "List every goal and every block (sketch, planned, active or done) with its progress: microcycles done, skipped and remaining, the current one, and sessions kept off-plan."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, training.BlockList, error) {
		v, err := service(ctx).ListBlocks(ctx)
		return nil, v, toolError(err)
	})
	getSchema := mustInputSchema[BlockIDInput]()
	setPositiveID(getSchema.Properties["block_id"])
	addTool(s, &mcp.Tool{Name: "get_block", Description: "Get one block in full: its rotation, every microcycle with its state (done, skipped, current, upcoming or elapsed), dates in calendar mode, routines done and due, the sessions placed in it, and the sessions kept off-plan.", InputSchema: getSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in BlockIDInput) (*mcp.CallToolResult, GetBlockOut, error) {
		v, err := service(ctx).GetBlock(ctx, in.BlockID)
		return nil, GetBlockOut{Block: v}, toolError(err)
	})
}
