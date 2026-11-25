package training

import "context"

// Periodization (#41). A goal holds blocks; a block is a number of microcycles
// that rotate routines (plans). The engine knows no methodology: it stores what
// the person or the coach decided and does the arithmetic of where a session
// falls. There is no built-in deload, progression or volume rule; a deload is a
// microcycle with a label and modifiers, like any other.
//
// Every field is optional except a block's intent and length, so a block can
// start as a sketch and be detailed when it is reached. Edits are forward
// only: they touch the microcycles not yet done, and done sessions keep the
// link and the prescription they had.

// How a block advances from one microcycle to the next.
const (
	// AdvanceSequence moves on when the microcycle's sessions are done (one per
	// routine of its rotation) or when it is closed or skipped explicitly, so a
	// missed week breaks nothing.
	AdvanceSequence = "sequence"
	// AdvanceCalendar moves on by dates counted from the block's start. A
	// calendar block never closes by itself.
	AdvanceCalendar = "calendar"
)

// Block statuses. Only one block per account is active at a time.
const (
	BlockSketch  = "sketch"
	BlockPlanned = "planned"
	BlockActive  = "active"
	BlockDone    = "done"
)

// Stored microcycle statuses: pending until closed, as done or skipped.
const (
	MicrocyclePending = "pending"
	MicrocycleDone    = "done"
	MicrocycleSkipped = "skipped"
)

// How a microcycle was closed: its last routine was done, by hand, or its
// dates passed when the block went back to sequence.
const (
	ClosedByPlacement  = "placement"
	ClosedByHand       = "hand"
	ClosedByModeChange = "mode_change"
)

// Microcycle states as read on a given day. done and skipped are closed;
// current and upcoming are the remaining microcycles, the only ones an edit
// may change; elapsed is a microcycle whose dates passed in a calendar block
// (or one never reached in a closed block), left as it was.
const (
	StateDone     = "done"
	StateSkipped  = "skipped"
	StateCurrent  = "current"
	StateUpcoming = "upcoming"
	StateElapsed  = "elapsed"
)

// Goal statuses.
const (
	GoalActive   = "active"
	GoalAchieved = "achieved"
	GoalDropped  = "dropped"
)

// advance_block actions.
const (
	BlockActivate         = "activate"
	BlockCompleteCurrent  = "complete_microcycle"
	BlockSkipCurrent      = "skip_microcycle"
	BlockClose            = "close"
	maxGoalTextLen        = 500
	maxBlockIntentLen     = 500
	maxMicrocycleLabelLen = 80
	// MaxMicrocycles bounds a block's length; MaxMicrocycleDays a microcycle's.
	MaxMicrocycles    = 52
	MaxMicrocycleDays = 28
	// MaxRotation bounds the routines one rotation holds.
	MaxRotation = 14
	// MaxSetsDelta and MaxEffortDelta bound a microcycle's modifiers, which a
	// session started in the microcycle applies to its copy of the routine.
	MaxSetsDelta   = 20
	MaxEffortDelta = 5.0
)

// Goal is what a person trains toward, in their own words.
type Goal struct {
	ID         int64  `json:"id"`
	Text       string `json:"text"`
	TargetDate string `json:"target_date,omitempty"`
	Status     string `json:"status"`
}

// GoalInput creates a goal (ID 0) or edits one; nil fields stay as they are.
type GoalInput struct {
	ID         int64
	Text       *string
	TargetDate *string
	Status     *string
}

// RotationEntry is one routine of a rotation. PlanID is 0 once the routine was
// deleted; PlanName then keeps the name it had.
type RotationEntry struct {
	PlanID   int64  `json:"plan_id,omitempty"`
	PlanName string `json:"plan_name"`
}

// Microcycle is one step of a block. Its rotation, when it has its own,
// overrides the block's; an own rotation may be empty (no sessions planned).
type Microcycle struct {
	ID          int64           `json:"id"`
	Position    int             `json:"position"`
	Label       string          `json:"label,omitempty"`
	SetsDelta   int             `json:"sets_delta,omitempty"`
	EffortDelta float64         `json:"effort_delta,omitempty"`
	OwnRotation bool            `json:"own_rotation"`
	Rotation    []RotationEntry `json:"rotation,omitempty"`
	Status      string          `json:"status"`
	ClosedOn    string          `json:"closed_on,omitempty"`
	ClosedBy    string          `json:"closed_by,omitempty"`
}

// Block is the stored aggregate: the block, its rotation and its microcycles in
// position order.
type Block struct {
	ID             int64
	GoalID         int64
	Intent         string
	Advance        string
	MicrocycleDays int
	Status         string
	StartDate      string
	EndedOn        string
	Rotation       []RotationEntry
	Microcycles    []Microcycle
}

// SessionLink is where a session counts in a block: a microcycle, or off-plan
// (outside every microcycle, never rejected). A session never placed (logged
// with no active block, or before periodization existed) has Placed false.
type SessionLink struct {
	SessionID    int64  `json:"session_id"`
	Date         string `json:"date"`
	PlanID       int64  `json:"plan_id,omitempty"`
	PlanName     string `json:"plan_name,omitempty"`
	BlockID      int64  `json:"block_id,omitempty"`
	MicrocycleID int64  `json:"microcycle_id,omitempty"`
	OffPlan      bool   `json:"off_plan"`
	Placed       bool   `json:"-"`
}

// PeriodizationStore keeps one user's goals and blocks and where each session
// counts. It is a port of its own, found on the UserStore by assertion, so a
// store without it (the many test doubles) keeps working exactly as before:
// no block is ever active there. Another user's id behaves as a missing one.
type PeriodizationStore interface {
	// SaveGoal inserts a goal (ID 0) or rewrites one of the user's.
	SaveGoal(context.Context, Goal) (Goal, error)
	GetGoal(context.Context, int64) (Goal, error)
	// Goals lists every goal, oldest first.
	Goals(context.Context) ([]Goal, error)
	// SaveBlock inserts a block (ID 0) or rewrites one of the user's with its
	// rotation and microcycles in one transaction: a microcycle with ID 0 is
	// added, a stored one missing from the list is removed.
	SaveBlock(context.Context, Block) (Block, error)
	GetBlock(context.Context, int64) (Block, error)
	// Blocks lists every block, oldest first.
	Blocks(context.Context) ([]Block, error)
	// ActiveBlock is the user's active block, if any.
	ActiveBlock(context.Context) (Block, bool, error)
	// BlockOfMicrocycle is the block a microcycle of the user belongs to.
	BlockOfMicrocycle(context.Context, int64) (int64, error)
	// SessionLink reads where one of the user's sessions counts.
	SessionLink(context.Context, int64) (SessionLink, error)
	// BlockSessions lists the sessions placed in a block, on plan or off it,
	// by date.
	BlockSessions(context.Context, int64) ([]SessionLink, error)
	// LinkSession stores where a session counts and, when block is not nil,
	// saves that block in the same transaction.
	LinkSession(ctx context.Context, link SessionLink, block *Block) error
}
