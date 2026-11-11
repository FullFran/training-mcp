package connector

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

func TestDescriptorIsServerJSONWithoutSecretValues(t *testing.T) {
	f := newFakeRemote(t)
	f.Static = "static-secret"
	fx := newFixture(t, f)
	ctx := context.Background()
	v, err := fx.svc.Add(ctx, fx.a, f.URL(), "Notion", "static-secret")
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.svc.Descriptor(ctx, fx.a, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(d)
	doc := string(raw)
	if strings.Contains(doc, "static-secret") {
		t.Fatal("a secret in the descriptor")
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	if m["$schema"] != ServerSchema || m["version"] == "" || m["description"] == "" {
		t.Fatalf("descriptor = %s", doc)
	}
	// server.json 2025-12-11: name is a reverse-DNS namespace and one slash.
	if !regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`).MatchString(d.Name) || !strings.HasSuffix(d.Name, "/notion") {
		t.Fatalf("name = %q", d.Name)
	}
	remote := m["remotes"].([]any)[0].(map[string]any)
	if remote["type"] != "streamable-http" || remote["url"] != f.URL() {
		t.Fatalf("remote = %v", remote)
	}
	header := remote["headers"].([]any)[0].(map[string]any)
	if header["isSecret"] != true || header["name"] != "Authorization" {
		t.Fatalf("header = %v", header)
	}
	// HCS-0008-R011: a secret input has no value and no default.
	if _, ok := header["value"]; ok {
		t.Fatal("secret input with a value")
	}
	if _, ok := header["default"]; ok {
		t.Fatal("secret input with a default")
	}
	if _, err := fx.svc.Descriptor(ctx, fx.b, v.ID); CodeOf(err) != "not_found" {
		t.Fatalf("other person's descriptor = %v", err)
	}
	open := Describe(training.Connector{Slug: "docs", URL: "https://docs.example.com/mcp", Auth: training.ConnectorAuthNone})
	if len(open.Remotes[0].Headers) != 0 || open.Name != "com.example.docs/docs" {
		t.Fatalf("open = %+v", open)
	}
}
