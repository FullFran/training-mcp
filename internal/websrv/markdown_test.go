package websrv_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// renderMarkdown runs static/markdown.js under node against a document that
// throws on any HTML parsing and on any element outside the subset.
func renderMarkdown(t *testing.T, inputs ...string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the reply renderer is untested here")
	}
	script, err := filepath.Abs("static/markdown.js")
	if err != nil {
		t.Fatal(err)
	}
	harness, err := filepath.Abs("testdata/markdown_harness.mjs")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(inputs)
	cmd := exec.Command(node, harness, script, string(args))
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	var trees []json.RawMessage
	if err := json.Unmarshal(out, &trees); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := make([]string, len(trees))
	for i, tree := range trees {
		got[i] = string(tree)
	}
	return got
}

// The coach's replies render a small Markdown subset as elements.
func TestReplyMarkdownSubset(t *testing.T) {
	cases := map[string]string{
		"una rutina **nueva** y *ligera*":      `[{"tag":"p","children":[{"text":"una rutina "},{"tag":"strong","children":[{"text":"nueva"}]},{"text":" y "},{"tag":"em","children":[{"text":"ligera"}]}]}]`,
		"línea uno\nlínea dos\n\notro párrafo": `[{"tag":"p","children":[{"text":"línea uno"},{"tag":"br","children":[]},{"text":"línea dos"}]},{"tag":"p","children":[{"text":"otro párrafo"}]}]`,
		"- banca 3×8\n- fondos":                `[{"tag":"ul","children":[{"tag":"li","children":[{"text":"banca 3×8"}]},{"tag":"li","children":[{"text":"fondos"}]}]}]`,
		"1. calienta\n2. **series**":           `[{"tag":"ol","children":[{"tag":"li","children":[{"text":"calienta"}]},{"tag":"li","children":[{"tag":"strong","children":[{"text":"series"}]}]}]}]`,
		"usa `live_workout`":                   `[{"tag":"p","children":[{"text":"usa "},{"tag":"code","children":[{"text":"live_workout"}]}]}]`,
		"```\na *b*\n```":                      `[{"tag":"pre","children":[{"tag":"code","children":[{"text":"a *b*"}]}]}]`,
		"3 * 4 * 5 y list_plans_x":             `[{"tag":"p","children":[{"text":"3 * 4 * 5 y list_plans_x"}]}]`,
	}
	inputs := make([]string, 0, len(cases))
	for in := range cases {
		inputs = append(inputs, in)
	}
	for i, got := range renderMarkdown(t, inputs...) {
		if want := cases[inputs[i]]; got != want {
			t.Errorf("%q\n got %s\nwant %s", inputs[i], got, want)
		}
	}
}

// Markup in a reply stays text: no element is ever made from it, and the
// renderer never parses HTML (the harness throws if it tries).
func TestReplyMarkupStaysInert(t *testing.T) {
	reply := "<script>alert(1)</script> <img src=x onerror=alert(1)> **<b>hola</b>**\n- <a href=\"javascript:alert(1)\">x</a>\n```\n<iframe src=//evil>\n```"
	got := renderMarkdown(t, reply)[0]
	for _, want := range []string{
		`{"text":"<script>alert(1)</script> <img src=x onerror=alert(1)> "}`,
		`{"tag":"strong","children":[{"text":"<b>hola</b>"}]}`,
		`{"tag":"li","children":[{"text":"<a href=\"javascript:alert(1)\">x</a>"}]}`,
		`{"tag":"code","children":[{"text":"<iframe src=//evil>"}]}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply rendered as %s\nmissing %s", got, want)
		}
	}
	for _, tag := range []string{`"tag":"script"`, `"tag":"img"`, `"tag":"a"`, `"tag":"b"`, `"tag":"iframe"`} {
		if strings.Contains(got, tag) {
			t.Fatalf("reply made an element %s: %s", tag, got)
		}
	}
	for _, file := range []string{"static/markdown.js", "static/coach.js"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, sink := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write"} {
			if strings.Contains(string(src), sink) {
				t.Fatalf("%s uses %s", file, sink)
			}
		}
	}
}
