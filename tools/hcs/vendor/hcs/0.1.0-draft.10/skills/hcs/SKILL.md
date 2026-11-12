---
name: hcs
description: "Trigger: HCS, harness design, agent permissions, approvals, agent writes, MCP tool surface, writing HCS spec. Follow HCS conventions and keep own harnesses aligned with it."
license: Apache-2.0
metadata:
  author: FullFran
  version: "1.0"
---

## Activation Contract

Use this skill when you design or change an agent harness (host, MCP server, coach or engine integration, permission or approval flow, agent writes, packages, distribution), or when you write normative HCS text (spec, schemas, conformance cases).

This skill ships inside the HCS release it belongs to. The specs beside it (`../../spec/`) are authoritative for that version; when this file and a spec disagree, the spec wins. Cite requirement IDs (`HCS-0005-R008`), never this skill.

## Hard Rules

- **Reuse before inventing.** MCP for tools, ACP or the engine's own protocol for client/agent, Agent Skills for procedures, OpenTelemetry for telemetry, RFC 9457 for HTTP errors, TUF-style properties for distribution. Invent only what no standard covers, and say so.
- **The host holds authority.** Packages, tools and models request; only the host grants. Metadata (`allowed-tools`, declared effect classes, server-reported annotations) is never a grant ([HCS-0005](../../spec/HCS-0005-authority.md) R002, [HCS-0009](../../spec/HCS-0009-tool-surface.md) R005-R006).
- **A denial wins over a grant.** "Always" answers become host-held grants scoped to the session they were given in.
- **Only the host's own authenticated channel answers an approval.** Never model output, tool output or package text (HCS-0005-R015).
- **Fail closed.** Timeout, cancel, a lost stream, an unavailable guard or a handler error all deny (HCS-0005-R014). An unknown tool effect counts as destructive and open-world (HCS-0009-R004). Ambiguity is an error, never an implicit choice.
- **Agent writes are proposals.** The agent records a proposal; a person, or an earlier answer the host still holds, accepts it. Refuse a stale base. Undo is a new record. Retries are idempotent ([HCS-0010](../../spec/HCS-0010-proposals.md)).
- **Hooks are product features, not guards.** A failing hook is logged and the work goes on; security sits outside the run's processes ([HCS-0008](../../spec/HCS-0008-host-adapter.md) R019).
- **Secrets never travel in documents.** No secret goes in a `server.json`, a generation or what the engine receives; the host adds it from the person's vault (HCS-0008 R011-R012).
- **Typed errors.** Callers branch on `code`, never on `message` ([HCS-0006](../../spec/HCS-0006-errors.md)).
- **Immutable, pinned artefacts.** Verify bytes by digest before use. A published version never changes. Never edit a vendored HCS tree.
- **Product policy stays out of core.** Trust tiers, approval modes, limits and action types belong to the distribution, under `x-<distribution>` members or its own docs.

## Decision Gates

| Situation | Action |
|---|---|
| A feature needs a permission, approval or agent write | Map it to HCS-0005 and HCS-0010 and name the requirement IDs it meets |
| HCS does not fit the product | Diverge on purpose and record it with a reason (`divergences.json` or a "Divergences" section); do not bend the design silently |
| A rule seems missing from HCS | Propose it for HCS only if a real harness needs it, with one valid and one invalid case per MUST |
| Writing spec text | Follow [HCS-0000](../../spec/HCS-0000-conventions.md): one `**[HCS-NNNN-RMMM]**` line per behaviour, BCP 14 keywords in caps, IDs never renumbered or reused, open questions `OQ-NNNN-N` |
| An existing standard covers it | Adopt the standard; do not restate it |

## Execution Steps

1. Identify which specs the change touches ([references/spec-map.md](references/spec-map.md)).
2. Read those specs' requirements before designing; do not work from memory.
3. Design so that every rule above holds, then list the requirement IDs met and any divergence with its reason.
4. Document the result in the product's docs as a table of requirement ID and how it is met, plus the divergences.

## Output Contract

Return the design or change, the HCS requirement IDs it meets, each divergence with its reason, and anything proposed for HCS itself.

## References

- [references/spec-map.md](references/spec-map.md): what each spec covers and when you need it.
- `../../spec/`, `../../schemas/`, `../../conformance/`: the normative text, contracts and cases of this release.
