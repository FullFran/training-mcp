# HCS-0006 Typed errors

| Field | Value |
| --- | --- |
| Status | Draft |
| Version | 0.1.0-draft |
| Requires | HCS-0000 |
| Replaces | none |
| Schema | `schemas/hcs-0006/error/v0.1/error.schema.json` (`urn:hcs:schema:hcs-0006:error:0.1`) |

## 1. Scope

A host that composes plugins fails in a small number of ways that callers need to tell apart without parsing text: a document is malformed, content does not match its digest, a dependency is missing or ambiguous, an operation is denied, a plugin fails. This document defines a closed set of error codes, the error object that carries them, and how the object travels over the two transports HCS adapters use: JSON-RPC 2.0 (MCP, ACP) and HTTP (RFC 9457). It does not define new transport error codes; it rides on the ones those standards already define ([ADR-0006](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0006-error-codes-ride-in-data.md)).

## 2. Error object

```json
{
  "code": "missing_dependency",
  "message": "dev.example.notes requires dev.example.search@1, which no entry provides",
  "entry": "dev.example.notes",
  "capability": "dev.example.search@1",
  "details": { "x-example-hint": "enable the search pack" }
}
```

- `code` (required): one value from section 3.
- `message` (required): human-readable text. Consumers branch on `code`, never on `message`.
- `entry` (optional): the catalog entry id the error is about (HCS-0001).
- `capability` (optional): the capability reference involved (HCS-0002).
- `requirement` (optional): the HCS requirement id that was violated, when the host knows it.
- `details` (optional): an object with extension members only (`x-<distribution>...`), for distribution-specific diagnostics.

**[HCS-0006-R001]** An error a host reports for an HCS-defined failure MUST validate against the HCS-0006 error schema.

**[HCS-0006-R002]** A host MUST NOT report a `code` outside the closed set in section 3; distribution-specific detail goes in `details` under extension members.

## 3. Codes

| Code | Raised when | Defined by |
| --- | --- | --- |
| `manifest_invalid` | a document fails its HCS schema, or an MCP `server.json` in a generation fails its schema or carries a secret value | HCS-0001, HCS-0008 |
| `unsupported_hcs_version` | `hcsVersion` names a contract the host does not implement | HCS-0001 |
| `duplicate_entry` | two catalog entries share an `id` | HCS-0001 |
| `integrity_mismatch` | fetched content does not match `size` or `contentHash`, or a declared `contentId` cannot be verified against the bounded uncompressed tar stream | HCS-0001 |
| `unsupported_contribution` | the host does not implement a contribution `kind` or the entry's package `mediaType`, or its host adapter cannot project that kind into the engine, or cannot start a bundled MCP server as declared (no host runtime for its `runtimeHint`, runtime arguments or control variables, a secret argument; HCS-0008 6.4.1) | HCS-0001, HCS-0008 |
| `package_invalid` | a package whose bytes passed verification is not a well-formed tar, exceeds the host's unpack limits, or has a member HCS-0001 forbids (R019 to R023) | HCS-0001 |
| `proposal_conflict` | a host refuses an answer, an undo or a retried proposal because the proposal was already answered, its target changed since it was proposed, the undo is not allowed, or its identity names a different proposal (HCS-0010 R003, R006, R008, R011) | HCS-0010 |
| `platform_incompatible` | no `compatibility.platforms` item matches the host platform | HCS-0001 |
| `missing_dependency` | no entry or host built-in provides a required capability id | HCS-0003 |
| `version_incompatible` | the capability id is provided, but not at the required contract major; or `compatibility.hosts` does not list this host's id and version (no `capability` member then) | HCS-0003, HCS-0001 |
| `ambiguous_provider` | more than one provider matches and nothing selects one, or two contributions of one generation would project the same engine-level name | HCS-0003, HCS-0008 |
| `dependency_cycle` | activation order cannot be computed because requirements form a cycle | HCS-0003 |
| `permission_denied` | an operation needs a permission the host has not granted, or a signed catalog is bound to an assignment the host does not have, or a contribution would replace, remove or widen a host-owned setting or needs a secret the host has not granted to its entry | HCS-0005, HCS-0007, HCS-0008 |
| `unsupported_capability` | a call targets a capability that is not active on this host | HCS-0004 |
| `timeout` | a lifecycle step or call exceeded the host's deadline, including the health check of a new generation | HCS-0004, HCS-0008 |
| `plugin_failed` | plugin code failed during a lifecycle step or call, or an engine run started from a new generation failed the host's health check | HCS-0004, HCS-0008 |
| `signature_invalid` | a signed catalog envelope or key set is unsigned, has a bad signature, does not meet its threshold, or has expired | HCS-0007 |
| `rollback_rejected` | a signed catalog envelope or key set is older than the last accepted one, or reuses its sequence or version with different content | HCS-0007 |
| `generation_mismatch` | an ACP `session/load` asks to reopen a session on a generation other than the one it was created with | HCS-0008 |

For `package_invalid` a host sets `requirement` to the HCS-0001 requirement the package violates and SHOULD name the member path in an extension member of `details`; it never copies member content into `message` or `details`. The code is separate from `integrity_mismatch` because the bytes are exactly what the publisher described: fetching them again from another location cannot help ([ADR-0033](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0033-archive-member-safety.md)).

The table is the single registry of HCS error codes. A profile that needs a new code adds a row here naming itself in the last column; it does not keep a code list of its own ([ADR-0014](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0014-profile-error-codes-join-the-core-registry.md)). A host that does not implement that profile never raises its codes.

A condition that no HCS document defines is not an HCS error. A host reports it outside the HCS error object, for example as an RFC 9457 problem type whose URI the distribution owns, and never as an `x-` value in `code`.

## 4. Transport mappings

### 4.1 JSON-RPC 2.0 (MCP, ACP)

The numeric `error.code` is whatever the carrying protocol prescribes for the situation (for example `-32602` for invalid params, or an implementation-defined code in `-32000..-32099`). HCS does not allocate numeric codes, because MCP and ACP already use parts of the server-error range. See [docs/standards/json-rpc-2.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/json-rpc-2.md).

**[HCS-0006-R003]** When an HCS error is carried in a JSON-RPC 2.0 error response, the HCS error object MUST be the value of `error.data.hcsError`.

### 4.2 HTTP (RFC 9457)

**[HCS-0006-R004]** When an HCS error is carried as an RFC 9457 problem details object, its `type` MUST be `urn:hcs:error:<code>`, where `<code>` equals `hcsError.code`.

**[HCS-0006-R005]** When an HCS error is carried as an RFC 9457 problem details object, the HCS error object MUST be the value of the extension member `hcsError`.

`title` SHOULD be the code; `detail` SHOULD be the `message`; `status` is chosen by the HTTP surface. See [docs/standards/rfc9457-problem-details.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/rfc9457-problem-details.md).

### 4.3 Clients

A client that reads the wrong member gets the wrong answer and can still pass its own tests: the first host's server put the HCS error in an extension member, its client read a top-level `reason`, and the client's unit test passed because it used a hand-written fake with the same wrong shape. Every enrolled machine would have reported "nothing published yet" as a sync failure; only an end-to-end run against the real server caught it.

**[HCS-0006-R006]** A client MUST take an HCS error only from `error.data.hcsError` of a JSON-RPC 2.0 error response, from the `hcsError` member of an RFC 9457 problem details object, or from an MCP tool result as R009 says, and MUST NOT derive an HCS code from any other member (for example a top-level `code`, `reason` or `title`). A response whose `hcsError` is missing or fails the HCS-0006 schema, or a problem whose `type` does not equal `urn:hcs:error:<hcsError.code>`, carries no HCS error. Other members of a problem with the wrong type (for example a string `status`) are ignored, as RFC 9457 section 3.1 says, and do not affect the HCS error.

`client` cases give the response a client receives and the HCS error it must extract, or `null` when there is none. A client's tests should build their fake server responses from these fixtures (or from responses recorded from the real server), never from hand-written objects, so the fake cannot share the client's mistake.

Because the set of codes is closed (R002), a client that implements this version reads a code added by a later version as "no HCS error" and handles the response as a generic failure.

### 4.4 Moving from a distribution code to a registry code

This section is informative. A distribution can ship before the registry has a code for one of its failures, and report it with a value of its own (for example `x-example-signature_invalid` where `signature_invalid` was later added). Such a value was never an HCS code (R002), so nothing here makes it one. When the registry gains the code:

- Hosts and servers emit the registry code from the first release that implements the version of this document that defines it, and move anything distribution-specific to `details`.
- A client of that distribution may keep reading the old value from the peers it already has in the field, as a distribution rule outside HCS, and treats it as the registry code. Under R006 the old value still carries no HCS error, so a client of another distribution is unaffected.
- The client drops the old value when no supported release of that distribution emits it any more. HCS sets no duration: the window is that distribution's support period for the releases that emit the old value, and the distribution states it in its own documentation.

New distributions do not need this: they report a registry code, or no HCS error.

### 4.5 MCP tool results

Over MCP a failure reaches the caller in one of two ways ([docs/standards/mcp.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/mcp.md); MCP 2026-07-28, Tools, Error Handling). A protocol error is a JSON-RPC error for a problem with the request itself that a model is unlikely to fix (an unknown tool, a malformed request, a server failure); section 4.1 carries an HCS error there. A tool execution error is a `CallToolResult` with `isError: true` for actionable feedback a model can use to retry (an API failure, invalid input, a business-rule refusal), which MCP asks clients to show to the model. A host that reports an HCS error for a failure of the tool's own execution, such as `permission_denied` for a call the person has not allowed, uses the second way, because a JSON-RPC error hides the failure from the model. A failure of the request or of the host itself, such as `unsupported_capability` for a call to a tool that is not active, stays a JSON-RPC error. One failure is reported one way, never both. A host that reports every HCS error as a JSON-RPC error still conforms to R003 ([ADR-0035](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0035-hcs-errors-in-mcp-tool-results.md)).

```json
{
  "content": [{ "type": "text", "text": "dev.example.notes has no grant for dev.example.fs.write" }],
  "isError": true,
  "structuredContent": {
    "hcsError": {
      "code": "permission_denied",
      "message": "dev.example.notes has no grant for dev.example.fs.write",
      "entry": "dev.example.notes"
    }
  }
}
```

**[HCS-0006-R007]** When an HCS error is carried in the result of an MCP `tools/call`, the result MUST have `isError` equal to `true`, MUST carry the HCS error object as the value of `structuredContent.hcsError`, and MUST have at least one `text` item in `content`.

The text item is for the model and the person. It SHOULD contain the `message` and MAY add guidance, such as the next tool to call; clients never read it for the code (R009). MCP asks a tool that returns structured content to also serialize it as text; the text item satisfies that.

`structuredContent` is a JSON object whose members other than `hcsError` belong to the host or the tool. A tool that also reports a condition no HCS document defines (section 3, last paragraph) puts it in those other members, for example an RFC 9457 problem object, and never as an `x-` value in `code`.

MCP says a server MUST provide structured results that conform to a tool's `outputSchema`, and says nothing that exempts error results. So a tool that declares an `outputSchema` and can report an HCS error as a result has to declare one that accepts it.

**[HCS-0006-R008]** When a tool declares an `outputSchema`, the `structuredContent` of an HCS error result for that tool MUST validate against it.

A schema that lists required result members rejects `{ "hcsError": ... }`. The usual remedy is to wrap the schema in `anyOf` with an alternative that requires `hcsError`; the SDK's `withHcsErrorOutput` does that (`sdk/ts/README.md`). A client that validates results can then keep doing so without special cases for errors.

**[HCS-0006-R009]** A client MUST take an HCS error from an MCP tool result only when `isError` is `true` and `structuredContent.hcsError` validates against the HCS-0006 error schema, and MUST NOT take one from `content` text, from a result whose `isError` is not `true`, or from any other member of `structuredContent`.

A result that fails R009 carries no HCS error; the client handles it as an ordinary tool failure. The `client` cases give the `tools/call` response a client receives in `input.response`, whose `result` member is the tool result.

## 5. Open questions

- OQ-0006-1: whether errors need a stable machine-readable `retryable` hint (`timeout` usually is, `integrity_mismatch` is not). No consumer needs it yet.
- OQ-0006-2: whether one failure may carry several errors (a resolution pass usually finds several). The reference runner returns a list; the wire shape for a list is not defined.
- OQ-0006-3: mapping to OpenTelemetry `error.type` (the GenAI conventions are Development status; see [docs/standards/opentelemetry-genai.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/opentelemetry-genai.md)).
- OQ-0006-5: an optional agent-recovery hint for tool results (the next tool to call after a refusal). One consumer puts it in a `next_step` member of its own problem object and repeats it in the text. It names tools of that product, a model is steered by text it already reads, and no second consumer carries a hint, so HCS defines no member. A distribution can put it in the text item (R007) or in `details` under an extension member (`x-<distribution>-next-step`).
- OQ-0006-4: a stable sub-reason for `signature_invalid` (unsigned, bad signature, threshold not met, expired) so operators can tell them apart without parsing `message`. The first host distinguishes them; HCS-0006 has no member for it yet.
