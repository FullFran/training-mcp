package connector

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

func TestPiToolNameMatchesPi(t *testing.T) {
	// Produced by @earendil-works/pi-mcp 1.5.2 createMcpToolName.
	for _, c := range [][3]string{
		{"notion", "notion-search", "mcp__notion__notion_search"},
		{"notion", "create-page", "mcp__notion__create_page"},
		{"notion", strings.Repeat("a", 70), "mcp__notion__aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_3d595d37"},
		{"gh", "emoji😀tool", "mcp__gh__emoji__tool"},
		{"notion", "Fetch.Page v2", "mcp__notion__Fetch_Page_v2"},
	} {
		if got := PiToolName(c[0], c[1]); got != c[2] {
			t.Errorf("PiToolName(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

type fixture struct {
	t       *testing.T
	store   *sqlitestore.Store
	vault   *engine.Vault
	svc     *Service
	a, b    int64
	mu      sync.Mutex
	reloads map[int64]int
}

func newFixture(t *testing.T, f *fakeRemote) *fixture {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlitestore.Open(filepath.Join(dir, "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	key := make([]byte, 32)
	vault, err := engine.NewVault(filepath.Join(dir, "credentials"), key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, _ := store.ProvisionUser(ctx, "user_a")
	b, _ := store.ProvisionUser(ctx, "user_b")
	fx := &fixture{t: t, store: store, vault: vault, a: a, b: b, reloads: map[int64]int{}}
	g := testGuard(f.srv)
	fx.svc = &Service{
		Store: store, Secrets: vault, Guard: g,
		OAuth:  &OAuthClient{Guard: g, RedirectURL: "https://app.example.test/coach/connectors/callback", ClientName: "training-mcp"},
		Reload: func(u int64) { fx.mu.Lock(); fx.reloads[u]++; fx.mu.Unlock() },
	}
	return fx
}

// connectOAuth adds the fake server for a person and signs in to the end.
func (fx *fixture) connectOAuth(f *fakeRemote, userID int64) View {
	fx.t.Helper()
	ctx := context.Background()
	v, err := fx.svc.Add(ctx, userID, f.URL(), "Notion", "")
	if err != nil {
		fx.t.Fatal(err)
	}
	res, err := fx.svc.Connect(ctx, userID, v.ID)
	if err != nil || res.State != "authorize" {
		fx.t.Fatalf("connect = %+v %v", res, err)
	}
	code, state := f.approve(res.AuthorizeURL)
	if _, err := fx.svc.Callback(ctx, userID, state, code); err != nil {
		fx.t.Fatal(err)
	}
	return v
}

func TestOAuthConnectorEndToEndForOnePerson(t *testing.T) {
	f := newFakeRemote(t)
	fx := newFixture(t, f)
	ctx := context.Background()
	v, err := fx.svc.Add(ctx, fx.a, f.URL(), "Notion", "")
	if err != nil || v.Name != "notion" || v.State != training.ConnectorDisconnected || v.Auth != training.ConnectorAuthOAuth {
		t.Fatalf("add = %+v %v", v, err)
	}
	res, err := fx.svc.Connect(ctx, fx.a, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(res.AuthorizeURL)
	if u.Query().Get("redirect_uri") != "https://app.example.test/coach/connectors/callback" || u.Query().Get("code_challenge") == "" {
		t.Fatalf("authorize URL = %s", res.AuthorizeURL)
	}
	code, state := f.approve(res.AuthorizeURL)

	// Another person cannot finish this sign-in, and trying does not spend it.
	if _, err := fx.svc.Callback(ctx, fx.b, state, code); CodeOf(err) != "state_invalid" {
		t.Fatalf("other person's callback = %v", err)
	}
	if _, err := fx.svc.Callback(ctx, fx.a, state, code); err != nil {
		t.Fatal(err)
	}
	// Single use.
	if _, err := fx.svc.Callback(ctx, fx.a, state, code); CodeOf(err) != "state_invalid" {
		t.Fatalf("reused state = %v", err)
	}
	list, _ := fx.svc.List(ctx, fx.a)
	if len(list) != 1 || list[0].State != training.ConnectorConnected {
		t.Fatalf("list = %+v", list)
	}
	if fx.reloads[fx.a] == 0 || fx.reloads[fx.b] != 0 {
		t.Fatalf("reloads = %v", fx.reloads)
	}
	tools, err := fx.svc.Tools(ctx, fx.a, v.ID)
	if err != nil || len(tools) != 2 || tools[1].Model != "mcp__notion__create_page" {
		t.Fatalf("tools = %+v %v", tools, err)
	}
	var sec secret
	if ok, err := fx.vault.ReadSecret(fx.a, secretName(v.ID), &sec); !ok || err != nil || sec.Tokens == nil || sec.Client == nil {
		t.Fatalf("vault = %v %v %+v", ok, err, sec)
	}

	// The other person sees and reaches nothing of it.
	if l, _ := fx.svc.List(ctx, fx.b); len(l) != 0 {
		t.Fatalf("other person's list = %+v", l)
	}
	for name, err := range map[string]error{
		"tools":      second(fx.svc.Tools(ctx, fx.b, v.ID)),
		"connect":    second(fx.svc.Connect(ctx, fx.b, v.ID)),
		"disconnect": fx.svc.Disconnect(ctx, fx.b, v.ID),
		"remove":     fx.svc.Remove(ctx, fx.b, v.ID),
	} {
		if CodeOf(err) != "not_found" {
			t.Errorf("other person's %s = %v", name, err)
		}
	}
	if len(fx.svc.ForEngine(fx.b)) != 0 || len(fx.svc.ForEngine(fx.a)) != 1 {
		t.Fatal("ForEngine leaks across people")
	}
	if ok, _ := fx.vault.ReadSecret(fx.b, secretName(v.ID), &sec); ok {
		t.Fatal("the other person reads the secret")
	}

	if err := fx.svc.Disconnect(ctx, fx.a, v.ID); err != nil {
		t.Fatal(err)
	}
	sec = secret{}
	fx.vault.ReadSecret(fx.a, secretName(v.ID), &sec)
	if sec.Tokens != nil || sec.Client == nil {
		t.Fatalf("after disconnect = %+v", sec)
	}
	if len(fx.svc.ForEngine(fx.a)) != 0 {
		t.Fatal("a disconnected server is still given to the engine")
	}
	if err := fx.svc.Remove(ctx, fx.a, v.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fx.vault.ReadSecret(fx.a, secretName(v.ID), &sec); ok {
		t.Fatal("secret left after remove")
	}
}

func second[T any](_ T, err error) error { return err }

func TestExpiredSignInIsRefused(t *testing.T) {
	f := newFakeRemote(t)
	fx := newFixture(t, f)
	now := time.Now()
	fx.svc.Now = func() time.Time { return now }
	ctx := context.Background()
	v, _ := fx.svc.Add(ctx, fx.a, f.URL(), "Notion", "")
	res, _ := fx.svc.Connect(ctx, fx.a, v.ID)
	code, state := f.approve(res.AuthorizeURL)
	now = now.Add(flowTTL + time.Second)
	if _, err := fx.svc.Callback(ctx, fx.a, state, code); CodeOf(err) != "state_invalid" {
		t.Fatalf("expired = %v", err)
	}
}

func TestStaticTokenAndOpenServers(t *testing.T) {
	f := newFakeRemote(t)
	f.Static = "static-secret"
	fx := newFixture(t, f)
	ctx := context.Background()
	v, err := fx.svc.Add(ctx, fx.a, f.URL(), "Docs", "static-secret")
	if err != nil || v.Auth != training.ConnectorAuthBearer {
		t.Fatalf("add = %+v %v", v, err)
	}
	if res, err := fx.svc.Connect(ctx, fx.a, v.ID); err != nil || res.State != training.ConnectorConnected {
		t.Fatalf("connect = %+v %v", res, err)
	}
	wrong, _ := fx.svc.Add(ctx, fx.a, f.URL(), "Docs2", "wrong")
	if _, err := fx.svc.Connect(ctx, fx.a, wrong.ID); CodeOf(err) != "bad_token" {
		t.Fatalf("wrong token = %v", err)
	}
	// The token never sits in the record.
	c, _ := fx.store.Connector(ctx, fx.a, v.ID)
	if strings.Contains(c.URL+c.Label+c.Slug, "static-secret") {
		t.Fatal("token in the record")
	}

	open := newFakeRemote(t)
	open.Open = true
	fx2 := newFixture(t, open)
	ov, _ := fx2.svc.Add(ctx, fx2.a, open.URL(), "", "")
	if res, err := fx2.svc.Connect(ctx, fx2.a, ov.ID); err != nil || res.State != training.ConnectorConnected {
		t.Fatalf("open = %+v %v", res, err)
	}
	if c, _ := fx2.store.Connector(ctx, fx2.a, ov.ID); c.Auth != training.ConnectorAuthNone {
		t.Fatalf("open auth = %q", c.Auth)
	}
}

func TestAddRefusesWhatTheGuardRefuses(t *testing.T) {
	f := newFakeRemote(t)
	fx := newFixture(t, f)
	fx.svc.Guard = &Guard{} // the strict one
	ctx := context.Background()
	for raw, code := range map[string]string{
		"https://127.0.0.1/mcp":            "blocked_address",
		"https://169.254.169.254/mcp":      "blocked_address",
		"https://localhost/mcp":            "blocked_address",
		"http://mcp.notion.com/mcp":        "bad_url",
		"https://user:pw@mcp.example/mcp":  "bad_url",
		"https://[fd00::1]/mcp":            "blocked_address",
		"https://metadata.google.internal": "blocked_address",
	} {
		if _, err := fx.svc.Add(ctx, fx.a, raw, "x", ""); CodeOf(err) != code {
			t.Errorf("%s = %v, want %s", raw, err, code)
		}
	}
	if _, err := fx.svc.Add(ctx, fx.a, f.URL(), "training", ""); err == nil {
		// Under the strict guard the fake is refused before the name; with the
		// test guard the reserved name is.
		t.Fatal("accepted")
	}
	fx.svc.Guard = testGuard(f.srv)
	if _, err := fx.svc.Add(ctx, fx.a, f.URL(), "training", ""); CodeOf(err) != "bad_label" {
		t.Fatalf("reserved name = %v", err)
	}
}

func TestOAuthNeedsAPublicURL(t *testing.T) {
	f := newFakeRemote(t)
	fx := newFixture(t, f)
	fx.svc.OAuth = nil
	v, _ := fx.svc.Add(context.Background(), fx.a, f.URL(), "Notion", "")
	if _, err := fx.svc.Connect(context.Background(), fx.a, v.ID); CodeOf(err) != "oauth_unavailable" {
		t.Fatalf("no public URL = %v", err)
	}
}
