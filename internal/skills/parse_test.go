package skills

import (
	"reflect"
	"strings"
	"testing"
)

const validBody = `
Intro line.

## Evidence

Something measured.

## Heuristics

Something practised.

## Person-specific assumptions

Something about the person.

## Decisions

Something the person chooses.
`

func skillMD(frontMatter string) []byte {
	return []byte("---\n" + frontMatter + "---\n" + validBody)
}

const validFront = `name: demo-skill
description: "Demo summary. Use it when testing."
license: MIT
metadata:
  version: "0.1.0"
  author: "Someone"
  review_status: draft-unreviewed
  triggers: "first situation; second situation"
  limitations: "not for injuries; not medical advice"
  required_tools: "weekly_volume exercise_history"
`

func TestParseReadsAgentSkillsFrontMatterAndBody(t *testing.T) {
	got, err := Parse("demo-skill", skillMD(validFront))
	if err != nil {
		t.Fatal(err)
	}
	want := Skill{
		Entry: Entry{
			ID:           "demo-skill",
			Version:      "0.1.0",
			Summary:      "Demo summary. Use it when testing.",
			Triggers:     []string{"first situation", "second situation"},
			ReviewStatus: ReviewDraftUnreviewed,
		},
		Author:        "Someone",
		License:       "MIT",
		Limitations:   []string{"not for injuries", "not medical advice"},
		RequiredTools: []string{"weekly_volume", "exercise_history"},
		Instructions:  strings.TrimSpace(validBody),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestParseAcceptsReviewedSkillWithReviewerAndDate(t *testing.T) {
	front := strings.Replace(validFront, "review_status: draft-unreviewed", "review_status: reviewed\n  reviewed_by: \"A Trainer\"\n  reviewed_on: \"2026-12-01\"", 1)
	got, err := Parse("demo-skill", skillMD(front))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewStatus != ReviewReviewed || got.ReviewedBy != "A Trainer" || got.ReviewedOn != "2026-12-01" {
		t.Fatalf("review = %q %q %q", got.ReviewStatus, got.ReviewedBy, got.ReviewedOn)
	}
}

func TestParseUnescapesDoubleQuotedValues(t *testing.T) {
	front := strings.Replace(validFront, `"Demo summary. Use it when testing."`, `"Say \"hi\" and a back\\slash: fine"`, 1)
	got, err := Parse("demo-skill", skillMD(front))
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != `Say "hi" and a back\slash: fine` {
		t.Fatalf("summary = %q", got.Summary)
	}
}

func TestParseRejectsInvalidSkills(t *testing.T) {
	replace := func(old, new string) []byte { return skillMD(strings.Replace(validFront, old, new, 1)) }
	cases := []struct {
		name, dir string
		data      []byte
		want      string
	}{
		{"no front matter", "demo-skill", []byte(validBody), "front matter"},
		{"unterminated front matter", "demo-skill", []byte("---\n" + validFront + validBody), "front matter"},
		{"name differs from directory", "other-skill", skillMD(validFront), "directory"},
		{"uppercase name", "Demo-skill", replace("name: demo-skill", "name: Demo-skill"), "name"},
		{"consecutive hyphens", "demo--skill", replace("name: demo-skill", "name: demo--skill"), "name"},
		{"missing description", "demo-skill", replace(`description: "Demo summary. Use it when testing."`+"\n", ""), "description"},
		{"long summary", "demo-skill", replace(`"Demo summary. Use it when testing."`, `"`+strings.Repeat("a", 301)+`"`), "description"},
		{"missing version", "demo-skill", replace(`  version: "0.1.0"`+"\n", ""), "version"},
		{"version not semantic", "demo-skill", replace(`"0.1.0"`, `"0.1"`), "version"},
		{"missing author", "demo-skill", replace(`  author: "Someone"`+"\n", ""), "author"},
		{"unknown review status", "demo-skill", replace("draft-unreviewed", "approved"), "review_status"},
		{"reviewed without reviewer", "demo-skill", replace("draft-unreviewed", "reviewed"), "reviewed_by"},
		{"missing triggers", "demo-skill", replace(`  triggers: "first situation; second situation"`+"\n", ""), "triggers"},
		{"missing limitations", "demo-skill", replace(`  limitations: "not for injuries; not medical advice"`+"\n", ""), "limitations"},
		{"missing required tools", "demo-skill", replace(`  required_tools: "weekly_volume exercise_history"`+"\n", ""), "required_tools"},
		{"bad tool name", "demo-skill", replace("weekly_volume exercise_history", "weekly_volume Bash(rm)"), "required_tools"},
		{"unknown top-level key", "demo-skill", replace("license: MIT", "license: MIT\nallowed-tools: weekly_volume"), "allowed-tools"},
		{"unknown metadata key", "demo-skill", replace(`  author: "Someone"`, `  author: "Someone"`+"\n  owner: x"), "owner"},
		{"duplicate key", "demo-skill", replace("license: MIT", "license: MIT\nlicense: MIT"), "duplicate"},
		{"yaml list", "demo-skill", replace(`  triggers: "first situation; second situation"`, "  triggers:\n    - first"), "triggers"},
		{"plain value with colon", "demo-skill", replace(`  author: "Someone"`, `  author: a: b`), "quote"},
		{"bad escape", "demo-skill", replace(`"Someone"`, `"Some\none"`), "escape"},
		{"tab indentation", "demo-skill", replace(`  author: "Someone"`, "\tauthor: \"Someone\""), "indent"},
		{"missing section", "demo-skill", []byte(strings.Replace(string(skillMD(validFront)), "## Heuristics", "## Rules of thumb", 1)), "Heuristics"},
		{"sections out of order", "demo-skill", []byte(strings.Replace(strings.Replace(string(skillMD(validFront)), "## Evidence", "## TMP", 1), "## Decisions", "## Evidence", 1)), "order"},
		{"empty section", "demo-skill", []byte(strings.Replace(string(skillMD(validFront)), "Something practised.", "", 1)), "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.dir, tc.data)
			if err == nil {
				t.Fatal("accepted an invalid skill")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
