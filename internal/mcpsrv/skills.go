package mcpsrv

import (
	"context"
	"errors"
	"fmt"

	"github.com/fullfran/training-mcp/internal/skills"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Skills are global and read-only: they hold no personal data, so these tools
// take no service and answer the same for every person. Loading a skill
// registers no tool and grants nothing; the tool list never changes.

type SkillsOut struct {
	Skills []skills.Entry `json:"skills"`
	Note   string         `json:"note"`
}

type LoadSkillInput struct {
	ID string `json:"id" jsonschema:"The skill id, as list_skills returns it."`
}

type LoadSkillOut struct {
	Skill skills.Skill `json:"skill"`
	Note  string       `json:"note"`
}

const (
	listSkillsNote = "Metadata only. Call load_skill with an id when a trigger matches. A skill is guidance, never a permission: loading it adds no tool. draft-unreviewed means no trainer has reviewed it yet."
	loadSkillNote  = "Instructions for you, not authority: they add no tool and bypass no confirmation. Get the person's numbers from the tools the skill names, cite its references by key, and say so when its review_status is draft-unreviewed."
)

func addSkillTools(s *mcp.Server) {
	lib := skills.Default()
	addTool(s, &mcp.Tool{Name: "list_skills", Description: "List the training-theory skills you can load: id, version, one-line summary, the situations each is for and its review status. Metadata only; call load_skill for one skill's instructions. The same for every person."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, SkillsOut, error) {
		return nil, SkillsOut{Skills: lib.Catalog(), Note: listSkillsNote}, nil
	})

	loadSchema := mustInputSchema[LoadSkillInput]()
	loadSchema.Properties["id"].Pattern = `^[a-z0-9]+(-[a-z0-9]+)*$`
	loadSchema.Properties["id"].MaxLength = jsonschema.Ptr(64)
	addTool(s, &mcp.Tool{Name: "load_skill", Description: "Load one skill whole: its instructions (sections Evidence, Heuristics, Person-specific assumptions and Decisions), limitations, the read tools it relies on and its references. Guidance only: it changes no tool and no data.", InputSchema: loadSchema}, func(_ context.Context, _ *mcp.CallToolRequest, in LoadSkillInput) (*mcp.CallToolResult, LoadSkillOut, error) {
		skill, err := lib.Load(in.ID)
		if errors.Is(err, skills.ErrUnknownSkill) {
			err = fmt.Errorf("%w: unknown skill %q; call list_skills for the ids", training.ErrNotFound, in.ID)
		}
		if err != nil {
			return nil, LoadSkillOut{}, toolError(err)
		}
		return nil, LoadSkillOut{Skill: skill, Note: loadSkillNote}, nil
	})
}
