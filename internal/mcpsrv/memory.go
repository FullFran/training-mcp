package mcpsrv

import (
	"context"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every memory write is its own action type, so letting the coach remember
// without asking never lets it forget, retire or merge facts without asking.
const (
	memorySaveAction        = training.MemorySaveAction
	memoryForgetAction      = "memory.forget"
	memoryJudgeAction       = "memory.judge"
	memoryConfirmAction     = "memory.confirm"
	memoryConsolidateAction = "memory.consolidate"
)

// No memory input has a user id: whose memory it is comes only from the
// authenticated request, through the service the tool is given.
type MemorySaveInput struct {
	TopicKey string `json:"topic_key,omitempty" jsonschema:"Stable key naming this fact, e.g. 'injury/right-shoulder' or 'goal/squat'. Saving the same key again updates the fact instead of adding a duplicate, keeping the previous text as a revision. Omit it to derive one from the type and title."`
	Type     string `json:"type,omitempty" jsonschema:"Kind of fact: preference, limitation (injury, pain, restriction), goal, equipment, worked (what worked well) or note. Defaults to note. It sets how soon the fact is due a check with the person: limitation 14 days, goal 30, worked 60, note 90, equipment and preference 180."`
	Title    string `json:"title" jsonschema:"Short title, up to 120 characters."`
	Body     string `json:"body" jsonschema:"The fact itself in a few sentences, up to 4000 characters. Write it so it still makes sense months later without the chat."`
	Pinned   *bool  `json:"pinned,omitempty" jsonschema:"Pinned facts always come first in memory_context. Omit to keep the current value."`
}
type MemorySaveOut struct {
	Memory training.Memory `json:"memory"`
	// Candidates are similar facts to judge with memory_judge, one call each.
	Candidates       []training.MemoryCandidate `json:"candidates"`
	JudgmentRequired bool                       `json:"judgment_required"`
	ProposalRef
}
type MemoryWriteOut struct {
	Memory training.Memory `json:"memory"`
	ProposalRef
}
type MemoryIDInput struct {
	MemoryID int64 `json:"memory_id" jsonschema:"Positive ID of the memory."`
}
type MemoryOut struct {
	Memory    training.Memory           `json:"memory"`
	Revisions []training.MemoryRevision `json:"revisions"`
}
type MemorySearchInput struct {
	Query string `json:"query,omitempty" jsonschema:"Words to look for in titles, bodies and topic keys; every word must match, as a prefix, ignoring accents. Omit to list facts, pinned first and then the most recently updated."`
	Type  string `json:"type,omitempty" jsonschema:"Only facts of this type."`
	View  string `json:"view,omitempty" jsonschema:"Which facts: active (default; neither forgotten nor superseded), due (active and due a check with the person) or superseded (replaced by a newer fact, kept as history)."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum facts to return; defaults to 10 and accepts 1 through 50."`
}
type MemoriesOut struct {
	Memories []training.Memory `json:"memories"`
}
type MemoryJudgeInput struct {
	RelationID int64  `json:"relation_id" jsonschema:"relation_id of a candidate returned by memory_save."`
	Relation   string `json:"relation" jsonschema:"How the saved fact relates to the candidate: supersedes (it replaces the candidate, which leaves context and search), conflicts_with (both cannot be true; the person resolves it in the app), related, compatible, scoped (both true in different situations) or not_conflict."`
	Reason     string `json:"reason,omitempty" jsonschema:"One sentence on why, up to 500 characters."`
}
type MemoryJudgeOut struct {
	Relation training.MemoryRelation `json:"relation"`
	ProposalRef
}
type MemoryConsolidateInput struct {
	MemoryIDs   []int64 `json:"memory_ids" jsonschema:"The 2 to 10 active facts to merge into one."`
	TopicKey    string  `json:"topic_key,omitempty" jsonschema:"Key of the merged fact. It may be one of the merged facts' keys; omit it to derive one from the type and title."`
	Type        string  `json:"type,omitempty" jsonschema:"Type of the merged fact; defaults to the first fact's type."`
	Title       string  `json:"title" jsonschema:"Title of the merged fact."`
	Body        string  `json:"body" jsonschema:"The merged fact, written by you from the facts it replaces."`
	Fingerprint string  `json:"fingerprint,omitempty" jsonschema:"The fingerprint memory_consolidate_preview returned for exactly these arguments."`
}
type MemoryConsolidateOut struct {
	Memory training.Memory `json:"memory"`
	ProposalRef
}
type MemoryPlanOut struct {
	training.ConsolidationPlan
}

func enumOf(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}

func consolidationInput(in MemoryConsolidateInput) training.ConsolidationInput {
	return training.ConsolidationInput{MemoryIDs: in.MemoryIDs, TopicKey: in.TopicKey, Type: in.Type, Title: in.Title, Body: in.Body, Fingerprint: in.Fingerprint}
}

func consolidateSchema(withFingerprint bool) *jsonschema.Schema {
	schema := mustInputSchema[MemoryConsolidateInput]()
	schema.Properties["memory_ids"].MinItems = jsonschema.Ptr(2)
	schema.Properties["memory_ids"].MaxItems = jsonschema.Ptr(10)
	schema.Properties["memory_ids"].Items.Minimum = jsonschema.Ptr(1.0)
	schema.Properties["title"].Pattern = `.*\S.*`
	schema.Properties["title"].MaxLength = jsonschema.Ptr(120)
	schema.Properties["body"].Pattern = `.*\S.*`
	schema.Properties["body"].MaxLength = jsonschema.Ptr(4000)
	schema.Properties["topic_key"].MaxLength = jsonschema.Ptr(120)
	schema.Properties["type"].Enum = enumOf(training.MemoryTypes)
	if withFingerprint {
		schema.Required = append(schema.Required, "fingerprint")
		schema.Properties["fingerprint"].Pattern = `.*\S.*`
	} else {
		delete(schema.Properties, "fingerprint")
	}
	return schema
}

func addMemoryTools(s *mcp.Server, w *writes, service training.ServiceFor) {
	saveSchema := mustInputSchema[MemorySaveInput]()
	saveSchema.Properties["title"].Pattern = `.*\S.*`
	saveSchema.Properties["title"].MaxLength = jsonschema.Ptr(120)
	saveSchema.Properties["body"].Pattern = `.*\S.*`
	saveSchema.Properties["body"].MaxLength = jsonschema.Ptr(4000)
	saveSchema.Properties["topic_key"].MaxLength = jsonschema.Ptr(120)
	saveSchema.Properties["type"].Enum = enumOf(training.MemoryTypes)
	setNonNullableType(saveSchema.Properties["pinned"], "boolean")
	addWrite(w, &mcp.Tool{Name: "memory_save", Description: "Remember a durable fact about the person for future chats: a preference, an injury or limitation, a goal, their equipment, or what worked. Chats are deleted after 90 days; memory is kept until the person deletes it. Save only what will still matter later, never the chat itself. The result lists similar facts already remembered as candidates; when judgment_required is true, call memory_judge once per candidate.", InputSchema: saveSchema}, memorySaveAction, func(ctx context.Context, svc *training.Service, in MemorySaveInput) (MemorySaveOut, error) {
		v, err := svc.SaveMemory(ctx, training.MemoryInput{TopicKey: in.TopicKey, Type: in.Type, Title: in.Title, Body: in.Body, Pinned: in.Pinned})
		return MemorySaveOut{Memory: v.Memory, Candidates: v.Candidates, JudgmentRequired: len(v.Candidates) > 0}, err
	})
	forgetSchema := mustInputSchema[MemoryIDInput]()
	setPositiveID(forgetSchema.Properties["memory_id"])
	addWrite(w, &mcp.Tool{Name: "memory_forget", Description: "Forget a remembered fact, e.g. when it is wrong or no longer true. It leaves context and search but is kept for the person, who alone can delete it for good. To correct a fact, prefer memory_save with its topic_key; when a newer fact replaces it, prefer memory_judge with supersedes.", InputSchema: forgetSchema}, memoryForgetAction, func(ctx context.Context, svc *training.Service, in MemoryIDInput) (MemoryWriteOut, error) {
		m, err := svc.ForgetMemory(ctx, in.MemoryID)
		return MemoryWriteOut{Memory: m}, err
	})
	judgeSchema := mustInputSchema[MemoryJudgeInput]()
	setPositiveID(judgeSchema.Properties["relation_id"])
	judgeSchema.Properties["relation"].Enum = enumOf(training.MemoryRelations)
	judgeSchema.Properties["reason"].MaxLength = jsonschema.Ptr(500)
	addWrite(w, &mcp.Tool{Name: "memory_judge", Description: "Record how a fact just saved relates to one of its candidates. Ask the person first when unsure, or before supersedes or conflicts_with on a limitation or goal. Each relation is judged once.", InputSchema: judgeSchema}, memoryJudgeAction, func(ctx context.Context, svc *training.Service, in MemoryJudgeInput) (MemoryJudgeOut, error) {
		r, err := svc.JudgeMemory(ctx, in.RelationID, in.Relation, in.Reason)
		return MemoryJudgeOut{Relation: r}, err
	})
	confirmSchema := mustInputSchema[MemoryIDInput]()
	setPositiveID(confirmSchema.Properties["memory_id"])
	addWrite(w, &mcp.Tool{Name: "memory_confirm", Description: "Record that the person confirmed a fact still holds, e.g. the shoulder still hurts, which restarts its review cycle. If it no longer holds, update it with memory_save or forget it instead.", InputSchema: confirmSchema}, memoryConfirmAction, func(ctx context.Context, svc *training.Service, in MemoryIDInput) (MemoryWriteOut, error) {
		m, err := svc.ConfirmMemory(ctx, in.MemoryID)
		return MemoryWriteOut{Memory: m}, err
	})
	addWrite(w, &mcp.Tool{Name: "memory_consolidate", Description: "Merge several facts into one, exactly as memory_consolidate_preview showed: pass the same arguments and its fingerprint. The merged facts are forgotten, pointing to the one that replaces them. If any fact changed since the preview, it is refused; preview again.", InputSchema: consolidateSchema(true)}, memoryConsolidateAction, func(ctx context.Context, svc *training.Service, in MemoryConsolidateInput) (MemoryConsolidateOut, error) {
		m, err := svc.ConsolidateMemory(ctx, consolidationInput(in))
		return MemoryConsolidateOut{Memory: m}, err
	})

	addTool(s, &mcp.Tool{Name: "memory_consolidate_preview", Description: "Show what merging several facts into one would do, without changing anything: the facts merged, the fact that would replace them, and a fingerprint to pass to memory_consolidate.", InputSchema: consolidateSchema(false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryConsolidateInput) (*mcp.CallToolResult, MemoryPlanOut, error) {
		in.Fingerprint = ""
		v, err := service(ctx).PreviewConsolidation(ctx, consolidationInput(in))
		return nil, MemoryPlanOut{v}, toolError(err)
	})
	searchSchema := mustInputSchema[MemorySearchInput]()
	searchSchema.Properties["type"].Enum = enumOf(training.MemoryTypes)
	searchSchema.Properties["view"].Enum = enumOf([]string{training.MemoryViewActive, training.MemoryViewDue, training.MemoryViewSuperseded})
	searchSchema.Properties["limit"].Minimum = jsonschema.Ptr(1.0)
	searchSchema.Properties["limit"].Maximum = jsonschema.Ptr(50.0)
	addTool(s, &mcp.Tool{Name: "memory_search", Description: "Search what you remember about the person by words, or list it. Forgotten facts are never returned. Bodies are returned whole.", InputSchema: searchSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemorySearchInput) (*mcp.CallToolResult, MemoriesOut, error) {
		v, err := service(ctx).SearchMemory(ctx, training.MemoryQuery{Text: in.Query, Type: in.Type, View: in.View, Limit: in.Limit})
		return nil, MemoriesOut{Memories: v}, toolError(err)
	})
	getSchema := mustInputSchema[MemoryIDInput]()
	setPositiveID(getSchema.Properties["memory_id"])
	addTool(s, &mcp.Tool{Name: "memory_get", Description: "Get one remembered fact with its whole body and its earlier versions.", InputSchema: getSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryIDInput) (*mcp.CallToolResult, MemoryOut, error) {
		svc := service(ctx)
		m, err := svc.GetMemory(ctx, in.MemoryID)
		if err != nil {
			return nil, MemoryOut{}, toolError(err)
		}
		revisions, err := svc.MemoryHistory(ctx, in.MemoryID)
		return nil, MemoryOut{Memory: m, Revisions: revisions}, toolError(err)
	})
	addTool(s, &mcp.Tool{Name: "memory_context", Description: "What you remember about the person, compact: up to 20 active facts, pinned first then the most recently updated, long bodies cut short (use memory_get for the whole fact). due lists facts to check with the person now, e.g. '¿sigue molestando el hombro?': ask, then memory_confirm if it still holds, or update or forget it. conflicts counts conflicting facts the person resolves in the app. Read it at the start of a conversation."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, training.MemoryContext, error) {
		v, err := service(ctx).MemoryContext(ctx)
		return nil, v, toolError(err)
	})
}
