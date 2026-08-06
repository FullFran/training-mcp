package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/httpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerProtocolRegistrationTypedSchemasAndCRUDFlow(t *testing.T) {
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := New(training.NewService(store, time.Now))
	h := httptest.NewServer(httpsrv.Routes(s.Handler(), false, "secret"))
	defer h.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: h.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{base: http.DefaultTransport}}}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedToolNames(tools), []string{"add_set", "delete_set", "get_session", "list_sessions", "start_session", "update_set"}) {
		t.Fatalf("tools=%v", toolNames(tools))
	}
	for _, tool := range tools.Tools {
		if tool.InputSchema == nil {
			t.Fatalf("tool %q has no typed input schema", tool.Name)
		}
	}
	start := call(t, session, "start_session", map[string]any{"date": "2026-08-06"})
	var started SessionOut
	decode(t, start, &started)
	add := call(t, session, "add_set", map[string]any{"session_id": started.Session.ID, "exercise": " Bench ", "weight_kg": 80, "reps": 5, "rpe": 8})
	var added SetOut
	decode(t, add, &added)
	if added.Set.Exercise != "bench" || added.TotalSI != added.Set.SI || added.TotalSI != 1 {
		t.Fatalf("add=%#v", added)
	}
	add2 := call(t, session, "add_set", map[string]any{"session_id": started.Session.ID, "exercise": "row", "weight_kg": 50, "reps": 5, "rpe": 10})
	var added2 SetOut
	decode(t, add2, &added2)
	if added2.TotalSI != 2.4 {
		t.Fatalf("second add total=%v", added2.TotalSI)
	}
	updated := call(t, session, "update_set", map[string]any{"set_id": added.Set.ID, "rpe": 2})
	var corrected SetOut
	decode(t, updated, &corrected)
	if corrected.Set.SI != 0 || corrected.TotalSI != 1.4 {
		t.Fatalf("update=%#v", corrected)
	}
	got := call(t, session, "get_session", map[string]any{"session_id": started.Session.ID})
	var retrieved SessionOut
	decode(t, got, &retrieved)
	if len(retrieved.Session.Sets) != 2 || retrieved.Session.TotalSI != 1.4 {
		t.Fatalf("get=%#v", retrieved)
	}
	deleted := call(t, session, "delete_set", map[string]any{"set_id": added.Set.ID})
	var removed DeleteOut
	decode(t, deleted, &removed)
	if removed.Remaining != 1 || removed.TotalSI != 1.4 {
		t.Fatalf("delete=%#v", removed)
	}
	listed := call(t, session, "list_sessions", map[string]any{})
	var summaries ListOut
	decode(t, listed, &summaries)
	if len(summaries.Sessions) != 1 || summaries.Sessions[0].SetCount != 1 || summaries.Sessions[0].TotalSI != 1.4 {
		t.Fatalf("list=%#v", summaries)
	}
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_set", Arguments: map[string]any{"set_id": added2.Set.ID, "rpe": 99}}); err == nil {
		t.Fatal("update_set accepted RPE outside its declared range")
	}
	if _, err := store.GetSession(context.Background(), started.Session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestServerToolsDescribeTheirInputsAndValidationContract(t *testing.T) {
	session := mustConnect(t, New(training.NewService(testStore{}, time.Now)).Handler())
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		description string
		required    []string
		properties  map[string]map[string]any
		minProps    float64
	}{
		{
			name:        "start_session",
			description: "Start a training session on an optional date. Omit date to use the server's current local date.",
			properties: map[string]map[string]any{
				"date": {"description": "Training date in exact YYYY-MM-DD format.", "pattern": `^\d{4}-\d{2}-\d{2}$`},
			},
		},
		{
			name:        "add_set",
			description: "Add a set to an existing training session and return the set plus the session's recalculated total SI.",
			required:    []string{"session_id", "exercise", "weight_kg", "reps", "rpe"},
			properties: map[string]map[string]any{
				"session_id": {"description": "Positive ID of the existing training session.", "minimum": float64(1)},
				"exercise":   {"description": "Non-empty exercise name; it is trimmed and lowercased before storage.", "pattern": `.*\S.*`},
				"weight_kg":  {"description": "Weight in kilograms; must be strictly positive.", "exclusiveMinimum": float64(0)},
				"reps":       {"description": "Repetition count; must be strictly positive.", "minimum": float64(1)},
				"rpe":        {"description": "Numeric rate of perceived exertion from 1 through 10; determines the set's SI.", "minimum": float64(1), "maximum": float64(10)},
			},
		},
		{
			name:        "update_set",
			description: "Update one or more fields of an existing set. Omitted fields remain unchanged; RPE changes recalculate SI.",
			required:    []string{"set_id"},
			minProps:    2,
			properties: map[string]map[string]any{
				"set_id":    {"description": "Positive ID of the existing set to update.", "minimum": float64(1)},
				"exercise":  {"description": "Non-empty replacement exercise name; it is trimmed and lowercased before storage.", "type": "string", "pattern": `.*\S.*`},
				"weight_kg": {"description": "Replacement weight in kilograms; must be strictly positive.", "type": "number", "exclusiveMinimum": float64(0)},
				"reps":      {"description": "Replacement repetition count; must be strictly positive.", "type": "integer", "minimum": float64(1)},
				"rpe":       {"description": "Replacement numeric RPE from 1 through 10; recalculates the set's SI.", "type": "number", "minimum": float64(1), "maximum": float64(10)},
			},
		},
		{
			name:        "delete_set",
			description: "Permanently delete an existing set and compact the remaining set positions to a dense sequence.",
			required:    []string{"set_id"},
			properties: map[string]map[string]any{
				"set_id": {"description": "Positive ID of the existing set to delete.", "minimum": float64(1)},
			},
		},
		{
			name:        "get_session",
			description: "Get an existing training session with its ordered sets and total SI.",
			required:    []string{"session_id"},
			properties: map[string]map[string]any{
				"session_id": {"description": "Positive ID of the existing training session to retrieve.", "minimum": float64(1)},
			},
		},
		{
			name:        "list_sessions",
			description: "List training sessions newest first, optionally filtered by inclusive date bounds. From must not be after to.",
			properties: map[string]map[string]any{
				"limit": {"description": "Maximum sessions to return; defaults to 20 and accepts 1 through 100.", "default": float64(20), "minimum": float64(1), "maximum": float64(100)},
				"from":  {"description": "Inclusive earliest training date in exact YYYY-MM-DD format.", "pattern": `^\d{4}-\d{2}-\d{2}$`},
				"to":    {"description": "Inclusive latest training date in exact YYYY-MM-DD format.", "pattern": `^\d{4}-\d{2}-\d{2}$`},
			},
		},
	}

	toolsByName := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		toolsByName[tool.Name] = tool
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := toolsByName[tt.name]
			if tool == nil {
				t.Fatalf("tool %q is not registered", tt.name)
			}
			if tool.Description != tt.description {
				t.Fatalf("description = %q, want %q", tool.Description, tt.description)
			}
			schema := schemaMap(t, tool.InputSchema)
			if got := stringSlice(schema["required"]); !reflect.DeepEqual(got, tt.required) {
				t.Fatalf("required = %v, want %v", got, tt.required)
			}
			if tt.minProps > 0 && schema["minProperties"] != tt.minProps {
				t.Fatalf("minProperties = %v, want %v", schema["minProperties"], tt.minProps)
			}
			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("properties = %#v", schema["properties"])
			}
			for property, expected := range tt.properties {
				actual, ok := properties[property].(map[string]any)
				if !ok {
					t.Fatalf("property %q = %#v", property, properties[property])
				}
				for keyword, want := range expected {
					if actual[keyword] != want {
						t.Errorf("property %q %s = %#v, want %#v", property, keyword, actual[keyword], want)
					}
				}
			}
		})
	}
}

func TestListSessionsRejectsInvertedDateRangeThroughMCP(t *testing.T) {
	session := mustConnect(t, New(training.NewService(testStore{}, time.Now)).Handler())
	result := call(t, session, "list_sessions", map[string]any{
		"from": "2026-08-07",
		"to":   "2026-08-06",
	})
	message := stringContent(result)

	if !result.IsError {
		t.Fatalf("result IsError = false, want true: %#v", result)
	}
	if !strings.Contains(message, "from must not be after to") {
		t.Fatalf("error message = %q, want date range guidance", message)
	}
	if strings.Contains(strings.ToLower(message), "sql") || strings.Contains(message, "internal tool error") {
		t.Fatalf("error message exposes internal details: %q", message)
	}
}

func TestListSessionsSerializesEmptyResultsAsArray(t *testing.T) {
	session := mustConnect(t, New(training.NewService(testStore{}, time.Now)).Handler())
	result := call(t, session, "list_sessions", map[string]any{})

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"sessions":[]}` {
		t.Fatalf("MCP output = %s, want empty sessions array", data)
	}
}

func TestUnexpectedStartSessionErrorUsesGenericMCPError(t *testing.T) {
	s := New(training.NewService(errorStore{}, time.Now))
	result := call(t, mustConnect(t, s.Handler()), "start_session", map[string]any{"date": "2026-08-06"})
	if !result.IsError || stringContent(result) != "internal tool error" {
		t.Fatalf("result=%#v content=%q", result, stringContent(result))
	}
}

func TestServerSerializesNormalizedSIValuesAndTotal(t *testing.T) {
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session := mustConnect(t, New(training.NewService(store, time.Now)).Handler())
	startedResult := call(t, session, "start_session", map[string]any{"date": "2026-08-06"})
	var started SessionOut
	decode(t, startedResult, &started)

	var result *mcp.CallToolResult
	for i, rpe := range []float64{9, 9, 10} {
		result = call(t, session, "add_set", map[string]any{
			"session_id": started.Session.ID,
			"exercise":   "press",
			"weight_kg":  50,
			"reps":       5,
			"rpe":        rpe,
		})
		if i == 0 {
			var first SetOut
			decode(t, result, &first)
			if first.Set.SI != 1.2 || first.TotalSI != 1.2 {
				t.Fatalf("first MCP result = %#v, want SI and total 1.2", first)
			}
		}
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("3.8000000000000003")) {
		t.Fatalf("MCP output contains floating-point artifact: %s", data)
	}
	if !bytes.Contains(data, []byte(`"si":1.4`)) || !bytes.Contains(data, []byte(`"total_si":3.8`)) {
		t.Fatalf("MCP output = %s, want SI 1.4 and total 3.8", data)
	}
}

type testStore struct{}

type errorStore struct{ testStore }

func (errorStore) Start(context.Context, training.Session) (training.Session, error) {
	return training.Session{}, context.DeadlineExceeded
}

func (testStore) Start(context.Context, training.Session) (training.Session, error) {
	return training.Session{}, nil
}
func (testStore) AddSet(context.Context, training.AddSetInput) (training.Set, float64, error) {
	return training.Set{}, 0, nil
}
func (testStore) UpdateSet(context.Context, int64, training.SetPatch) (training.Set, float64, error) {
	return training.Set{}, 0, nil
}
func (testStore) DeleteSet(context.Context, int64) (int64, float64, int, error) { return 0, 0, 0, nil }
func (testStore) GetSession(context.Context, int64) (training.Session, error) {
	return training.Session{}, nil
}
func (testStore) ListSessions(context.Context, training.ListFilter) ([]training.SessionSummary, error) {
	return nil, nil
}

type bearerTransport struct{ base http.RoundTripper }

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer secret")
	return t.base.RoundTrip(r)
}

func toolNames(result *mcp.ListToolsResult) []string {
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func sortedToolNames(result *mcp.ListToolsResult) []string {
	names := toolNames(result)
	sort.Strings(names)
	return names
}
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func decode(t *testing.T, result *mcp.CallToolResult, out any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool error: %q", stringContent(result))
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}
func stringContent(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	if text, ok := result.Content[0].(*mcp.TextContent); ok {
		return text.Text
	}
	return ""
}

func schemaMap(t *testing.T, schema any) map[string]any {
	t.Helper()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.(string))
	}
	return result
}

func mustConnect(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "error-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
