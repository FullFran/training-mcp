package mcpsrv

import (
	"context"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Saving and forgetting are separate action types, so letting the coach
// remember without asking never lets it delete memory without asking.
const (
	memorySaveAction   = "memory.save"
	memoryForgetAction = "memory.forget"
)

// No memory input has a user id: whose memory it is comes only from the
// authenticated request, through the service the tool is given.
type MemorySaveInput struct {
	TopicKey string `json:"topic_key,omitempty" jsonschema:"Stable key naming this fact, e.g. 'injury/right-shoulder' or 'goal/squat'. Saving the same key again updates the fact instead of adding a duplicate. Omit it to derive one from the type and title."`
	Type     string `json:"type,omitempty" jsonschema:"Kind of fact: preference, limitation (injury, pain, restriction), goal, equipment, worked (what worked well) or note. Defaults to note."`
	Title    string `json:"title" jsonschema:"Short title, up to 120 characters."`
	Body     string `json:"body" jsonschema:"The fact itself in a few sentences, up to 4000 characters. Write it so it still makes sense months later without the chat."`
	Pinned   *bool  `json:"pinned,omitempty" jsonschema:"Pinned facts always come first in memory_context. Omit to keep the current value."`
}
type MemoryWriteOut struct {
	Memory training.Memory `json:"memory"`
	ProposalRef
}
type MemoryIDInput struct {
	MemoryID int64 `json:"memory_id" jsonschema:"Positive ID of the memory."`
}
type MemoryOut struct {
	Memory training.Memory `json:"memory"`
}
type MemorySearchInput struct {
	Query string `json:"query,omitempty" jsonschema:"Words to look for in titles, bodies and topic keys; every word must match, as a prefix, ignoring accents. Omit to list facts, pinned first and then the most recently updated."`
	Type  string `json:"type,omitempty" jsonschema:"Only facts of this type."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum facts to return; defaults to 10 and accepts 1 through 50."`
}
type MemoriesOut struct {
	Memories []training.Memory `json:"memories"`
}

func memoryTypeEnum() []any {
	out := make([]any, 0, len(training.MemoryTypes))
	for _, t := range training.MemoryTypes {
		out = append(out, t)
	}
	return out
}

func addMemoryTools(s *mcp.Server, w *writes, service training.ServiceFor) {
	saveSchema := mustInputSchema[MemorySaveInput]()
	saveSchema.Properties["title"].Pattern = `.*\S.*`
	saveSchema.Properties["title"].MaxLength = jsonschema.Ptr(120)
	saveSchema.Properties["body"].Pattern = `.*\S.*`
	saveSchema.Properties["body"].MaxLength = jsonschema.Ptr(4000)
	saveSchema.Properties["topic_key"].MaxLength = jsonschema.Ptr(120)
	saveSchema.Properties["type"].Enum = memoryTypeEnum()
	setNonNullableType(saveSchema.Properties["pinned"], "boolean")
	addWrite(w, &mcp.Tool{Name: "memory_save", Description: "Remember a durable fact about the person for future chats: a preference, an injury or limitation, a goal, their equipment, or what worked. Chats are deleted after 90 days; memory is kept until the person deletes it. Save only what will still matter later, never the chat itself.", InputSchema: saveSchema}, memorySaveAction, func(ctx context.Context, svc *training.Service, in MemorySaveInput) (MemoryWriteOut, error) {
		m, err := svc.SaveMemory(ctx, training.MemoryInput{TopicKey: in.TopicKey, Type: in.Type, Title: in.Title, Body: in.Body, Pinned: in.Pinned})
		return MemoryWriteOut{Memory: m}, err
	})
	forgetSchema := mustInputSchema[MemoryIDInput]()
	setPositiveID(forgetSchema.Properties["memory_id"])
	addWrite(w, &mcp.Tool{Name: "memory_forget", Description: "Delete a remembered fact for good, e.g. when it is wrong or no longer true. To correct a fact, prefer memory_save with its topic_key.", InputSchema: forgetSchema}, memoryForgetAction, func(ctx context.Context, svc *training.Service, in MemoryIDInput) (MemoryWriteOut, error) {
		m, err := svc.DeleteMemory(ctx, in.MemoryID)
		return MemoryWriteOut{Memory: m}, err
	})
	searchSchema := mustInputSchema[MemorySearchInput]()
	searchSchema.Properties["type"].Enum = memoryTypeEnum()
	searchSchema.Properties["limit"].Minimum = jsonschema.Ptr(1.0)
	searchSchema.Properties["limit"].Maximum = jsonschema.Ptr(50.0)
	addTool(s, &mcp.Tool{Name: "memory_search", Description: "Search what you remember about the person by words, or list it. Bodies are returned whole.", InputSchema: searchSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemorySearchInput) (*mcp.CallToolResult, MemoriesOut, error) {
		v, err := service(ctx).SearchMemory(ctx, training.MemoryQuery{Text: in.Query, Type: in.Type, Limit: in.Limit})
		return nil, MemoriesOut{Memories: v}, toolError(err)
	})
	getSchema := mustInputSchema[MemoryIDInput]()
	setPositiveID(getSchema.Properties["memory_id"])
	addTool(s, &mcp.Tool{Name: "memory_get", Description: "Get one remembered fact with its whole body.", InputSchema: getSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryIDInput) (*mcp.CallToolResult, MemoryOut, error) {
		v, err := service(ctx).GetMemory(ctx, in.MemoryID)
		return nil, MemoryOut{Memory: v}, toolError(err)
	})
	addTool(s, &mcp.Tool{Name: "memory_context", Description: "What you remember about the person, compact: pinned facts first, then the most recently updated, up to 20, long bodies cut short (use memory_get for the whole fact). Read it at the start of a conversation."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, MemoriesOut, error) {
		v, err := service(ctx).MemoryContext(ctx)
		return nil, MemoriesOut{Memories: v}, toolError(err)
	})
}
