package mcpsrv

import (
	"context"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// review_microcycle (#41, slice 3): a microcycle planned against done, as
// numbers. It reads only and gives no verdict.

type ReviewMicrocycleInput struct {
	BlockID      int64 `json:"block_id,omitempty" jsonschema:"Block to review, from list_blocks. Omitted: the active block, else the most recently closed one."`
	MicrocycleID int64 `json:"microcycle_id,omitempty" jsonschema:"Microcycle to review, from get_block. Omitted: the last closed microcycle of the block, else the current one, else the first."`
}

type ReviewMicrocycleOut struct {
	Review training.MicrocycleReview `json:"review"`
}

const reviewMicrocycleDescription = "Review one microcycle: what was planned next to what was done, as plain counts. Rotation routines done, skipped and pending; planned sets (each session's own copy, with the modifiers it got) against working sets done, per exercise and per muscle group, in the single-group count and in the fractional count when the exercise library knows the muscles (method named, uncertain muscles kept apart); target RPE against logged RPE, where imputed RPE is counted but never compared; the drop-off read of each exercise with its not-comparable reasons; off-plan sessions and extra work listed apart; session feedback and the bodyweight change over the microcycle's days; and the previous microcycle of the block for comparison. It reads only. It gives no verdict, no score and no recommendation: what to change is the person's call. Totals come first; long lists stop at a fixed length with their full count beside them."

func registerPeriodizationReviewTool(s *mcp.Server, service training.ServiceFor) {
	schema := mustInputSchema[ReviewMicrocycleInput]()
	setPositiveID(schema.Properties["block_id"])
	setPositiveID(schema.Properties["microcycle_id"])
	addTool(s, &mcp.Tool{Name: "review_microcycle", Description: reviewMicrocycleDescription, InputSchema: schema}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReviewMicrocycleInput) (*mcp.CallToolResult, ReviewMicrocycleOut, error) {
		v, err := service(ctx).ReviewMicrocycle(ctx, training.ReviewInput{BlockID: in.BlockID, MicrocycleID: in.MicrocycleID})
		return nil, ReviewMicrocycleOut{Review: v}, toolError(err)
	})
}
