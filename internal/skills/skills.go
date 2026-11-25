// Package skills serves training-theory skills on demand: a small catalog of
// metadata, and one skill's full instructions when an agent asks for it.
//
// A skill is a directory in the Agent Skills format (a SKILL.md with front
// matter, and optional references/), the format HCS-0001 adopts for its `skill`
// contribution kind. Skills ship inside the binary and are the same for every
// person: they hold no personal data and are read-only at run time.
//
// A skill is instructions for the agent, never authority. Loading one registers
// no tool and grants nothing; its required_tools only names the tools it reads
// (HCS-0005-R002).
package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

// The evals/ directory of each pack is left out: its test prompts are
// fixtures for evaluating the coach (#117), never served to an agent.
//
//go:embed packs/*/SKILL.md packs/*/references/*.md
var packs embed.FS

// ErrUnknownSkill is returned for an id that is not in the catalog.
var ErrUnknownSkill = errors.New("unknown skill")

// Packs is the embedded skills directory, one subdirectory per skill.
func Packs() fs.FS {
	sub, err := fs.Sub(packs, "packs")
	if err != nil {
		panic(err)
	}
	return sub
}

// Library is a validated, immutable set of skills ordered by id.
type Library struct {
	skills []Skill
}

var defaultLibrary = sync.OnceValues(func() (*Library, error) { return New(Packs()) })

// Default is the embedded library. The packs are validated by tests, so an
// invalid one panics at startup instead of serving a half-read skill.
func Default() *Library {
	lib, err := defaultLibrary()
	if err != nil {
		panic(fmt.Sprintf("skills: embedded packs are invalid: %v", err))
	}
	return lib
}

var (
	citePattern     = regexp.MustCompile(`\[ref:([a-z0-9-]+)\]`)
	identifierLine  = regexp.MustCompile(`(?m)^- (DOI|PMID): \S+`)
	referenceHeader = "### "
)

// New reads every <id>/SKILL.md of fsys with its <id>/references/*.md files.
// Every citation [ref:key] in a skill must name a reference entry ("### key")
// that carries a DOI or PMID line, and every entry must be cited.
func New(fsys fs.FS) (*Library, error) {
	files, err := fs.Glob(fsys, "*/SKILL.md")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no skills found")
	}
	lib := &Library{}
	for _, file := range files {
		dir := path.Dir(file)
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		s, err := Parse(dir, data)
		if err != nil {
			return nil, err
		}
		if s.References, err = readReferences(fsys, dir); err != nil {
			return nil, err
		}
		if err = checkCitations(s); err != nil {
			return nil, fmt.Errorf("%s: %w", s.ID, err)
		}
		lib.skills = append(lib.skills, s)
	}
	sort.Slice(lib.skills, func(i, j int) bool { return lib.skills[i].ID < lib.skills[j].ID })
	return lib, nil
}

func readReferences(fsys fs.FS, dir string) ([]Reference, error) {
	files, err := fs.Glob(fsys, dir+"/references/*.md")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	refs := []Reference{}
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		refs = append(refs, Reference{Path: strings.TrimPrefix(file, dir+"/"), Content: string(data)})
	}
	return refs, nil
}

// checkCitations ties the body's [ref:key] citations to reference entries, so
// a source cannot be cited without a stable identifier, or listed unused.
func checkCitations(s Skill) error {
	entries := map[string]string{}
	for _, ref := range s.References {
		parts := strings.Split("\n"+ref.Content, "\n"+referenceHeader)
		for _, part := range parts[1:] {
			key, text, _ := strings.Cut(part, "\n")
			key = strings.TrimSpace(key)
			if _, dup := entries[key]; dup {
				return fmt.Errorf("duplicate reference entry %q", key)
			}
			if !identifierLine.MatchString(text) {
				return fmt.Errorf("reference %q has no DOI or PMID line ('- DOI: ...' or '- PMID: ...')", key)
			}
			entries[key] = text
		}
	}
	cited := map[string]bool{}
	for _, m := range citePattern.FindAllStringSubmatch(s.Instructions, -1) {
		if _, ok := entries[m[1]]; !ok {
			return fmt.Errorf("cites [ref:%s] but no reference entry '### %s' exists", m[1], m[1])
		}
		cited[m[1]] = true
	}
	for key := range entries {
		if !cited[key] {
			return fmt.Errorf("reference %q is never cited", key)
		}
	}
	return nil
}

// Catalog lists every skill's metadata, ordered by id.
func (l *Library) Catalog() []Entry {
	out := make([]Entry, 0, len(l.skills))
	for _, s := range l.skills {
		e := s.Entry
		e.Triggers = slices.Clone(e.Triggers)
		out = append(out, e)
	}
	return out
}

// Load returns one skill whole.
func (l *Library) Load(id string) (Skill, error) {
	for _, s := range l.skills {
		if s.ID == id {
			s.Triggers = slices.Clone(s.Triggers)
			s.Limitations = slices.Clone(s.Limitations)
			s.RequiredTools = slices.Clone(s.RequiredTools)
			s.References = slices.Clone(s.References)
			return s, nil
		}
	}
	return Skill{}, fmt.Errorf("%w %q", ErrUnknownSkill, id)
}

// RequiredTools is every tool any skill names, sorted and unique.
func (l *Library) RequiredTools() []string {
	var out []string
	for _, s := range l.skills {
		out = append(out, s.RequiredTools...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
