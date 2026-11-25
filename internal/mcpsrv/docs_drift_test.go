package mcpsrv

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// registeredTools is the tool surface as a client sees it, read at test time,
// so a tool added anywhere is checked without editing this file.
func registeredTools(t *testing.T) []string {
	t.Helper()
	session := mustConnect(t, New(only(training.NewService(testStore{}, nil))).Handler())
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return sortedToolNames(result)
}

var toolRow = regexp.MustCompile("^\\|\\s*`([a-z0-9_]+)`\\s*\\|")

// documentedTools reads the tool names in the first column of every table of
// the reference whose header's first cell is "Tool". Other tables (answers,
// approval modes, action types) name things that are not tools.
func documentedTools(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	inToolTable := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "|") {
			inToolTable = false
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) > 1 && strings.TrimSpace(cells[1]) == "Tool" {
			inToolTable = true
			continue
		}
		if m := toolRow.FindStringSubmatch(line); inToolTable && m != nil {
			out = append(out, m[1])
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func missingFrom(want, have []string) []string {
	set := make(map[string]bool, len(have))
	for _, v := range have {
		set[v] = true
	}
	var out []string
	for _, v := range want {
		if !set[v] {
			out = append(out, v)
		}
	}
	return out
}

// The tool reference lists every registered tool in one of its tool tables,
// and no tool that is not registered.
func TestToolReferenceListsExactlyTheRegisteredTools(t *testing.T) {
	registered := registeredTools(t)
	documented := documentedTools(t, filepath.Join("..", "..", "docs", "tools.md"))
	if len(documented) == 0 {
		t.Fatal("no tool rows found in docs/tools.md; did the table header change?")
	}
	if missing := missingFrom(registered, documented); len(missing) > 0 {
		t.Errorf("registered tools missing from docs/tools.md: %v", missing)
	}
	if extra := missingFrom(documented, registered); len(extra) > 0 {
		t.Errorf("docs/tools.md documents tools that are not registered: %v", extra)
	}
}

var toolCount = regexp.MustCompile(`\b(\d+) (?:MCP )?tools\b`)

// A document that states how many tools there are states the registered
// number. Wording without a number is preferred, since it cannot go stale.
func TestDocumentedToolCountsMatchTheRegistry(t *testing.T) {
	want := len(registeredTools(t))
	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "..", "README.md"))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range toolCount.FindAllStringSubmatch(line, -1) {
				if n, _ := strconv.Atoi(m[1]); n != want {
					t.Errorf("%s:%d says %q, but %d tools are registered", path, i+1, m[0], want)
				}
			}
		}
	}
}
