# Skills

A skill is training-theory guidance an agent loads when it needs it: how to
think about weekly volume, fatigue or choosing exercises, without picking a
methodology for the person. The agent sees a small catalog with
`list_skills` and loads one skill whole with `load_skill`
([tool reference](tools.md#skills)). Nothing else in the server changes when a
skill is loaded.

Skills are the first step towards the knowledge packs in #114 and #115. Personal
data never lives in a skill: the person's numbers come from the vibeTraining
tools the skill names.

## What a skill is not

- **Not a permission.** A skill is instructions for the agent. Loading it
  registers no tool, hides none and grants nothing. Every write the agent makes
  afterwards is still a proposal ([Every write is a
  proposal](tools.md#every-write-is-a-proposal)). This follows HCS-0005-R002:
  a request or contribution metadata is never a grant.
- **Not dynamic tool registration.** The tool list is fixed and the same for
  every client. Whether a host calls `list_skills` or `load_skill` at all is
  the host's and the model's decision; the server instructions only mention
  that `list_skills` exists. A host that never calls them gets no skill.
- **Not per person.** Skills are embedded in the binary with `go:embed`, global
  and read-only. There is no table, no migration and no way to add one at run
  time.

## Format

Each skill is a directory in the [Agent Skills](https://agentskills.io/specification)
format, the format HCS-0001 adopts for its `skill` contribution kind:

```
internal/skills/packs/<id>/
  SKILL.md               front matter + instructions (served)
  references/sources.md  one entry per cited source (served)
  evals/prompts.json     test prompts for evaluating the coach (not served)
```

`SKILL.md` starts with YAML front matter. The server reads it with a strict
subset of YAML (the module has no YAML dependency): `key: value` lines, one
`metadata:` map indented by two spaces, values that are plain words or
double-quoted strings. Anything else is rejected with the line number. The
comments below explain the fields; the parser does not accept comments.

```yaml
---
name: fatigue-autoregulation          # the id: lowercase, digits, single hyphens; equals the directory
description: "One line, up to 300 characters; the catalog summary."
license: MIT                           # optional
compatibility: "..."                   # optional, 1-500 characters
metadata:
  version: "0.1.0"                     # semantic version
  author: "training-mcp maintainers"
  review_status: draft-unreviewed      # or reviewed
  reviewed_by: "..."                   # required when reviewed, absent otherwise
  reviewed_on: "2027-01-31"            # required when reviewed, YYYY-MM-DD
  triggers: "situation one; situation two"        # ';'-separated
  limitations: "limit one; limit two"             # ';'-separated
  required_tools: "performance_dropoff live_workout"  # space-separated MCP tools
---
```

`allowed-tools`, the Agent Skills field for pre-approved tools, is rejected on
purpose. `required_tools` only names the read tools the skill relies on; a test
fails if any of them is not a registered, read-only tool.

The body is Markdown and must contain these level-two sections, in this order,
none empty (other sections may sit between them):

1. `## Evidence`: what research found, each claim citing a source.
2. `## Heuristics`: common practice without a verified source, presented as
   options, never as fact.
3. `## Person-specific assumptions`: what must be read from the person's data
   or asked, never assumed.
4. `## Decisions`: what the person chooses, with the costs of each option.

## References

A claim in `## Evidence` cites a source as `[ref:key]`. Each key is an entry
`### key` in a file under `references/`, with at least a `- DOI:` or `- PMID:`
line. Loading the library fails if a citation has no entry, an entry has no
identifier, a key repeats, or an entry is never cited.

Before a source is added it is checked against its live Crossref record
(title, authors, year) and its PubMed record (PMID, abstract), and the entry
records design, population, finding and limits from the abstract. A claim with
no verified source goes under `## Heuristics`, labelled as practice. Keys are
stable so that a later evidence tool (#116) can link them to evidence ids; the
skills do not depend on it.

## Review status

| Status | Meaning |
|---|---|
| `draft-unreviewed` | Written from verified sources, but no knowledgeable trainer has read it for safety and clarity. The skill says so and tells the agent to say so. |
| `reviewed` | A named reviewer (`reviewed_by`) read this version on `reviewed_on`. A new version needs a new review. |

All shipped skills are `draft-unreviewed` at 0.1.0.

## Test prompts

`evals/prompts.json` holds five cases per skill, in this order: `normal`,
`ambiguous`, `missing-data`, `conflicting-evidence` and `limitation`. Each has
a context, the prompt, the expected behaviour and a `must_not` list. They are
fixtures for evaluating the coach (#117); a test checks that every skill has
them for its current version.

## Adding or changing a skill

1. Create `internal/skills/packs/<id>/SKILL.md` with the front matter and the
   four sections. Keep the body under about 500 lines.
2. Verify each source on Crossref and PubMed, add it to
   `references/sources.md`, and cite it. Anything unverified is a heuristic.
3. Name only read tools in `required_tools`, and tell the agent to take the
   person's numbers from them.
4. Limitation, pain and injury cases fail closed: stop and defer to a
   professional. A skill never tells the agent to skip a proposal or write on
   its own.
5. Write the five test prompts in `evals/prompts.json`.
6. Bump `version` for any change to a published skill, and reset
   `review_status` to `draft-unreviewed` unless the new version was reviewed.
7. Run `go test ./internal/skills/ ./internal/mcpsrv/`. The catalog must stay
   under 4096 bytes.
