package mcpsrv

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fullfran/training-mcp/internal/training"
)

// LbPreviewInput picks the sets to preview for a correction from lb.
type LbPreviewInput struct {
	SessionID int64   `json:"session_id,omitempty" jsonschema:"One session: every set in it."`
	Exercise  string  `json:"exercise,omitempty" jsonschema:"One exercise between from and to (both required with it)."`
	From      string  `json:"from,omitempty" jsonschema:"First date, YYYY-MM-DD, inclusive."`
	To        string  `json:"to,omitempty" jsonschema:"Last date, YYYY-MM-DD, inclusive."`
	SetIDs    []int64 `json:"set_ids,omitempty" jsonschema:"Sets by id, at most 500."`
}

type LbPreviewOut struct {
	Preview training.LbPreview `json:"preview"`
}

// LbCorrectInput lists the sets to correct from lb.
type LbCorrectInput struct {
	SetIDs []int64 `json:"set_ids" jsonschema:"The sets to correct, at most 500; usually the ones preview_lb_correction listed as not yet corrected."`
}

type LbCorrectOut struct {
	Correction training.LbCorrection `json:"correction"`
	ProposalRef
}

// addLbTools are the correction of past sets typed in lb but stored as kg: a
// read that previews it and a proposal-gated write that applies it.
func addLbTools(s *mcp.Server, w *writes, service training.ServiceFor) {
	previewSchema := mustInputSchema[LbPreviewInput]()
	previewSchema.Properties["session_id"].Minimum = jsonschema.Ptr(1.0)
	previewSchema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	previewSchema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	previewSchema.Properties["set_ids"].MaxItems = jsonschema.Ptr(training.MaxLbCorrection)
	addTool(s, &mcp.Tool{Name: "preview_lb_correction", Description: "Preview correcting past sets whose weight was typed in lb but stored as kg (135 lb logged as 135 kg). Give exactly one scope: session_id, exercise with from and to, or set_ids. Lists every set with old_kg (now) and new_kg (the number read as lb), marks sets already corrected, and counts what would change. Changes nothing; correct_lb_sets applies it.", InputSchema: previewSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in LbPreviewInput) (*mcp.CallToolResult, LbPreviewOut, error) {
		v, err := service(ctx).LbCorrectionPreview(ctx, training.LbScope{SessionID: in.SessionID, Exercise: in.Exercise, From: in.From, To: in.To, SetIDs: in.SetIDs})
		return nil, LbPreviewOut{Preview: v}, toolError(err)
	})
	correctSchema := mustInputSchema[LbCorrectInput]()
	correctSchema.Properties["set_ids"].MinItems = jsonschema.Ptr(1)
	correctSchema.Properties["set_ids"].MaxItems = jsonschema.Ptr(training.MaxLbCorrection)
	addWrite(w, &mcp.Tool{Name: training.LbCorrectionTool, Description: "Correct past sets whose weight was typed in lb but stored as kg: each set's stored number is read as lb and stored in kg, the same as typing it in lb. All or nothing: a set that is not the person's, or one already corrected, refuses the whole correction, so it never applies twice. Only the weight changes; the person can undo it in the app. Preview first with preview_lb_correction and confirm with the person.", InputSchema: correctSchema}, training.LbCorrectionAction, func(ctx context.Context, svc *training.Service, in LbCorrectInput) (LbCorrectOut, error) {
		v, err := svc.CorrectLbSets(ctx, in.SetIDs)
		return LbCorrectOut{Correction: v}, err
	})
}
