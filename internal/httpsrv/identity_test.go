package httpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

const staticToken = "static-token"

// identityFixture runs the real routes, tools and database, so that these tests
// fail if an identity stops reaching the data at any point on the way.
type identityFixture struct {
	*oauthFixture
	store *sqlitestore.Store
	mcp   http.Handler
}

func newIdentityFixture(t *testing.T, provision bool) *identityFixture {
	t.Helper()
	f := newOAuthFixture(t)
	store, err := sqlitestore.Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	services := training.NewServices(store, time.Now)
	mcp := mcpsrv.New(services.ForRequest).Handler()
	f.handler = RoutesWithOAuth(mcp, false, staticToken, nil, NewOAuth(OAuthConfig{
		Issuer: testIssuer, Resource: testResource, JWKSURL: f.jwks.URL, Logger: log.New(f.logs, "", 0),
		Resolve: services.ResolveUser(testSubject, provision),
	}))
	return &identityFixture{oauthFixture: f, store: store, mcp: mcp}
}

func (f *identityFixture) tokenFor(subject string) string {
	claims := goodClaims()
	claims["sub"] = subject
	return f.sign(claims)
}

func callTool(t *testing.T, handler http.Handler, token, tool string, args map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func createPlan(t *testing.T, handler http.Handler, token, name string) {
	t.Helper()
	got := callTool(t, handler, token, "create_plan", map[string]any{
		"name": name, "items": []map[string]any{{"exercise": "banca", "target_sets": 1}},
	})
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"structuredContent"`) {
		t.Fatalf("create_plan %q: status %d, body %s", name, got.Code, got.Body)
	}
}

func listPlans(t *testing.T, handler http.Handler, token string) string {
	t.Helper()
	got := callTool(t, handler, token, "list_plans", map[string]any{})
	if got.Code != http.StatusOK {
		t.Fatalf("list_plans: status %d, body %s", got.Code, got.Body)
	}
	return got.Body.String()
}

func TestEveryTokenActsAsItsOwnUser(t *testing.T) {
	f := newIdentityFixture(t, true)
	owner, guest := f.tokenFor(testSubject), f.tokenFor("user_guest")

	createPlan(t, f.handler, owner, "Owner plan")
	if got := listPlans(t, f.handler, guest); strings.Contains(got, "Owner plan") {
		t.Fatalf("the guest sees the owner's plan: %s", got)
	}
	// The guest's account was created by that call, after migration 14, so it asks
	// before applying agent writes. Accepting everything lets create_plan land.
	guestID, found, err := f.store.UserBySubject(context.Background(), "user_guest")
	if err != nil || !found {
		t.Fatalf("guest account: %v, %v", found, err)
	}
	if err := f.store.ForUser(guestID).SetAcceptAll(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	createPlan(t, f.handler, guest, "Guest plan")
	if got := listPlans(t, f.handler, owner); strings.Contains(got, "Guest plan") || !strings.Contains(got, "Owner plan") {
		t.Fatalf("owner's plans = %s", got)
	}
	// The second call must land on the account the first one created.
	if got := listPlans(t, f.handler, guest); !strings.Contains(got, "Guest plan") {
		t.Fatalf("the guest lost their plan: %s", got)
	}
}

func TestTheOwnersSubjectNeverCreatesAnAccount(t *testing.T) {
	f := newIdentityFixture(t, true)
	createPlan(t, f.handler, f.tokenFor(testSubject), "Owner plan")
	if _, ok, err := f.store.UserBySubject(context.Background(), testSubject); err != nil || ok {
		t.Fatalf("owner got a second account: ok=%v err=%v", ok, err)
	}
}

func TestStaticTokenActsAsTheOwner(t *testing.T) {
	f := newIdentityFixture(t, true)
	createPlan(t, f.handler, staticToken, "Static plan")
	if got := listPlans(t, f.handler, f.tokenFor(testSubject)); !strings.Contains(got, "Static plan") {
		t.Fatalf("owner's plans = %s", got)
	}
}

func TestRoutesWithoutOAuthActAsTheOwner(t *testing.T) {
	f := newIdentityFixture(t, false)
	createPlan(t, f.handler, staticToken, "Owner plan")
	for name, handler := range map[string]http.Handler{
		"static token":  Routes(f.mcp, false, staticToken, nil),
		"auth disabled": Routes(f.mcp, true, "", nil),
	} {
		t.Run(name, func(t *testing.T) {
			token := ""
			if name == "static token" {
				token = staticToken
			}
			if got := listPlans(t, handler, token); !strings.Contains(got, "Owner plan") {
				t.Fatalf("plans = %s", got)
			}
		})
	}
}

func TestWebUIActsAsTheOwner(t *testing.T) {
	seen := int64(0)
	web := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen, _ = training.UserFrom(r.Context()) })
	handler := Routes(http.NotFoundHandler(), false, staticToken, &WebUI{Handler: web, BasePath: "/app"})
	r := httptest.NewRequest(http.MethodGet, "/app/", nil)
	r.Header.Set("Authorization", "Bearer "+staticToken)
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if seen != training.OwnerUserID {
		t.Fatalf("web acts as user %d", seen)
	}
}

func TestUnknownSubjectIsRefusedUnlessProvisioningIsOn(t *testing.T) {
	f := newIdentityFixture(t, false)
	got := callTool(t, f.handler, f.tokenFor("user_stranger"), "list_plans", map[string]any{})
	if got.Code != http.StatusUnauthorized || !strings.Contains(got.Header().Get("WWW-Authenticate"), "invalid_token") {
		t.Fatalf("status = %d, challenge = %q", got.Code, got.Header().Get("WWW-Authenticate"))
	}
	if _, ok, err := f.store.UserBySubject(context.Background(), "user_stranger"); err != nil || ok {
		t.Fatalf("a refused token created an account: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(f.logs.String(), "user_stranger") {
		t.Fatalf("the refusal was not logged: %s", f.logs)
	}
}

func TestExistingAccountStillWorksWithProvisioningOff(t *testing.T) {
	f := newIdentityFixture(t, false)
	if _, err := f.store.ProvisionUser(context.Background(), "user_guest"); err != nil {
		t.Fatal(err)
	}
	guest := f.tokenFor("user_guest")
	createPlan(t, f.handler, guest, "Guest plan")
	if got := listPlans(t, f.handler, staticToken); strings.Contains(got, "Guest plan") {
		t.Fatalf("the guest wrote as the owner: %s", got)
	}
}

func TestTokenWithoutASubjectNeverGetsAnAccount(t *testing.T) {
	f := newIdentityFixture(t, true)
	missing := goodClaims()
	delete(missing, "sub")
	for name, token := range map[string]string{"no sub": f.sign(missing), "empty sub": f.tokenFor("")} {
		t.Run(name, func(t *testing.T) {
			if got := callTool(t, f.handler, token, "list_plans", map[string]any{}); got.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body %s", got.Code, got.Body)
			}
		})
	}
	if _, ok, err := f.store.UserBySubject(context.Background(), ""); err != nil || ok {
		t.Fatalf("an empty subject got an account: ok=%v err=%v", ok, err)
	}
}

// A failure looking the user up says nothing about the token, so it must not tell
// the client to log in again.
func TestFailureResolvingTheUserIsNotAnAuthFailure(t *testing.T) {
	f := newOAuthFixture(t)
	f.handler = RoutesWithOAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { f.reached.Store(true) }), false, staticToken, nil, NewOAuth(OAuthConfig{
		Issuer: testIssuer, Resource: testResource, JWKSURL: f.jwks.URL, Logger: log.New(f.logs, "", 0),
		Resolve: func(context.Context, string) (int64, bool, error) { return 0, false, errors.New("database locked") },
	}))
	got := f.post("/mcp", f.sign(goodClaims()))
	if got.Code != http.StatusServiceUnavailable || f.reached.Load() || got.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("status = %d, reached = %v, challenge = %q", got.Code, f.reached.Load(), got.Header().Get("WWW-Authenticate"))
	}
	if strings.Contains(got.Body.String(), "database locked") {
		t.Fatalf("the cause reached the client: %s", got.Body)
	}
	if !strings.Contains(f.logs.String(), "database locked") {
		t.Fatalf("the cause was not logged: %s", f.logs)
	}
}

func TestBrokenDatabaseIsNotAnAuthFailure(t *testing.T) {
	f := newIdentityFixture(t, true)
	f.store.Close()
	got := callTool(t, f.handler, f.tokenFor("user_guest"), "list_plans", map[string]any{})
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body %s", got.Code, got.Body)
	}
}

func TestRequestWithoutAnIdentitySeesNobodysData(t *testing.T) {
	f := newIdentityFixture(t, false)
	createPlan(t, f.handler, staticToken, "Owner plan")
	services := training.NewServices(f.store, time.Now)
	plans, err := services.ForRequest(context.Background()).ListPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Fatalf("a request with no identity read %d plans", len(plans))
	}
}

// Subjects are compared byte for byte: a near match must never reach someone else's
// data, least of all the owner's.
func TestSubjectsMatchExactly(t *testing.T) {
	f := newIdentityFixture(t, true)
	createPlan(t, f.handler, f.tokenFor(testSubject), "Owner plan")
	for _, near := range []string{" " + testSubject, testSubject + " ", strings.ToUpper(testSubject)} {
		if got := listPlans(t, f.handler, f.tokenFor(near)); strings.Contains(got, "Owner plan") {
			t.Fatalf("%q reads the owner's plans: %s", near, got)
		}
	}
	createPlan(t, f.handler, f.tokenFor("user_guest"), "Guest plan")
	if got := listPlans(t, f.handler, f.tokenFor("USER_GUEST")); strings.Contains(got, "Guest plan") {
		t.Fatalf("a case variant reads the guest's plans: %s", got)
	}
}

// Moving the owner to a subject that already has an account must still give them
// their own data.
func TestConfiguredOwnerWinsOverAStoredAccount(t *testing.T) {
	f := newIdentityFixture(t, true)
	createPlan(t, f.handler, staticToken, "Owner plan")
	if _, err := f.store.ProvisionUser(context.Background(), testSubject); err != nil {
		t.Fatal(err)
	}
	if got := listPlans(t, f.handler, f.tokenFor(testSubject)); !strings.Contains(got, "Owner plan") {
		t.Fatalf("owner's plans = %s", got)
	}
}
