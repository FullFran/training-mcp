package websrv_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

const testCSRF = "csrf-token-for-this-session"

// newRootServer builds the app as the login mounts it: at the root, with a CSRF
// token for every request and no database export.
func newRootServer(t *testing.T) http.Handler {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	clock := func() time.Time { return time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC) }
	services := training.NewServices(store, clock)
	service := services.ForUser(1)
	server, err := websrv.NewWithOptions(func(context.Context) *training.Service { return service }, services.Snapshot, clock, "",
		websrv.Options{CSRFToken: func(*http.Request) string { return testCSRF }})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func TestRootPagesCarryTheCSRFTokenForEveryWrite(t *testing.T) {
	h := newRootServer(t)
	for _, path := range []string{"/", "/plans"} {
		body := get(t, h, path).Body.String()
		if !strings.Contains(body, `<meta name="csrf-token" content="`+testCSRF+`">`) {
			t.Errorf("%s has no csrf meta tag", path)
		}
		// htmx inherits hx-headers, so every partial request sends the token.
		if !strings.Contains(body, `hx-headers='{"X-CSRF-Token":"`+testCSRF+`"}'`) {
			t.Errorf("%s does not send the token with htmx requests", path)
		}
	}
	// The plan form posts without JavaScript, so it carries the token itself.
	if _, err := newRootPlan(t, h); err != nil {
		t.Fatal(err)
	}
	// Signing out moved from the history page to Tú.
	you := section(t, get(t, h, "/?modo=tu").Body.String(), `id="you"`, "</section>\n\n")
	if !strings.Contains(you, `action="/logout"`) || !strings.Contains(you, `name="csrf_token" value="`+testCSRF+`"`) {
		t.Error("Tú has no logout form carrying the token")
	}
	if strings.Contains(you, "export.db") {
		t.Error("Tú links to an export the root does not serve")
	}
	if w := get(t, h, "/export.db"); w.Code != http.StatusNotFound {
		t.Errorf("export at the root: status %d", w.Code)
	}
}

// newRootPlan checks the plain plan-picking form, which only shows once a plan exists.
func newRootPlan(t *testing.T, h http.Handler) (string, error) {
	t.Helper()
	w := post(t, h, "/plans/create", map[string][]string{"name": {"Rutina"}}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("create plan: status %d", w.Code)
	}
	body := get(t, h, "/").Body.String()
	form := body[strings.Index(body, `action="/plan"`):]
	form = form[:strings.Index(form, "</form>")]
	if !strings.Contains(form, `name="csrf_token" value="`+testCSRF+`"`) {
		t.Fatalf("plan form carries no token: %s", form)
	}
	return form, nil
}

func TestSecretMountPagesAreUnchanged(t *testing.T) {
	h, _ := newServer(t)
	for _, path := range []string{base + "/", base + "/plans", base + "/progress"} {
		body := get(t, h, path).Body.String()
		if strings.Contains(body, "csrf") || strings.Contains(body, "logout") || strings.Contains(body, "hx-headers") {
			t.Errorf("%s gained login markup", path)
		}
	}
	if !strings.Contains(get(t, h, base+"/?modo=tu").Body.String(), base+"/export.db") {
		t.Error("the secret mount lost its backup link")
	}
}

// TestServiceWorkerKeepsEachUsersPagesApart runs the real worker under node with
// an in-memory Cache API, as a shared phone would run it.
func TestServiceWorkerKeepsEachUsersPagesApart(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the worker's cache rules are untested here")
	}
	h := newRootServer(t)
	sw := filepath.Join(t.TempDir(), "sw.js")
	if err := os.WriteFile(sw, get(t, h, "/sw.js").Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	harness, err := filepath.Abs("testdata/sw_harness.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, harness, sw)
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	var got map[string]struct {
		Today   *string  `json:"today"`
		History *string  `json:"history"`
		Caches  []string `json:"caches"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	str := func(p *string) string {
		if p == nil {
			return "<nothing>"
		}
		return *p
	}
	want := map[string]string{
		"switchUser":       "B today",
		"logout":           "<nothing>",
		"expired":          "<nothing>",
		"errors":           "A today",
		"malformed":        "<nothing>",
		"secretMount":      "owner today",
		"secretBesideRoot": "owner today",
		"modeQuery":        "A today",
	}
	for name, today := range want {
		if str(got[name].Today) != today {
			t.Errorf("%s: offline served %q, want %q", name, str(got[name].Today), today)
		}
	}
	if str(got["switchUser"].History) != "<nothing>" {
		t.Errorf("B offline sees A's history: %q", str(got["switchUser"].History))
	}
	for _, name := range []string{"switchUser", "logout", "expired", "malformed"} {
		for _, c := range got[name].Caches {
			if strings.Contains(c, "aaaa") || strings.Contains(c, "evil") {
				t.Errorf("%s left cache %q behind", name, c)
			}
		}
	}
}
