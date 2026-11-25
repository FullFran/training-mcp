package skills

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

const citingBody = `
## Evidence

A measured claim [ref:demo-2020].

## Heuristics

A practice.

## Person-specific assumptions

An assumption.

## Decisions

A choice.
`

const demoSources = `# Sources

### demo-2020

- Citation: Doe J. A demo study. Journal. 2020.
- DOI: 10.0000/demo
- Verified: 2026-11-25 against Crossref.
`

func demoFS(name, body, sources string) fstest.MapFS {
	front := strings.Replace(validFront, "demo-skill", name, 1)
	fsys := fstest.MapFS{
		name + "/SKILL.md": {Data: []byte("---\n" + front + "---\n" + body)},
	}
	if sources != "" {
		fsys[name+"/references/sources.md"] = &fstest.MapFile{Data: []byte(sources)}
	}
	return fsys
}

func merge(parts ...fstest.MapFS) fstest.MapFS {
	out := fstest.MapFS{}
	for _, p := range parts {
		for k, v := range p {
			out[k] = v
		}
	}
	return out
}

func TestLibraryCatalogIsSortedAndLoadReturnsReferences(t *testing.T) {
	lib, err := New(merge(demoFS("zeta-skill", citingBody, demoSources), demoFS("alpha-skill", validBody, "")))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range lib.Catalog() {
		ids = append(ids, e.ID)
	}
	if !reflect.DeepEqual(ids, []string{"alpha-skill", "zeta-skill"}) {
		t.Fatalf("catalog order = %v", ids)
	}
	s, err := lib.Load("zeta-skill")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.References) != 1 || s.References[0].Path != "references/sources.md" || s.References[0].Content != demoSources {
		t.Fatalf("references = %#v", s.References)
	}
	if !strings.Contains(s.Instructions, "[ref:demo-2020]") {
		t.Fatalf("instructions = %q", s.Instructions)
	}
}

func TestLibraryLoadRejectsUnknownID(t *testing.T) {
	lib, err := New(demoFS("alpha-skill", validBody, ""))
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.Load("beta-skill")
	if !errors.Is(err, ErrUnknownSkill) || !strings.Contains(err.Error(), "beta-skill") {
		t.Fatalf("err = %v", err)
	}
}

func TestLibraryRejectsBrokenReferences(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		sources string
		want    string
	}{
		{"cited key without entry", citingBody, "", "demo-2020"},
		{"entry without identifier", citingBody, strings.Replace(demoSources, "- DOI: 10.0000/demo\n", "", 1), "DOI or PMID"},
		{"entry never cited", validBody, demoSources, "never cited"},
		{"duplicate entry", citingBody, demoSources + "\n### demo-2020\n\n- DOI: 10.0000/other\n", "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(demoFS("alpha-skill", tc.body, tc.sources))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestLibraryRejectsAnEmptyCatalog(t *testing.T) {
	if _, err := New(fstest.MapFS{}); err == nil {
		t.Fatal("accepted a catalog with no skills")
	}
}

// The embedded packs are what production serves: they must parse, and the
// catalog must stay metadata only.
func TestEmbeddedPacks(t *testing.T) {
	lib, err := New(Packs())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range lib.Catalog() {
		ids = append(ids, e.ID)
	}
	if want := []string{"exercise-selection", "fatigue-autoregulation", "hypertrophy-programming"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("embedded skills = %v, want %v", ids, want)
	}
	for _, id := range ids {
		s, err := lib.Load(id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Version != "0.1.0" || s.ReviewStatus != ReviewDraftUnreviewed {
			t.Errorf("%s: version %q review %q; no trainer has reviewed these drafts", id, s.Version, s.ReviewStatus)
		}
		if len(s.References) == 0 {
			t.Errorf("%s has no references", id)
		}
	}
	data, err := json.Marshal(lib.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4096 {
		t.Fatalf("catalog is %d bytes; it is listed on demand and must stay small", len(data))
	}
	if Default() == nil {
		t.Fatal("Default returned nil")
	}
}

// testPrompts is the shape of packs/<id>/evals/prompts.json. The files ship in
// the repository beside each skill but are not embedded: they are fixtures for
// coach evaluations (#117), never served to an agent.
type testPrompts struct {
	Skill   string `json:"skill"`
	Version string `json:"version"`
	Cases   []struct {
		ID       string   `json:"id"`
		Kind     string   `json:"kind"`
		Context  string   `json:"context"`
		Prompt   string   `json:"prompt"`
		Expected []string `json:"expected"`
		MustNot  []string `json:"must_not"`
	} `json:"cases"`
}

func readPrompts(id string) (testPrompts, error) {
	var p testPrompts
	data, err := os.ReadFile(filepath.Join("packs", id, "evals", "prompts.json"))
	if err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return p, dec.Decode(&p)
}

// Each skill's test prompts: one per required kind, each with the behaviour
// expected and what the agent must not do.
func TestEveryPackHasItsTestPrompts(t *testing.T) {
	lib := Default()
	kinds := []string{"normal", "ambiguous", "missing-data", "conflicting-evidence", "limitation"}
	for _, e := range lib.Catalog() {
		t.Run(e.ID, func(t *testing.T) {
			p, err := readPrompts(e.ID)
			if err != nil {
				t.Fatal(err)
			}
			if p.Skill != e.ID || p.Version != e.Version {
				t.Fatalf("prompts are for %s@%s, skill is %s@%s", p.Skill, p.Version, e.ID, e.Version)
			}
			var got []string
			for _, c := range p.Cases {
				got = append(got, c.Kind)
				if strings.TrimSpace(c.Prompt) == "" || len(c.Expected) == 0 || len(c.MustNot) == 0 {
					t.Errorf("case %q lacks a prompt, expected behaviour or must_not list", c.ID)
				}
			}
			if !reflect.DeepEqual(got, kinds) {
				t.Fatalf("case kinds = %v, want %v", got, kinds)
			}
		})
	}
}
