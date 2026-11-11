package connector

import (
	"context"
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fullfran/training-mcp/internal/training"
)

func TestBuiltinCatalogIsValid(t *testing.T) {
	entries, err := BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("empty catalog")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.ID] {
			t.Errorf("duplicate id %s", e.ID)
		}
		seen[e.ID] = true
		if !strings.HasPrefix(e.URL, "https://") {
			t.Errorf("%s: %s is not https", e.ID, e.URL)
		}
		if e.Title == "" || e.Icon == "" || e.Summary == "" || e.Category == "" || e.CategoryLabel == "" {
			t.Errorf("%s: missing display metadata: %+v", e.ID, e)
		}
	}
	// No file of the catalog carries a value for any input, nor anything that
	// looks like a credential.
	err = fs.WalkDir(catalogFS, "catalog", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(catalogFS, p)
		var doc any
		if json.Unmarshal(b, &doc) != nil {
			t.Errorf("%s is not JSON", p)
			return nil
		}
		walkJSON(doc, func(key string, v any) {
			switch strings.ToLower(key) {
			case "value", "default", "token", "secret", "password", "apikey", "api_key", "client_secret":
				t.Errorf("%s carries %q", p, key)
			}
		})
		if m := credentialShape.FindString(string(b)); m != "" {
			t.Errorf("%s contains something shaped like a credential", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// credentialShape is a bearer value or a well-known token prefix.
var credentialShape = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]{16,}|\b(sk|ghp|gho|xoxb|xoxp|lin_api|ntn|secret)_?-?[a-z0-9]{12,}`)

func TestCatalogIsGroupedByCategoryInDeclaredOrder(t *testing.T) {
	two := strings.Replace(goodCatalog, `]}`,
		`, {"id": "agenda", "server": "servers/agenda.server.json", "icon": "📅", "category": "calendar", "summary": "Agenda."},
		   {"id": "example2", "server": "servers/example2.server.json", "icon": "📝", "category": "notes", "summary": "Más notas."}]}`, 1)
	fsys := catalogFSWith(two, goodServer)
	fsys["servers/agenda.server.json"] = &fstest.MapFile{Data: []byte(strings.Replace(goodServer, `"title": "Example"`, `"title": "Agenda"`, 1))}
	fsys["servers/example2.server.json"] = &fstest.MapFile{Data: []byte(strings.Replace(goodServer, `"title": "Example"`, `"title": "Example2"`, 1))}
	entries, err := LoadCatalog(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Category+":"+e.CategoryLabel+":"+e.ID)
	}
	want := "notes:Notas:example notes:Notas:example2 calendar:Calendario:agenda"
	if strings.Join(got, " ") != want {
		t.Fatalf("order = %v, want %s", got, want)
	}
}

func walkJSON(v any, f func(key string, v any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			f(k, c)
			walkJSON(c, f)
		}
	case []any:
		for _, c := range x {
			walkJSON(c, f)
		}
	}
}

const goodServer = `{
  "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
  "name": "com.example/mcp",
  "title": "Example",
  "description": "An example server.",
  "version": "1.0.0",
  "remotes": [{"type": "streamable-http", "url": "https://mcp.example.com/mcp",
    "headers": [{"name": "Authorization", "description": "Bearer token held by the host.", "isRequired": true, "isSecret": true}]}]
}`

const goodCatalog = `{"id": "dev.test.connectors", "version": "1",
 "categories": [{"id": "notes", "label": "Notas"}, {"id": "calendar", "label": "Calendario"}],
 "entries": [
  {"id": "example", "server": "servers/example.server.json", "icon": "📝", "category": "notes", "summary": "Notas."}
]}`

func catalogFSWith(catalog, server string) fstest.MapFS {
	return fstest.MapFS{
		"catalog.json":                {Data: []byte(catalog)},
		"servers/example.server.json": {Data: []byte(server)},
	}
}

func TestLoadCatalogRefusesBadDocuments(t *testing.T) {
	if _, err := LoadCatalog(catalogFSWith(goodCatalog, goodServer)); err != nil {
		t.Fatalf("good catalog: %v", err)
	}
	server := func(old, new string) string { return strings.Replace(goodServer, old, new, 1) }
	catalog := func(old, new string) string { return strings.Replace(goodCatalog, old, new, 1) }
	cases := map[string]fstest.MapFS{
		"http url":          catalogFSWith(goodCatalog, server("https://mcp.example.com/mcp", "http://mcp.example.com/mcp")),
		"private address":   catalogFSWith(goodCatalog, server("https://mcp.example.com/mcp", "https://10.0.0.1/mcp")),
		"localhost":         catalogFSWith(goodCatalog, server("https://mcp.example.com/mcp", "https://localhost/mcp")),
		"internal name":     catalogFSWith(goodCatalog, server("https://mcp.example.com/mcp", "https://mcp.corp/mcp")),
		"user info":         catalogFSWith(goodCatalog, server("https://mcp.example.com/mcp", "https://u:p@mcp.example.com/mcp")),
		"secret value":      catalogFSWith(goodCatalog, server(`"isSecret": true}`, `"isSecret": true, "value": "Bearer abc"}`)),
		"secret default":    catalogFSWith(goodCatalog, server(`"isSecret": true}`, `"isSecret": true, "default": "abc"}`)),
		"header not secret": catalogFSWith(goodCatalog, server(`"isSecret": true`, `"isSecret": false`)),
		"wrong schema":      catalogFSWith(goodCatalog, server("2025-12-11", "2024-01-01")),
		"stdio transport":   catalogFSWith(goodCatalog, server(`"streamable-http"`, `"stdio"`)),
		"no title":          catalogFSWith(goodCatalog, server(`"title": "Example",`, "")),
		"title not id":      catalogFSWith(goodCatalog, server(`"title": "Example"`, `"title": "Other"`)),
		"no icon":           catalogFSWith(catalog(`"icon": "📝"`, `"icon": ""`), goodServer),
		"no summary":        catalogFSWith(catalog(`"summary": "Notas."`, `"summary": ""`), goodServer),
		"unknown category":  catalogFSWith(catalog(`"notes"`, `"games"`), goodServer),
		"escaping path":     catalogFSWith(catalog(`servers/example.server.json`, `../example.server.json`), goodServer),
		"missing server":    catalogFSWith(catalog(`servers/example.server.json`, `servers/none.server.json`), goodServer),
		"bad id":            catalogFSWith(catalog(`"id": "example"`, `"id": "Ex ample"`), goodServer),
		"no entries":        catalogFSWith(`{"id": "dev.test.connectors", "version": "1", "entries": []}`, goodServer),
		"no version":        catalogFSWith(catalog(`"version": "1"`, `"version": ""`), goodServer),
		"unknown field":     catalogFSWith(catalog(`"icon"`, `"token": "x", "icon"`), goodServer),
		"duplicate id": catalogFSWith(strings.Replace(goodCatalog, `]}`,
			`, {"id": "example", "server": "servers/example.server.json", "icon": "📝", "category": "notes", "summary": "Otra."}]}`, 1), goodServer),
	}
	for name, fsys := range cases {
		if _, err := LoadCatalog(fsys); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// catalogFixture serves the fake remote as the one catalog entry.
func catalogFixture(t *testing.T) (*fixture, *fakeRemote) {
	f := newFakeRemote(t)
	fx := newFixture(t, f)
	fx.svc.CatalogEntries = []CatalogEntry{{ID: "notion", Title: "Notion", Icon: "📝", Category: "notes", CategoryLabel: "Notas", Summary: "Notas.", URL: f.URL()}}
	return fx, f
}

func TestOneTapCreatesTheSameConnectorAsTheManualPath(t *testing.T) {
	fx, f := catalogFixture(t)
	ctx := context.Background()
	tapped, err := fx.svc.AddFromCatalog(ctx, fx.a, "notion")
	if err != nil {
		t.Fatal(err)
	}
	manual, err := fx.svc.Add(ctx, fx.b, f.URL(), "Notion", "")
	if err != nil {
		t.Fatal(err)
	}
	tapped.ID, manual.ID = 0, 0
	if tapped != manual {
		t.Fatalf("one tap = %+v, manual = %+v", tapped, manual)
	}
	ta, _ := fx.store.ListConnectors(ctx, fx.a)
	tb, _ := fx.store.ListConnectors(ctx, fx.b)
	if len(ta) != 1 || len(tb) != 1 {
		t.Fatalf("records = %+v %+v", ta, tb)
	}
	da, db := Describe(ta[0]), Describe(tb[0])
	ja, _ := json.Marshal(da)
	jb, _ := json.Marshal(db)
	if string(ja) != string(jb) {
		t.Fatalf("server.json differs:\n%s\n%s", ja, jb)
	}
	// It goes on into the same OAuth sign-in.
	res, err := fx.svc.Connect(ctx, fx.a, ta[0].ID)
	if err != nil || res.State != "authorize" || res.AuthorizeURL == "" {
		t.Fatalf("connect = %+v %v", res, err)
	}
	// Tapping again does not make a second one.
	if _, err := fx.svc.AddFromCatalog(ctx, fx.a, "notion"); CodeOf(err) != "exists" {
		t.Fatalf("second tap = %v", err)
	}
	if _, err := fx.svc.AddFromCatalog(ctx, fx.a, "nope"); CodeOf(err) != "not_found" {
		t.Fatalf("unknown entry = %v", err)
	}
}

func TestCatalogShowsEachPersonOnlyTheirOwnState(t *testing.T) {
	fx, f := catalogFixture(t)
	ctx := context.Background()
	fx.connectOAuth(f, fx.a)
	ca, err := fx.svc.Catalog(ctx, fx.a)
	if err != nil {
		t.Fatal(err)
	}
	if len(ca) != 1 || ca[0].Connector == nil || ca[0].Connector.State != training.ConnectorConnected {
		t.Fatalf("a's catalog = %+v", ca)
	}
	cb, err := fx.svc.Catalog(ctx, fx.b)
	if err != nil {
		t.Fatal(err)
	}
	if len(cb) != 1 || cb[0].Connector != nil {
		t.Fatalf("b's catalog = %+v", cb)
	}
	// b's tap makes b's own connector and leaves a's alone.
	v, err := fx.svc.AddFromCatalog(ctx, fx.b, "notion")
	if err != nil || v.State != training.ConnectorDisconnected {
		t.Fatalf("b's tap = %+v %v", v, err)
	}
	if ca, _ := fx.svc.Catalog(ctx, fx.a); ca[0].Connector.State != training.ConnectorConnected || ca[0].Connector.ID == v.ID {
		t.Fatalf("a's catalog after b's tap = %+v", ca[0].Connector)
	}
}

func TestCatalogURLsStillGoThroughTheGuard(t *testing.T) {
	fx, _ := catalogFixture(t)
	fx.svc.Guard = &Guard{} // the strict one: the loopback fake is refused
	fx.svc.CatalogEntries = append(fx.svc.CatalogEntries,
		CatalogEntry{ID: "metadata", Title: "Metadata", Icon: "x", Category: "notes", CategoryLabel: "Notas", Summary: "x", URL: "https://169.254.169.254/mcp"})
	ctx := context.Background()
	for _, id := range []string{"notion", "metadata"} {
		if _, err := fx.svc.AddFromCatalog(ctx, fx.a, id); CodeOf(err) != "blocked_address" {
			t.Errorf("%s = %v", id, err)
		}
	}
	if l, _ := fx.store.ListConnectors(ctx, fx.a); len(l) != 0 {
		t.Fatalf("records = %+v", l)
	}
}
