# HCS-0009 Tool surface: effect classes of package tools

| Field | Value |
| --- | --- |
| Status | Draft |
| Version | 0.1.0-draft |
| Requires | HCS-0001, HCS-0005, HCS-0006, HCS-0008 |
| Replaces | none |
| Schema | `schemas/hcs-0009/tool-effects/v0.1/tool-effects.schema.json` (`urn:hcs:schema:hcs-0009:tool-effects:0.1`) |

## 1. Scope

A package can contribute an MCP server (HCS-0001 `mcp-server`, HCS-0008 section 6.4), and a host shows the tools of that server to an engine, a client and a person, who decide whether a call needs a question first. To decide, the host needs to know what a tool does to the world: only reads, only adds, or may change or remove something. MCP carries that as `ToolAnnotations` hints (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`), but they are optional, they come from the server itself, and their defaults are the most dangerous values ([docs/standards/mcp.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/mcp.md)). Three independent harnesses built the same step on their own: each classifies every tool it exposes into a small set of effects, maps the class to annotations, and treats a tool nobody classified as the most dangerous one ([ADR-0034](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0034-tool-effect-classes.md)).

This document defines the effect classes, where a package declares them, the mapping to MCP annotations, and what a host does with a tool that has no usable declaration. It does not define permission ids (HCS-0005 OQ-0005-1), approval modes, which classes ask a question (a distribution policy, README rule 4), or how a tool's arguments are judged.

## 2. Terms

- **Effect class:** one of `read`, `additive` or `destructive`.
  - `read`: the tool changes nothing the person could observe.
  - `additive`: the tool may create data or records, and never changes or removes existing ones.
  - `destructive`: the tool may change or remove existing data, or has an effect the person cannot undo (for example sending or publishing).
- **Declaration:** the entry for one tool, `{ "effect": <string>, "idempotent": <boolean, optional>, "openWorld": <boolean, optional> }`, plus extension members. `idempotent` says that repeating the same call with the same arguments has no further effect. `openWorld` says that the tool reaches things outside the system the server owns (the web, other people's accounts).
- **Tool name:** the name the server reports in `tools/list`. It is local to the server (HCS-0002 does not treat it as an identifier).

## 3. Where a package declares

The declarations are the member `tools` of the object at `_meta["io.github.fullfran.hcs"]` in the server's `server.json`: a map from tool name to declaration. `server.json` already is the descriptor of an `mcp-server` contribution (HCS-0008-R010) and, for a `file` server, travels in the same signed package as the code it describes; HCS-0000 section 7 names this `_meta` key for HCS's own extensions to MCP documents. The catalog manifest (HCS-0001) is not used: a tool list belongs to the server, not to the catalog entry, and the manifest would repeat it. A tool listed by the server can only be known at run time, so the declaration is checked against what the server lists when the host reads `tools/list`; a declaration for a tool the server does not list has no effect.

**[HCS-0009-R001]** If the `_meta["io.github.fullfran.hcs"]` member of a `server.json` of an `mcp-server` contribution is present, it MUST validate against the `serverMeta` definition of the HCS-0009 schema, and an adapter MUST report `manifest_invalid` for an entry whose member does not.

**[HCS-0009-R002]** A package that contributes an `mcp-server` SHOULD declare every tool its server lists.

R001 does not reject an unknown class: `effect` is any non-empty string, so a class added by a later version is a valid document (R004). It is part of the first check of HCS-0008 (`manifest_invalid`), before any `unsupported_contribution` or `permission_denied`.

## 4. Mapping to MCP annotations

Let `idempotent` be `true` only when the declaration says so, and `openWorld` be `false` only when the declaration says so.

| Effect class | `readOnlyHint` | `destructiveHint` | `idempotentHint` | `openWorldHint` |
| --- | --- | --- | --- | --- |
| `read` | `true` | `false` | `true` | `openWorld` |
| `additive` | `false` | `false` | `idempotent` | `openWorld` |
| `destructive` | `false` | `true` | `idempotent` | `openWorld` |

**[HCS-0009-R003]** A host that presents the tools of a projected MCP server to an engine, a client or a person MUST present each tool with the four annotations that the table in section 4 gives for its declaration.

## 5. Fail closed

**[HCS-0009-R004]** A host MUST present a tool whose declaration is missing, or whose `effect` is not `read`, `additive` or `destructive`, as `destructive` with `idempotent` false and `openWorld` true, that is `readOnlyHint` false, `destructiveHint` true, `idempotentHint` false and `openWorldHint` true.

The values in R004 are the MCP defaults for a tool without annotations, written down so that a host that sends no annotations at all is not the only way to get them. A host MAY refuse to start a server that has undeclared tools; this document does not require it.

**[HCS-0009-R005]** A host MUST NOT take the effect of a tool from the annotations the server reports for itself, and MUST replace them with the annotations of R003 or R004 when it presents the tool.

MCP says a client must treat annotations from a server it does not trust as untrusted. A signed package can still contain a server that describes itself as read-only and writes; the declaration is covered by the package's content hash and signature, the server's run-time answer is not.

## 6. A class is metadata, not a grant

**[HCS-0009-R006]** A host MUST NOT treat a declared effect class, `read` included, as a grant, and MUST evaluate every `tools/call` of a package's server under HCS-0005-R001 as if the declaration did not exist.

This is HCS-0005-R002 applied to tool metadata: `readOnlyHint` and the class behind it are what the publisher says, and the host decides. A distribution may use the class to decide when to ask a person (for example never for `read` once a grant exists); it does so in its own policy, not by skipping the check.

## 7. Conformance

`document` cases check the shape of the member (R001) and, through the schema's `classification` definition, the annotations that follow from a declaration (R003) and from a missing or unknown one (R004). `authority` cases check that a declared class gives no grant (R006). `host` cases check what a host presents (R003 to R005). The reference SDK implements the mapping in `sdk/ts/src/tool-effects.ts`.

## 8. Open questions

- OQ-0009-1: finer classes. One harness separates move, copy, rename and archive from create, delete and edit, and judges the call by whether its destination exists; this document maps all of them to `additive` or `destructive` and leaves the finer judgement to an `x-<distribution>` member of the declaration. A second harness that needs them would justify a core class.
- OQ-0009-2: a class for effects that leave the system and cannot be undone (send, publish). Today they are `destructive` with `openWorld` true.
- OQ-0009-3: whether a remote MCP server (`remotes` in `server.json`) can be trusted to list only the tools it declared, and how a host binds the declaration to a server it does not ship.
- OQ-0009-4: classes for MCP resources and prompts, and for tools a harness provides itself (not through a package).
