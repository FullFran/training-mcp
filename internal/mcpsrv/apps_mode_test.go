package mcpsrv

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// render_rest_timer takes one optional argument, the presentation mode of the
// view. Without it the view is the full one, as before; the countdown data, the
// text fallback and the rows are the same in both modes.
func TestRenderRestTimerModeOnlyChangesPresentation(t *testing.T) {
	path := sqlitestoretest.Path(t)
	store, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := training.NewService(store.ForUser(1), liveClock)
	session := mustConnect(t, New(only(service)).Handler())
	_, sets, err := service.LogSet(context.Background(), "elevaciones laterales", 8, 12, 8, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE sets SET created_at='2026-08-07 09:59:25' WHERE id=?`, sets[0].ID); err != nil {
		t.Fatal(err)
	}
	before := dumpTables(t, db, nil)

	render := func(args map[string]any) (RestTimerOut, string) {
		result := call(t, session, "render_rest_timer", args)
		var out RestTimerOut
		decode(t, result, &out)
		return out, stringContent(result)
	}
	omitted, omittedText := render(map[string]any{})
	full, fullText := render(map[string]any{"mode": "full"})
	compact, compactText := render(map[string]any{"mode": "compact"})

	if omitted.Mode != "full" || full.Mode != "full" || compact.Mode != "compact" {
		t.Fatalf("modes = %q (omitted), %q (full), %q (compact)", omitted.Mode, full.Mode, compact.Mode)
	}
	// The same server-authoritative rest state and the same text fallback.
	for _, other := range []RestTimerOut{full, compact} {
		other.Mode, omitted.Mode = "", ""
		if !reflect.DeepEqual(other, omitted) {
			t.Fatalf("a mode changed the data:\n%+v\n%+v", other, omitted)
		}
	}
	if fullText != omittedText || compactText != omittedText {
		t.Fatalf("text fallback differs: %q, %q, %q", omittedText, fullText, compactText)
	}
	if omitted.Rest.State != training.RestResting || omitted.LastSet == nil || omitted.NextSet == nil {
		t.Fatalf("the data the full view needs is gone: %+v", omitted)
	}
	// Rendering never writes: no rest interval, no row.
	if after := dumpTables(t, db, nil); after != before {
		t.Fatalf("rendering changed rows:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestRenderRestTimerRefusesAnUnknownMode(t *testing.T) {
	session := mustConnect(t, New(only(training.NewService(testStore{}, liveClock))).Handler())
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "render_rest_timer", Arguments: map[string]any{"mode": "tiny"}})
	if err == nil && !result.IsError {
		t.Fatalf("an unknown mode was accepted: %+v", result)
	}
}

// The only argument is the mode: a user id would be a hole.
func TestRenderRestTimerSchemaIsJustTheMode(t *testing.T) {
	session := mustConnect(t, New(only(training.NewService(testStore{}, liveClock))).Handler())
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "render_rest_timer" {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		mode, ok := schema.Properties["mode"]
		if len(schema.Properties) != 1 || !ok || !reflect.DeepEqual(mode.Enum, []string{"compact", "full"}) || len(schema.Required) != 0 {
			t.Fatalf("schema = %s", raw)
		}
		return
	}
	t.Fatal("render_rest_timer is not listed")
}
