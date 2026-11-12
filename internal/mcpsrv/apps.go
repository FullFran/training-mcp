package mcpsrv

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP Apps (extension io.modelcontextprotocol/ui, specification 2026-01-26):
// a tool names an HTML view through _meta.ui.resourceUri, the host reads that
// ui:// resource and renders the tool's result in a sandboxed iframe. The
// views are built from ui/ (TypeScript on the official ext-apps SDK) into
// self-contained files under apps/, committed, and embedded here. See
// docs/apps.md.
//
// The server is stateless, so tools/list cannot see whether the client
// declared the extension: the tool always carries its _meta.ui, hosts without
// MCP Apps ignore it, and every result carries a readable text as well.

const (
	appMIMEType  = "text/html;profile=mcp-app"
	restTimerURI = "ui://vibetraining/rest-timer/v1"
)

//go:embed apps/rest-timer.html
var restTimerHTML string

// appToolMeta is the tool _meta for a view: the current nested key and the
// deprecated flat key the official registerAppTool still sends for hosts that
// read only that one.
func appToolMeta(uri string) mcp.Meta {
	return mcp.Meta{"ui": map[string]any{"resourceUri": uri}, "ui/resourceUri": uri}
}

// RestTimerSet is the last logged set, as the timer shows it.
type RestTimerSet struct {
	Exercise string  `json:"exercise"`
	WeightKG float64 `json:"weight_kg"`
	Reps     int     `json:"reps"`
}

// RestTimerOut is what the rest-timer view starts from. The view counts down
// locally from Rest.ElapsedSeconds; the server's clock stays the truth.
type RestTimerOut struct {
	Now              string        `json:"now"`
	Rest             training.Rest `json:"rest"`
	IdleAfterSeconds int           `json:"idle_after_seconds"`
	LastSet          *RestTimerSet `json:"last_set,omitempty"`
	NextExercise     string        `json:"next_exercise,omitempty"`
	NextSuperset     bool          `json:"next_superset,omitempty"`
	WeightUnit       string        `json:"weight_unit"`
}

func restTimerOut(live training.LiveWorkout) RestTimerOut {
	out := RestTimerOut{
		Now:              live.Now,
		Rest:             live.Rest,
		IdleAfterSeconds: int(training.RestIdleAfter / time.Second),
		WeightUnit:       live.WeightUnit,
	}
	if live.LastSet != nil {
		out.LastSet = &RestTimerSet{Exercise: live.LastSet.Set.Exercise, WeightKG: live.LastSet.Set.WeightKG, Reps: live.LastSet.Set.Reps}
	}
	if live.Next != nil {
		out.NextExercise = live.Next.Exercise
		out.NextSuperset = live.Rest.State == training.RestSuperset
	}
	return out
}

// restTimerText is the result's text: what a host that cannot render the view
// shows, and what the model reads.
func restTimerText(out RestTimerOut) string {
	r := out.Rest
	after := ""
	if out.LastSet != nil {
		after = " after " + out.LastSet.Exercise
	}
	var text string
	switch r.State {
	case training.RestResting:
		text = fmt.Sprintf("Resting: %s left of %s%s.", clock(r.RemainingSeconds), clock(r.TargetSeconds), after)
	case training.RestReady:
		text = fmt.Sprintf("Rest done: %s target reached %s ago%s.", clock(r.TargetSeconds), clock(r.OverBySeconds), after)
	case training.RestSuperset:
		if out.NextExercise != "" {
			return fmt.Sprintf("Superset: go straight to %s, no rest.", out.NextExercise)
		}
		return "Superset: next exercise, no rest."
	case training.RestIdle:
		return fmt.Sprintf("No rest running: the last set was over %d minutes ago.", int(training.RestIdleAfter/time.Minute))
	default:
		return fmt.Sprintf("No set logged today yet. Rest target %s.", clock(r.TargetSeconds))
	}
	if out.NextExercise != "" {
		text += " Next: " + out.NextExercise + "."
	}
	return text
}

// clock formats seconds as m:ss, as the view does.
func clock(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func addApps(s *mcp.Server, service training.ServiceFor) {
	s.AddResource(&mcp.Resource{
		URI:         restTimerURI,
		Name:        "rest-timer",
		Title:       "Rest timer",
		Description: "Interactive rest countdown after a logged set, for hosts that support MCP Apps.",
		MIMEType:    appMIMEType,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if req.Params.URI != restTimerURI {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: restTimerURI, MIMEType: appMIMEType, Text: restTimerHTML}}}, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "render_rest_timer",
		Description: "Show the person's rest countdown since their last logged set, as an interactive timer in chats that support MCP Apps and as text elsewhere. Call it right after logging sets during a workout. It reads only; it records nothing and needs no arguments.",
		Meta:        appToolMeta(restTimerURI),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, RestTimerOut, error) {
		live, err := service(ctx).LiveWorkout(ctx)
		if err != nil {
			return nil, RestTimerOut{}, toolError(err)
		}
		out := restTimerOut(live)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: restTimerText(out)}}}, out, nil
	})
}
