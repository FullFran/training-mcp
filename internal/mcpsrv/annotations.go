package mcpsrv

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverInstructions is cross-tool guidance sent to clients on connect. ChatGPT
// only guarantees the first 512 characters count, so it must stay under that.
const serverInstructions = `Personal strength-training log. To record training call log_set: it needs no session id and creates today's session. Use start_session and add_set only to follow a plan or log a past date. To judge progress use exercise_history and weekly_volume instead of reading whole sessions. Call suggest_load before proposing a weight and volume_recommendation before changing weekly sets. Weights are kilograms. Deletions are permanent.`

// toolBehavior is what a tool declares to the client. Hosts use it to decide
// when to ask the user for confirmation; it never replaces server validation.
type toolBehavior struct {
	title       string
	readOnly    bool
	destructive bool
	idempotent  bool
}

func read(title string) toolBehavior {
	return toolBehavior{title: title, readOnly: true, idempotent: true}
}

// additive tools only add or reorder data: nothing stored is lost.
func additive(title string) toolBehavior { return toolBehavior{title: title} }

// overwriting tools delete or replace stored data, so the previous value is
// gone, though repeating the same call changes nothing further.
func overwriting(title string) toolBehavior {
	return toolBehavior{title: title, destructive: true, idempotent: true}
}

var toolBehaviors = map[string]toolBehavior{
	"get_session":           read("Get session"),
	"list_sessions":         read("List sessions"),
	"list_plans":            read("List plans"),
	"get_plan":              read("Get plan"),
	"session_progress":      read("Get session progress"),
	"suggest_load":          read("Suggest next weight"),
	"list_exercise_notes":   read("List exercise notes"),
	"list_volume_landmarks": read("List volume landmarks"),
	"volume_recommendation": read("Recommend weekly volume"),
	"exercise_history":      read("Get exercise history"),
	"weekly_volume":         read("Get weekly volume"),
	"list_exercise_groups":  read("List exercise groups"),
	"volume_by_muscle":      read("Get volume by muscle"),

	"start_session":        additive("Start session"),
	"add_set":              additive("Add set"),
	"log_set":              additive("Log set"),
	"create_plan":          additive("Create plan"),
	"move_plan_exercise":   additive("Move plan exercise"),
	"save_session_as_plan": additive("Save session as plan"),

	"update_set":              overwriting("Update set"),
	"delete_set":              overwriting("Delete set"),
	"delete_session":          overwriting("Delete session"),
	"update_plan":             overwriting("Update plan"),
	"set_plan_exercise":       overwriting("Set plan exercise"),
	"remove_plan_exercise":    overwriting("Remove plan exercise"),
	"delete_plan":             overwriting("Delete plan"),
	"add_session_exercise":    overwriting("Add session exercise"),
	"adjust_session_exercise": overwriting("Adjust session exercise"),
	"swap_session_exercise":   overwriting("Swap session exercise"),
	"remove_session_exercise": overwriting("Remove session exercise"),
	"set_exercise_note":       overwriting("Set exercise note"),
	"record_feedback":         overwriting("Record feedback"),
	"set_volume_landmarks":    overwriting("Set volume landmarks"),
	"set_exercise_group":      overwriting("Set exercise group"),
}

// addTool registers a tool with its declared behavior. A tool missing from
// toolBehaviors panics at startup, the same way mcp.AddTool treats a bad tool:
// left undeclared, the protocol defaults would call it destructive and open-world.
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	b, ok := toolBehaviors[t.Name]
	if !ok {
		panic(fmt.Sprintf("mcpsrv: tool %q has no entry in toolBehaviors", t.Name))
	}
	t.Title = b.title
	t.Annotations = &mcp.ToolAnnotations{
		ReadOnlyHint:    b.readOnly,
		DestructiveHint: &b.destructive,
		IdempotentHint:  b.idempotent,
		OpenWorldHint:   new(bool),
	}
	mcp.AddTool(s, t, h)
}
