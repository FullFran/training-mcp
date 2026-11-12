package mcpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// wantActionType is the action type each write tool's call is proposed under, for
// the arguments isolationCases gives it.
var wantActionType = map[string]string{
	"start_session": "session.start", "add_set": "set.log", "log_set": "set.log", "log_sets": "set.log",
	"update_set": "set.update", "delete_set": "set.delete", "delete_session": "session.delete",
	"create_plan": "plan.create", "save_session_as_plan": "plan.create", "update_plan": "plan.update",
	"set_plan_exercise": "plan.exercise.set", "remove_plan_exercise": "plan.exercise.remove",
	"move_plan_exercise": "plan.exercise.move", "delete_plan": "plan.delete",
	"add_session_exercise": "session.exercise.set", "adjust_session_exercise": "session.exercise.adjust",
	"swap_session_exercise": "session.exercise.swap", "remove_session_exercise": "session.exercise.remove",
	"set_exercise_note": "exercise.note.set", "set_exercise_group": "exercise.group.set",
	"record_feedback": "feedback.record", "set_volume_landmarks": "landmarks.set",
	"set_rest_target": "rest.set", "stop_rest": "rest.stop",
	"correct_lb_sets": "set.correct_lb",
	"memory_save":     "memory.save", "memory_forget": "memory.forget",
	"memory_judge": "memory.judge", "memory_confirm": "memory.confirm", "memory_consolidate": "memory.consolidate",
}

// Every tool that is not read-only proposes, and no read-only tool does.
func TestWriteToolsAreExactlyTheToolsThatChangeData(t *testing.T) {
	s := New(only(training.NewService(testStore{}, nil)))
	got := s.WriteTools()
	slices.Sort(got)
	var want []string
	for name, b := range toolBehaviors {
		if !b.readOnly {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("write tools = %v, want %v", got, want)
	}
	for _, name := range want {
		if _, ok := wantActionType[name]; !ok {
			t.Errorf("%s has no expected action type", name)
		}
	}
}

func structured(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool error: %s", stringContent(result))
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// timestamps differ between two runs of the same change; nothing else may.
var timestamps = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})?`)

func withoutProposal(t *testing.T, v map[string]any) string {
	t.Helper()
	delete(v, "status")
	delete(v, "proposal_id")
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return timestamps.ReplaceAllString(string(data), "T")
}

// With ask-everything, each write tool records a pending proposal of its action
// type and changes nothing. Accepting it makes exactly the change, and returns
// exactly the result, the tool gives with accept-all on.
func TestEveryWriteToolProposesAndAcceptingAppliesTheSameChange(t *testing.T) {
	ctx := context.Background()
	for name, action := range wantActionType {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			direct := newIsolationWorld(t)
			args := isolationCases[name].args(direct.ids)
			if name == "log_sets" {
				args["idempotency_key"] = "nueva" // the world already used "clave"
			}
			immediate := structured(t, call(t, direct.owner, name, args))
			if immediate["status"] != "accepted" || immediate["proposal_id"] == float64(0) {
				t.Fatalf("accept-all result = %v", immediate)
			}
			wantData := timestamps.ReplaceAllString(dumpTables(t, direct.db, nil), "T")

			world := newIsolationWorld(t)
			if err := world.ownerService.SetAcceptAll(ctx, false); err != nil {
				t.Fatal(err)
			}
			before := dumpTables(t, world.db, nil)
			pending := structured(t, call(t, world.owner, name, args))
			if pending["status"] != "pending" {
				t.Fatalf("result = %v, want pending", pending)
			}
			if after := dumpTables(t, world.db, nil); after != before {
				t.Fatalf("a pending proposal changed data:\nbefore\n%s\nafter\n%s", before, after)
			}
			id := int64(pending["proposal_id"].(float64))
			p, err := world.ownerService.GetProposal(ctx, id)
			if err != nil || p.Status != training.ProposalPending || p.ActionType != action || p.Tool != name {
				t.Fatalf("proposal = %+v, %v; want %s pending", p, err, action)
			}

			accepted, err := world.ownerService.AcceptProposal(ctx, id, training.AnswerAllowOnce, world.ownerServer.Apply)
			if err != nil || accepted.Status != training.ProposalAccepted {
				t.Fatalf("accept = %+v, %v", accepted, err)
			}
			var result map[string]any
			if err := json.Unmarshal(accepted.Result, &result); err != nil {
				t.Fatal(err)
			}
			if got, want := withoutProposal(t, result), withoutProposal(t, immediate); got != want {
				t.Fatalf("accepted result = %s, want %s", got, want)
			}
			if got := timestamps.ReplaceAllString(dumpTables(t, world.db, nil), "T"); got != wantData {
				t.Fatalf("accepted change differs:\ngot\n%s\nwant\n%s", got, wantData)
			}
		})
	}
}

func TestRejectedProposalChangesNothing(t *testing.T) {
	ctx := context.Background()
	world := newIsolationWorld(t)
	if err := world.ownerService.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	before := dumpTables(t, world.db, nil)
	pending := structured(t, call(t, world.owner, "delete_session", map[string]any{"session_id": world.ids.session}))
	if _, err := world.ownerService.RejectProposal(ctx, int64(pending["proposal_id"].(float64)), training.AnswerRejectOnce); err != nil {
		t.Fatal(err)
	}
	if after := dumpTables(t, world.db, nil); after != before {
		t.Fatalf("rejecting changed data:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// A note set to empty removes it, a different action from setting one.
func TestEmptyNoteIsProposedAsRemoval(t *testing.T) {
	ctx := context.Background()
	world := newIsolationWorld(t)
	_ = world.ownerService.SetAcceptAll(ctx, false)
	out := structured(t, call(t, world.owner, "set_exercise_note", map[string]any{"exercise": isolationExercise, "note": " "}))
	p, err := world.ownerService.GetProposal(ctx, int64(out["proposal_id"].(float64)))
	if err != nil || p.ActionType != "exercise.note.remove" {
		t.Fatalf("proposal = %+v, %v", p, err)
	}
}

// A pending proposal belongs to whoever proposed it: the other user can neither
// apply it nor learn it exists.
func TestPendingProposalsAreIsolatedBetweenUsers(t *testing.T) {
	ctx := context.Background()
	world := newIsolationWorld(t)
	_ = world.ownerService.SetAcceptAll(ctx, false)
	out := structured(t, call(t, world.owner, "delete_session", map[string]any{"session_id": world.ids.session}))
	id := int64(out["proposal_id"].(float64))
	before := dumpTables(t, world.db, nil)

	second := world.services.ForUser(2)
	_ = second.SetAcceptAll(ctx, false)
	if _, err := second.AcceptProposal(ctx, id, training.AnswerAllowAlways, world.ownerServer.Apply); err == nil || err.Error() != training.ErrNotFound.Error() {
		t.Fatalf("second user accepting = %v", err)
	}
	if list, _ := second.ListProposals(ctx, training.ProposalFilter{Pending: true}); len(list) != 0 {
		t.Fatalf("second user sees %+v", list)
	}
	if after := dumpTables(t, world.db, nil); after != before {
		t.Fatalf("data changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// Read tools answer as before: no proposal is recorded and no status is added.
func TestReadToolsDoNotPropose(t *testing.T) {
	ctx := context.Background()
	world := newIsolationWorld(t)
	_ = world.ownerService.SetAcceptAll(ctx, false)
	for name, b := range toolBehaviors {
		if !b.readOnly {
			continue
		}
		out := structured(t, call(t, world.owner, name, isolationCases[name].args(world.ids)))
		if _, ok := out["status"]; ok {
			t.Errorf("%s returned a status: %v", name, out)
		}
	}
	for _, pending := range []bool{true, false} {
		if list, _ := world.ownerService.ListProposals(ctx, training.ProposalFilter{Pending: pending}); len(list) != 0 {
			t.Fatalf("read tools recorded proposals: %+v", list)
		}
	}
}

// Every write tool declares status and proposal_id in its output, and says that
// a pending change is not applied.
func TestWriteToolsDeclareTheProposalContract(t *testing.T) {
	session := mustConnect(t, New(only(training.NewService(testStore{}, nil))).Handler())
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if _, write := wantActionType[tool.Name]; !write {
			continue
		}
		schema := schemaMap(t, tool.OutputSchema)
		required := stringSlice(schema["required"])
		if !slices.Contains(required, "status") || !slices.Contains(required, "proposal_id") {
			t.Errorf("%s output requires %v", tool.Name, required)
		}
		if !regexp.MustCompile(`'pending'.*'rejected'.*not applied`).MatchString(tool.Description) {
			t.Errorf("%s description does not explain pending and rejected: %q", tool.Name, tool.Description)
		}
		status, _ := schemaMap(t, schema["properties"])["status"].(map[string]any)
		if desc, _ := status["description"].(string); !strings.Contains(desc, "'rejected'") {
			t.Errorf("%s status does not explain rejected: %q", tool.Name, desc)
		}
	}
}

// engineBearer sends every request with one bearer, as the coach's engine does.
type engineBearer struct{ token string }

func (b engineBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connectWithBearer(t *testing.T, handler http.Handler, token string) *mcp.ClientSession {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "coach", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: engineBearer{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// A coach conversation is its own session, keyed by its engine bearer; every
// external client shares the external session; a reject_always in one
// conversation rejects there and nowhere else, and the agent is told so.
func TestProposalsCarryTheirChannelsSession(t *testing.T) {
	ctx := context.Background()
	world := newIsolationWorld(t)
	_ = world.ownerService.SetAcceptAll(ctx, false)
	coach := connectWithBearer(t, world.ownerServer.Handler(), "tme_conversation-one")
	otherCoach := connectWithBearer(t, world.ownerServer.Handler(), "tme_conversation-two")
	args := map[string]any{"exercise": isolationExercise, "note": "codos dentro"}

	proposed := func(session *mcp.ClientSession) (map[string]any, training.Proposal) {
		t.Helper()
		out := structured(t, call(t, session, "set_exercise_note", args))
		p, err := world.ownerService.GetProposal(ctx, int64(out["proposal_id"].(float64)))
		if err != nil {
			t.Fatal(err)
		}
		return out, p
	}
	_, first := proposed(coach)
	_, second := proposed(otherCoach)
	_, external := proposed(world.owner)
	if !strings.HasPrefix(first.Session, training.CoachSessionPrefix) || first.Session == second.Session ||
		strings.Contains(first.Session, "conversation-one") || external.Session != training.ExternalSession {
		t.Fatalf("sessions: coach %q, other coach %q, external %q", first.Session, second.Session, external.Session)
	}

	if _, err := world.ownerService.RejectProposal(ctx, first.ID, training.AnswerRejectAlways); err != nil {
		t.Fatal(err)
	}
	before := dumpTables(t, world.db, nil)
	out, denied := proposed(coach)
	if out["status"] != "rejected" || denied.Status != training.ProposalRejected || denied.DecidedBy != training.DecidedByDenial {
		t.Fatalf("same conversation = %v, %+v", out, denied)
	}
	if after := dumpTables(t, world.db, nil); after != before {
		t.Fatalf("a denied proposal changed data:\nbefore\n%s\nafter\n%s", before, after)
	}
	for _, session := range []*mcp.ClientSession{otherCoach, world.owner} {
		if out, _ := proposed(session); out["status"] != "pending" {
			t.Fatalf("denial reached another session: %v", out)
		}
	}
}

func TestACallNotMadeOverHTTPHasNoSession(t *testing.T) {
	for _, req := range []*mcp.CallToolRequest{nil, {}, {Extra: &mcp.RequestExtra{}}} {
		if got := agentSession(req, nil); got != "" {
			t.Fatalf("agentSession(%+v) = %q", req, got)
		}
	}
}

// With stored coach conversations, the session is the conversation, not the
// engine process: a reject_always holds for a later engine of the same chat
// and for no other chat.
func TestACoachProposalBelongsToItsStoredConversation(t *testing.T) {
	ctx := context.Background()
	world := newIsolationWorld(t)
	_ = world.ownerService.SetAcceptAll(ctx, false)
	chats := map[string]string{
		"tme_first-process":  training.CoachSessionPrefix + "conversation-7",
		"tme_second-process": training.CoachSessionPrefix + "conversation-7",
		"tme_other-chat":     training.CoachSessionPrefix + "conversation-8",
	}
	world.ownerServer.SetCoachSessions(func(token string) (string, bool) {
		s, ok := chats[token]
		return s, ok
	})
	args := map[string]any{"exercise": isolationExercise, "note": "codos dentro"}
	proposed := func(token string) (map[string]any, training.Proposal) {
		t.Helper()
		session := connectWithBearer(t, world.ownerServer.Handler(), token)
		out := structured(t, call(t, session, "set_exercise_note", args))
		p, err := world.ownerService.GetProposal(ctx, int64(out["proposal_id"].(float64)))
		if err != nil {
			t.Fatal(err)
		}
		return out, p
	}
	_, first := proposed("tme_first-process")
	if first.Session != chats["tme_first-process"] {
		t.Fatalf("session = %q, want the stored conversation", first.Session)
	}
	if _, err := world.ownerService.RejectProposal(ctx, first.ID, training.AnswerRejectAlways); err != nil {
		t.Fatal(err)
	}
	if out, p := proposed("tme_second-process"); out["status"] != "rejected" || p.DecidedBy != training.DecidedByDenial {
		t.Fatalf("a later engine of the same chat = %v, %+v", out, p)
	}
	if out, _ := proposed("tme_other-chat"); out["status"] != "pending" {
		t.Fatalf("the denial reached another chat: %v", out)
	}
	// A bearer the coach does not know keeps its process session.
	if _, p := proposed("tme_unknown"); !strings.HasPrefix(p.Session, training.CoachSessionPrefix) || strings.Contains(p.Session, "conversation-") {
		t.Fatalf("unknown bearer session = %q", p.Session)
	}
}
