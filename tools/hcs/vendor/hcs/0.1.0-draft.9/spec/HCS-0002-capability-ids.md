# HCS-0002 Capability identifiers

| Field | Value |
| --- | --- |
| Status | Draft |
| Version | 0.1.0-draft |
| Requires | HCS-0000 |
| Replaces | none |
| Schema | `schemas/hcs-0002/capability/v0.1/capability.schema.json` (`urn:hcs:schema:hcs-0002:capability:0.1`) |

## 1. Scope

A capability is a contract a host or package can provide and another package can require ("search the web", "store notes"). Its identifier names the contract, not the package that implements it, so a distribution can swap implementations without touching dependents.

Nothing existing gives this: MCP tool names are local to one server, A2A skills live inside one Agent Card, Agent Skills `name` identifies a directory ([docs/standards/mcp.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/mcp.md), [docs/standards/a2a.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/a2a.md), [docs/standards/agent-skills.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/agent-skills.md)). HCS-0002 adds only the identifier; what a contract means (methods, schemas) stays with the standard the contract is expressed in, for example a set of MCP tools.

## 2. Grammar

- **Capability id:** lowercase dotted identifier with at least two segments, each `[a-z0-9]([a-z0-9-]*[a-z0-9])?`, at most 128 characters. Example: `dev.example.search`.
- **Capability reference:** `<capability id>@<major>`, where `<major>` is a non-negative integer without leading zeros. Example: `dev.example.search@1`.

The major is the contract version. Compatible changes keep the major; an incompatible change is a new major ([ADR-0005](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0005-capability-refs-major-only.md)). There are no minor versions or ranges in references.

## 3. Requirements

**[HCS-0002-R001]** A capability reference in an HCS document MUST match the capability reference grammar.

**[HCS-0002-R002]** A host MUST match a required capability to providers by capability reference only, never by the id of the package that provides it.

**[HCS-0002-R003]** A host MUST treat two references with the same capability id and different majors as different contracts.

**[HCS-0002-R004]** The owner of a contract SHOULD use a capability id whose leading segments are a reversed domain name it controls.

**[HCS-0002-R005]** The owner of a contract SHOULD publish a new major when a change can break an existing provider or consumer.

R004 and R005 are guidance for contract owners; conformance does not test SHOULD requirements.

## 4. Open questions

- OQ-0002-1: a reserved namespace for contracts HCS itself defines (for example `hcs.*`). None exist yet.
- OQ-0002-2: where a contract's definition is published and how a host learns it (a registry, or a document next to the package).
- OQ-0002-3: whether a host may declare that one provider implements several majors of the same id. Today it lists each reference.
- OQ-0002-4: relation to MCP extension identifiers and A2A extension URIs, if an adapter wants to map them (TOOLS-1, AGENT-1).
