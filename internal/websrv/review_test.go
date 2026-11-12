package websrv_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

type reviewFixture struct {
	handler  http.Handler
	service  *training.Service
	services *training.Services
	mcp      *mcpsrv.Server
	store    *sqlitestore.Store
	path     string
}

// newReviewFixture is a user created after migration 14 (asks for everything),
// served with the CSRF token a signed-in mount carries.
func newReviewFixture(t *testing.T) reviewFixture {
	t.Helper()
	path := sqlitestoretest.Path(t)
	store, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	services := training.NewServices(store, webClock)
	id, err := store.ProvisionUser(context.Background(), "user_reviewer")
	if err != nil {
		t.Fatal(err)
	}
	service := services.ForUser(id)
	serviceFor := func(context.Context) *training.Service { return service }
	mcp := mcpsrv.New(serviceFor)
	server, err := websrv.NewWithOptions(serviceFor, services.Snapshot, webClock, "", websrv.Options{
		Apply: mcp.Apply, CSRFToken: func(*http.Request) string { return "tok" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return reviewFixture{handler: server.Handler(), service: service, services: services, mcp: mcp, store: store, path: path}
}

const coachSession = training.CoachSessionPrefix + "chat"

// propose records what the create_plan tool would propose in a coach conversation.
func (f reviewFixture) propose(t *testing.T, name string) training.Proposal {
	t.Helper()
	return f.proposeIn(t, coachSession, name)
}

func (f reviewFixture) proposeIn(t *testing.T, session, name string) training.Proposal {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"name": name, "items": []map[string]any{{"exercise": "sentadilla", "target_sets": 3}}})
	p, err := f.service.Propose(training.WithAgentSession(context.Background(), session), training.ProposalInput{Tool: "create_plan", ActionType: "plan.create", Payload: payload},
		func(ctx context.Context) (json.RawMessage, error) {
			return f.mcp.Apply(ctx, f.service, "create_plan", payload)
		})
	if err != nil {
		t.Fatalf("propose = %+v, %v", p, err)
	}
	return p
}

func (f reviewFixture) plans(t *testing.T) []training.Plan {
	t.Helper()
	plans, err := f.service.ListPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plans
}

func TestReviewPageListsWhatEachProposalChanges(t *testing.T) {
	f := newReviewFixture(t)
	f.propose(t, "pierna pesada")
	w := get(t, f.handler, "/review")
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	for _, want := range []string{"Crear rutina", "pierna pesada", "sentadilla",
		`name="answer" value="allow_once"`, "Aceptar una vez", `name="answer" value="allow_always"`, "Aceptar siempre en esta sesión",
		`name="answer" value="reject_once"`, ">Rechazar<", `name="answer" value="reject_always"`, "Rechazar siempre en esta sesión",
		"Aceptar todo", `name="csrf_token" value="tok"`, `name="viewport"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "type the name") || strings.Contains(body, "escribe el nombre") {
		t.Error("the page asks to type a name; one plain confirmation is enough")
	}
}

func TestAcceptingAppliesTheProposalAndRemembers(t *testing.T) {
	f := newReviewFixture(t)
	p := f.propose(t, "pierna pesada")
	w := post(t, f.handler, "/review/"+itoa(p.ID)+"/accept", url.Values{"answer": {"allow_always"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/review" {
		t.Fatalf("status %d location %q: %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	if plans := f.plans(t); len(plans) != 1 || plans[0].Name != "pierna pesada" {
		t.Fatalf("plans = %+v", plans)
	}
	got, _ := f.service.GetProposal(context.Background(), p.ID)
	if got.Status != training.ProposalAccepted || got.DecidedBy != training.DecidedByPerson {
		t.Fatalf("proposal = %+v", got)
	}
	// The grant is listed with its session, and the next one of that type in the
	// same conversation applies at once; another session still asks.
	if body := get(t, f.handler, "/review").Body.String(); !strings.Contains(body, "Crear rutina") || !strings.Contains(body, "aceptar en esta conversación con el coach") {
		t.Fatalf("remembered answer not shown:\n%s", body)
	}
	if next := f.proposeIn(t, coachSession, "otra"); next.Status != training.ProposalAccepted || next.DecidedBy != training.DecidedBySessionGrant {
		t.Fatalf("next = %+v", next)
	}
	if elsewhere := f.proposeIn(t, training.ExternalSession, "fuera"); elsewhere.Status != training.ProposalPending {
		t.Fatalf("another session = %+v", elsewhere)
	}
	perms, _ := f.service.Permissions(context.Background())
	if len(perms.Rules) != 1 {
		t.Fatalf("rules = %+v", perms.Rules)
	}
	// Forgetting it brings the question back.
	if w := post(t, f.handler, "/review/forget", url.Values{"rule_id": {itoa(perms.Rules[0].ID)}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("forget status %d", w.Code)
	}
	if perms, _ := f.service.Permissions(context.Background()); len(perms.Rules) != 0 {
		t.Fatalf("rules = %+v", perms.Rules)
	}
	if again := f.proposeIn(t, coachSession, "otra más"); again.Status != training.ProposalPending {
		t.Fatalf("after forgetting = %+v", again)
	}
}

func TestRejectAlwaysFromThePageDeniesInThatSession(t *testing.T) {
	f := newReviewFixture(t)
	p := f.propose(t, "pierna pesada")
	if w := post(t, f.handler, "/review/"+itoa(p.ID)+"/reject", url.Values{"answer": {"reject_always"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if next := f.proposeIn(t, coachSession, "otra"); next.Status != training.ProposalRejected || next.DecidedBy != training.DecidedByDenial {
		t.Fatalf("next = %+v", next)
	}
	if len(f.plans(t)) != 0 {
		t.Fatalf("plans = %+v", f.plans(t))
	}
	body := get(t, f.handler, "/review").Body.String()
	if !strings.Contains(body, "rechazar en esta conversación con el coach") || !strings.Contains(body, "rechazado siempre en esa sesión") {
		t.Fatalf("denial not shown:\n%s", body)
	}
}

// A proposal asked outside any session offers only the once answers, and an
// always answer posted for it anyway is refused.
func TestNoSessionProposalOffersOnlyOnceAnswers(t *testing.T) {
	f := newReviewFixture(t)
	p := f.proposeIn(t, "", "pierna pesada")
	body := get(t, f.handler, "/review").Body.String()
	if !strings.Contains(body, `value="allow_once"`) || strings.Contains(body, `value="allow_always"`) || strings.Contains(body, `value="reject_always"`) {
		t.Fatalf("answers offered:\n%s", body)
	}
	if home := get(t, f.handler, "/").Body.String(); !strings.Contains(home, `value="allow_once"`) || strings.Contains(home, `value="allow_always"`) {
		t.Fatalf("chat card answers:\n%s", home)
	}
	if w := post(t, f.handler, "/review/"+itoa(p.ID)+"/accept", url.Values{"answer": {"allow_always"}}, false); w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	if got, _ := f.service.GetProposal(context.Background(), p.ID); got.Status != training.ProposalPending || len(f.plans(t)) != 0 {
		t.Fatalf("proposal = %+v", got)
	}
}

// The chat cards offer the same answers as the review page, with the same labels.
func TestChatCardsOfferTheFourAnswers(t *testing.T) {
	f := newReviewFixture(t)
	// A proposed routine is a draft (planificar_test.go); any other change
	// keeps the four answers.
	payload := json.RawMessage(`{"seconds":120}`)
	if _, err := f.service.Propose(training.WithAgentSession(context.Background(), coachSession), training.ProposalInput{Tool: "set_rest_target", ActionType: "rest.set", Payload: payload},
		func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }); err != nil {
		t.Fatal(err)
	}
	body := get(t, f.handler, "/").Body.String()
	for _, want := range []string{`value="allow_once" class="primary">Aceptar una vez<`, `value="allow_always" class="ghost">Aceptar siempre en esta sesión<`,
		`value="reject_once" class="ghost">Rechazar<`, `value="reject_always" class="ghost">Rechazar siempre en esta sesión<`} {
		if !strings.Contains(body, want) {
			t.Errorf("chat card lacks %q", want)
		}
	}
}

func TestRejectingChangesNothing(t *testing.T) {
	f := newReviewFixture(t)
	p := f.propose(t, "pierna pesada")
	if w := post(t, f.handler, "/review/"+itoa(p.ID)+"/reject", nil, false); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if plans := f.plans(t); len(plans) != 0 {
		t.Fatalf("plans = %+v", plans)
	}
	body := get(t, f.handler, "/review").Body.String()
	if !strings.Contains(body, "Rechazada") {
		t.Fatalf("history lacks the rejection:\n%s", body)
	}
	// An answered proposal cannot be answered again.
	w := post(t, f.handler, "/review/"+itoa(p.ID)+"/accept", url.Values{"answer": {"allow_once"}}, false)
	if w.Code != http.StatusConflict || len(f.plans(t)) != 0 {
		t.Fatalf("second answer: %d, plans %+v", w.Code, f.plans(t))
	}
}

func TestAcceptAllSwitch(t *testing.T) {
	f := newReviewFixture(t)
	if w := post(t, f.handler, "/review/accept-all", url.Values{"on": {"1"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if perms, _ := f.service.Permissions(context.Background()); !perms.AcceptAll {
		t.Fatal("accept-all is off")
	}
	if body := get(t, f.handler, "/review").Body.String(); !strings.Contains(body, "Aceptar todo está activado") {
		t.Fatalf("the switch is not clearly shown as on:\n%s", body)
	}
	if w := post(t, f.handler, "/review/accept-all", url.Values{"on": {"0"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if perms, _ := f.service.Permissions(context.Background()); perms.AcceptAll {
		t.Fatal("accept-all is still on")
	}
}

func TestReviewRejectsBadInput(t *testing.T) {
	f := newReviewFixture(t)
	p := f.propose(t, "pierna pesada")
	for path, form := range map[string]url.Values{
		"/review/" + itoa(p.ID) + "/accept":     {"answer": {"reject_once"}},
		"/review/" + itoa(p.ID) + "/reject":     {"answer": {"allow_always"}},
		"/review/abc/accept":                    {"answer": {"allow_once"}},
		"/review/accept-all":                    {"on": {"maybe"}},
		"/review/forget":                        {"rule_id": {"abc"}},
		"/review/" + itoa(p.ID) + "/accept?old": {"remember": {"always"}},
	} {
		w := post(t, f.handler, path, form, false)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, w.Code)
		}
	}
	if got, _ := f.service.GetProposal(context.Background(), p.ID); got.Status != training.ProposalPending {
		t.Fatalf("proposal = %+v", got)
	}
}

// The index links to the review page and says how many changes wait.
func TestTodayPageLinksToPendingReview(t *testing.T) {
	f := newReviewFixture(t)
	f.propose(t, "a")
	f.propose(t, "b")
	body := get(t, f.handler, "/").Body.String()
	if !strings.Contains(body, `href="/review"`) || !strings.Contains(body, "Revisar (2)") {
		t.Fatalf("index lacks the review link with its count")
	}
}
