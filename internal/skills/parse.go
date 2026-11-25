package skills

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Review statuses. A skill is a draft until a knowledgeable trainer has read it
// for safety and clarity; reviewed needs who and when.
const (
	ReviewDraftUnreviewed = "draft-unreviewed"
	ReviewReviewed        = "reviewed"
)

// Sections every skill body has, in this order (#115): what was measured, what
// is practised without strong evidence, what depends on the person, and what
// the person decides.
var Sections = []string{"Evidence", "Heuristics", "Person-specific assumptions", "Decisions"}

// Entry is what the catalog shows for one skill: metadata only, never the
// instructions, so listing every skill stays cheap.
type Entry struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Summary      string   `json:"summary"`
	Triggers     []string `json:"triggers"`
	ReviewStatus string   `json:"review_status"`
}

// Skill is one skill, loaded whole.
type Skill struct {
	Entry
	Author        string      `json:"author"`
	License       string      `json:"license,omitempty"`
	ReviewedBy    string      `json:"reviewed_by,omitempty"`
	ReviewedOn    string      `json:"reviewed_on,omitempty"`
	Limitations   []string    `json:"limitations"`
	RequiredTools []string    `json:"required_tools"`
	Instructions  string      `json:"instructions"`
	References    []Reference `json:"references"`
}

// Reference is one file of a skill's references/ directory, returned whole.
type Reference struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

const maxSummary = 300

var (
	// Agent Skills name: lowercase letters, digits and single hyphens, 1-64.
	namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// Semantic Versioning 2.0.0, without build metadata.
	semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	toolPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	keyPattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// Top-level keys: the Agent Skills fields this project uses. allowed-tools is
// left out on purpose: it pre-approves tools, and a skill here never asks for
// authority (HCS-0005-R002); required_tools only names what it reads.
var topKeys = map[string]bool{"name": true, "description": true, "license": true, "compatibility": true, "metadata": true}

var metadataKeys = map[string]bool{
	"version": true, "author": true, "review_status": true, "reviewed_by": true, "reviewed_on": true,
	"triggers": true, "limitations": true, "required_tools": true,
}

// Parse reads one SKILL.md in the Agent Skills format: YAML front matter, then
// Markdown. The front matter is read with a strict subset of YAML, since the
// module has no YAML dependency: `key: value` lines, one nested `metadata:` map
// indented by two spaces, and values that are plain words or double-quoted
// strings whose only escapes are \" and \\. Anything else is an error, never a
// guess. dir is the skill's directory, which Agent Skills requires to equal its
// name.
func Parse(dir string, data []byte) (Skill, error) {
	front, body, err := split(string(data))
	if err != nil {
		return Skill{}, err
	}
	top, meta, err := readFrontMatter(front)
	if err != nil {
		return Skill{}, err
	}
	var s Skill
	s.ID = top["name"]
	if !namePattern.MatchString(s.ID) || len(s.ID) > 64 {
		return Skill{}, fmt.Errorf("name %q: use 1-64 lowercase letters, digits and single hyphens", s.ID)
	}
	if s.ID != dir {
		return Skill{}, fmt.Errorf("name %q differs from its directory %q", s.ID, dir)
	}
	s.Summary = top["description"]
	if s.Summary == "" || len(s.Summary) > maxSummary {
		return Skill{}, fmt.Errorf("%s: description is the one-line summary: 1-%d characters", s.ID, maxSummary)
	}
	if c, ok := top["compatibility"]; ok && (c == "" || len(c) > 500) {
		return Skill{}, fmt.Errorf("%s: compatibility must be 1-500 characters", s.ID)
	}
	s.License = top["license"]
	s.Version = meta["version"]
	if !semverPattern.MatchString(s.Version) {
		return Skill{}, fmt.Errorf("%s: metadata version %q is not a semantic version", s.ID, s.Version)
	}
	s.Author = meta["author"]
	if s.Author == "" {
		return Skill{}, fmt.Errorf("%s: metadata author is required", s.ID)
	}
	if err = s.readReview(meta); err != nil {
		return Skill{}, err
	}
	if s.Triggers = splitList(meta["triggers"], ";"); len(s.Triggers) == 0 {
		return Skill{}, fmt.Errorf("%s: metadata triggers needs at least one situation, separated by ';'", s.ID)
	}
	if s.Limitations = splitList(meta["limitations"], ";"); len(s.Limitations) == 0 {
		return Skill{}, fmt.Errorf("%s: metadata limitations needs at least one, separated by ';'", s.ID)
	}
	s.RequiredTools = strings.Fields(meta["required_tools"])
	if len(s.RequiredTools) == 0 {
		return Skill{}, fmt.Errorf("%s: metadata required_tools needs at least one MCP tool, separated by spaces", s.ID)
	}
	for _, tool := range s.RequiredTools {
		if !toolPattern.MatchString(tool) {
			return Skill{}, fmt.Errorf("%s: required_tools %q is not an MCP tool name", s.ID, tool)
		}
	}
	if err = checkSections(body); err != nil {
		return Skill{}, fmt.Errorf("%s: %w", s.ID, err)
	}
	s.Instructions = strings.TrimSpace(body)
	return s, nil
}

func (s *Skill) readReview(meta map[string]string) error {
	s.ReviewStatus = meta["review_status"]
	s.ReviewedBy = meta["reviewed_by"]
	s.ReviewedOn = meta["reviewed_on"]
	switch s.ReviewStatus {
	case ReviewDraftUnreviewed:
		if s.ReviewedBy != "" || s.ReviewedOn != "" {
			return fmt.Errorf("%s: a %s skill has no reviewed_by or reviewed_on", s.ID, ReviewDraftUnreviewed)
		}
	case ReviewReviewed:
		if s.ReviewedBy == "" {
			return fmt.Errorf("%s: a reviewed skill needs reviewed_by", s.ID)
		}
		if _, err := time.Parse(time.DateOnly, s.ReviewedOn); err != nil {
			return fmt.Errorf("%s: a reviewed skill needs reviewed_on as YYYY-MM-DD", s.ID)
		}
	default:
		return fmt.Errorf("%s: review_status %q is not %s or %s", s.ID, s.ReviewStatus, ReviewDraftUnreviewed, ReviewReviewed)
	}
	return nil
}

func split(text string) (front, body string, err error) {
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", "", errors.New("SKILL.md must start with a front matter line '---'")
	}
	front, body, ok = strings.Cut(rest, "\n---\n")
	if !ok {
		return "", "", errors.New("front matter is not closed by a line '---'")
	}
	return front + "\n", body, nil
}

func readFrontMatter(front string) (top, meta map[string]string, err error) {
	top, meta = map[string]string{}, map[string]string{}
	inMetadata := false
	for i, line := range strings.Split(strings.TrimSuffix(front, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indented := strings.HasPrefix(line, "  ")
		if strings.HasPrefix(line, "\t") || (indented && strings.HasPrefix(line, "   ")) {
			return nil, nil, fmt.Errorf("front matter line %d: indent metadata keys by exactly two spaces", i+1)
		}
		key, raw, ok := strings.Cut(strings.TrimPrefix(line, "  "), ":")
		if !ok || !keyPattern.MatchString(key) {
			return nil, nil, fmt.Errorf("front matter line %d: expected 'key: value'", i+1)
		}
		raw = strings.TrimSpace(raw)
		target, known := top, topKeys
		if indented {
			if !inMetadata {
				return nil, nil, fmt.Errorf("front matter line %d: only metadata keys are indented", i+1)
			}
			target, known = meta, metadataKeys
		} else {
			inMetadata = key == "metadata"
		}
		if !known[key] {
			return nil, nil, fmt.Errorf("front matter line %d: unknown key %q", i+1, key)
		}
		if _, dup := target[key]; dup {
			return nil, nil, fmt.Errorf("front matter line %d: duplicate key %q", i+1, key)
		}
		if key == "metadata" && !indented {
			if raw != "" {
				return nil, nil, fmt.Errorf("front matter line %d: metadata is a map of indented keys", i+1)
			}
			target[key] = ""
			continue
		}
		if raw == "" {
			return nil, nil, fmt.Errorf("front matter line %d: %s has no value; lists are strings separated by ';' or spaces", i+1, key)
		}
		value, err := scalar(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("front matter line %d: %s: %w", i+1, key, err)
		}
		target[key] = value
	}
	return top, meta, nil
}

// scalar reads a plain word or a double-quoted string.
func scalar(raw string) (string, error) {
	if !strings.HasPrefix(raw, `"`) {
		if strings.ContainsAny(raw[:1], `'[]{}>|*&!%@#,?-`+"`") || strings.Contains(raw, ": ") || strings.Contains(raw, " #") {
			return "", errors.New("quote this value with double quotes")
		}
		return raw, nil
	}
	if len(raw) < 2 || !strings.HasSuffix(raw, `"`) {
		return "", errors.New("unterminated double-quoted string")
	}
	var out strings.Builder
	inner := raw[1 : len(raw)-1]
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch c {
		case '"':
			return "", errors.New(`escape a double quote inside a string as \"`)
		case '\\':
			if i+1 >= len(inner) || (inner[i+1] != '"' && inner[i+1] != '\\') {
				return "", errors.New(`unsupported escape: only \" and \\ are allowed`)
			}
			i++
			out.WriteByte(inner[i])
		default:
			out.WriteByte(c)
		}
	}
	return out.String(), nil
}

func splitList(value, sep string) []string {
	var out []string
	for _, item := range strings.Split(value, sep) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// checkSections requires each of Sections once, as a level-two heading, in
// order and not empty. Other headings may sit between them.
func checkSections(body string) error {
	type heading struct {
		title string
		start int
	}
	var headings []heading
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if title, ok := strings.CutPrefix(line, "## "); ok {
			headings = append(headings, heading{strings.TrimSpace(title), i})
		}
	}
	next := 0
	for h, hd := range headings {
		want := -1
		for k, s := range Sections {
			if s == hd.title {
				want = k
			}
		}
		if want < 0 {
			continue
		}
		if want > next {
			for _, later := range headings[h+1:] {
				if later.title == Sections[next] {
					return fmt.Errorf("section %q is out of order; the order is %s", Sections[next], strings.Join(Sections, ", "))
				}
			}
			return fmt.Errorf("missing section '## %s' before %q", Sections[next], hd.title)
		}
		if want < next {
			return fmt.Errorf("section %q is out of order; the order is %s", hd.title, strings.Join(Sections, ", "))
		}
		end := len(lines)
		if h+1 < len(headings) {
			end = headings[h+1].start
		}
		if strings.TrimSpace(strings.Join(lines[hd.start+1:end], "\n")) == "" {
			return fmt.Errorf("section %q is empty", hd.title)
		}
		next++
	}
	if next < len(Sections) {
		return fmt.Errorf("missing section '## %s'", Sections[next])
	}
	return nil
}
