package mcpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
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
	invalid := call(t, session, "update_set", map[string]any{"set_id": added2.Set.ID, "rpe": 99})
	if !invalid.IsError || invalid.Content == nil {
		t.Fatalf("invalid mutation result=%#v", invalid)
	}
	if _, err := store.GetSession(context.Background(), started.Session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUnexpectedStartSessionErrorUsesGenericMCPError(t *testing.T) {
	s := New(training.NewService(errorStore{}, time.Now))
	result := call(t, mustConnect(t, s.Handler()), "start_session", map[string]any{"date": "2026-08-06"})
	if !result.IsError || stringContent(result) != "internal tool error" {
		t.Fatalf("result=%#v content=%q", result, stringContent(result))
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
