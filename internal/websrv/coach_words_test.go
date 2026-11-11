package websrv

import (
	"context"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/training"
)

// Every tool the coach can call shows a Spanish label in the chat's steps
// row, not its raw name.
func TestEveryMCPToolHasASpanishStepLabel(t *testing.T) {
	server := httptest.NewServer(mcpsrv.New(func(context.Context) *training.Service { return nil }).Handler())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "words", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	script, err := os.ReadFile("static/coach.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		seen++
		entry := regexp.MustCompile(`(?m)^    ` + regexp.QuoteMeta(tool.Name) + `: \['[^']+', '[^']+'\],$`)
		if !entry.Match(script) {
			t.Errorf("tool %s has no Spanish label in coach.js WORDS", tool.Name)
		}
	}
	if seen < 40 {
		t.Fatalf("listed only %d tools", seen)
	}
}
