package mcpsrv

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/skills"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func skillSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return mustConnect(t, New(only(training.NewService(testStore{}, time.Now))).Handler())
}

func rawContent(t *testing.T, result *mcp.CallToolResult) []byte {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool error: %q", stringContent(result))
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestListSkillsIsMetadataOnlyAndSmall(t *testing.T) {
	session := skillSession(t)
	data := rawContent(t, call(t, session, "list_skills", nil))
	for _, field := range []string{`"instructions"`, `"references"`, `"required_tools"`, `"limitations"`} {
		if strings.Contains(string(data), field) {
			t.Errorf("list_skills returns %s; the catalog is metadata only", field)
		}
	}
	if len(data) > 4096 {
		t.Errorf("list_skills is %d bytes, want at most 4096", len(data))
	}
	var out SkillsOut
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range out.Skills {
		ids = append(ids, e.ID)
		if e.Version == "" || e.Summary == "" || len(e.Triggers) == 0 || e.ReviewStatus == "" {
			t.Errorf("entry %q lacks metadata: %+v", e.ID, e)
		}
	}
	if want := []string{"exercise-selection", "fatigue-autoregulation", "hypertrophy-programming"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if again := rawContent(t, call(t, session, "list_skills", nil)); string(again) != string(data) {
		t.Fatal("list_skills is not deterministic")
	}
}

func TestLoadSkillReturnsTheWholeSkill(t *testing.T) {
	var out LoadSkillOut
	decode(t, call(t, skillSession(t), "load_skill", map[string]any{"id": "fatigue-autoregulation"}), &out)
	s := out.Skill
	if s.ID != "fatigue-autoregulation" || s.Version != "0.1.0" || s.ReviewStatus != skills.ReviewDraftUnreviewed {
		t.Fatalf("skill = %s@%s %s", s.ID, s.Version, s.ReviewStatus)
	}
	for _, section := range skills.Sections {
		if !strings.Contains(s.Instructions, "\n## "+section+"\n") {
			t.Errorf("instructions lack section %q", section)
		}
	}
	if !strings.Contains(s.Instructions, "performance_dropoff") || len(s.Limitations) == 0 || len(s.RequiredTools) == 0 {
		t.Errorf("skill lacks its tools or limitations: %+v", s.RequiredTools)
	}
	if len(s.References) == 0 || !strings.Contains(s.References[0].Content, "- DOI: ") {
		t.Errorf("references = %+v", s.References)
	}
	if !strings.Contains(out.Note, "not authority") {
		t.Errorf("note = %q", out.Note)
	}
}

func TestLoadSkillRejectsAnUnknownIDClearly(t *testing.T) {
	session := skillSession(t)
	result := call(t, session, "load_skill", map[string]any{"id": "no-such-skill"})
	if text := stringContent(result); !result.IsError || !strings.Contains(text, `unknown skill "no-such-skill"`) || !strings.Contains(text, "list_skills") {
		t.Fatalf("result IsError=%v content=%q", result.IsError, text)
	}
	for _, id := range []string{"", "Not An Id", "../hypertrophy-programming", strings.Repeat("a", 65)} {
		if result := call(t, session, "load_skill", map[string]any{"id": id}); !result.IsError {
			t.Errorf("load_skill %q succeeded", id)
		}
	}
}

// A skill only names tools it reads; every name must be a registered tool
// that writes nothing, so a skill can never point an agent at a missing tool
// or present a write as part of reading it.
func TestEverySkillRequiredToolIsARegisteredReadTool(t *testing.T) {
	tools, err := skillSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	required := skills.Default().RequiredTools()
	if len(required) == 0 {
		t.Fatal("no required tools")
	}
	for _, name := range required {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("a skill requires %q, which is not a registered tool", name)
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("a skill requires %q, which is not read-only", name)
		}
	}
}

func TestLoadingASkillLeavesTheToolListUnchanged(t *testing.T) {
	session := skillSession(t)
	before, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range skills.Default().Catalog() {
		rawContent(t, call(t, session, "load_skill", map[string]any{"id": e.ID}))
	}
	after, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(toolNames(before), toolNames(after)) {
		t.Fatalf("tools changed after load_skill:\nbefore %v\nafter  %v", toolNames(before), toolNames(after))
	}
}
