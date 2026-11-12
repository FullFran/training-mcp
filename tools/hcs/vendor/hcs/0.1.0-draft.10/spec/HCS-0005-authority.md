# HCS-0005 Authority: requests and grants

| Field | Value |
| --- | --- |
| Status | Draft |
| Version | 0.1.0-draft |
| Requires | HCS-0001, HCS-0006 |
| Replaces | none |
| Schema | `schemas/hcs-0005/authority/v0.1/authority.schema.json` (`urn:hcs:schema:hcs-0005:authority:0.1`) |

## 1. Scope

Packages ask for authority; the host decides. This document defines the shape of a permission request, the shape of a host grant, and the enforcement rules every host needs. It does not define a permission vocabulary, a policy language or trust tiers: those are distribution decisions (README rule 4) and go under `x-<distribution>` members or in the host's policy engine.

What existing standards cover and what they do not:

- OAuth 2.0 scopes authorize a client against a resource server, not a plugin inside a host ([docs/standards/oauth2.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/oauth2.md)).
- OPA returns decisions; it is a policy decision point, and the host stays the enforcement point ([docs/standards/opa.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/opa.md)). A host MAY delegate the decision to OPA; the rules below still apply to enforcement.
- Agent Skills `allowed-tools` is experimental and advisory ([docs/standards/agent-skills.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/agent-skills.md)).

## 2. Objects

- **Permission id:** a dotted id (HCS-0000 common `dottedId`), for example `dev.example.fs.read`. HCS 0.1 defines no permission ids (OQ-0005-1). For an agent write the permission id is the proposal's action type (HCS-0010-R012).
- **Request** (`permissionRequest`): `{ "permission": <permission id>, "reason": <string, optional> }`, listed in an entry's `requests` array in an HCS-0005 request document, or in the catalog manifest under a future version (OQ-0005-3).
- **Grant** (`grant`): `{ "entry": <package id>, "permission": <permission id> }`, a record held by the host. Optional members: `id`, the host's identifier for the grant; `session`, which limits the grant to one session; `origin`, the `id` of the decision record of the answer that created it (section 3.2). Other scope dimensions (project, subject, operation) are OQ-0005-2.
- **Denial** (`denial`): the same shape as a grant, stored when a person answers "always reject" (section 3.2). A matching denial wins over a matching grant.
- **Check** (`check`): `{ "entry": <package id>, "permission": <permission id>, "session": <string, optional> }`, the question the host asks before an operation.
- **Decision** (`decision`): `{ "allow": true }` or `{ "allow": false, "error": <HCS-0006 error with code permission_denied> }`.
- **Decision record** (`decisionRecord`): what a host writes for each decision it acts on (section 3.3).

A grant or denial **matches** a check when `entry` and `permission` are equal and, if it has a `session`, the check has the same `session`. A check is denied when a denial matches it; otherwise it is allowed when a grant matches it or a person allowed that one operation (`allow_once`, R005); otherwise it is denied.

## 3. Requirements

### 3.1 Enforcement

**[HCS-0005-R001]** A host MUST deny an operation that needs a permission unless it holds a grant for that entry and that permission or a person allowed that one operation (R005), and MUST report the denial with code `permission_denied`.

**[HCS-0005-R002]** A host MUST NOT treat a permission request, or any contribution metadata such as Agent Skills `allowed-tools`, as a grant.

**[HCS-0005-R003]** A host MUST evaluate grants when each operation is dispatched, so that a revoked grant denies every operation dispatched after the revocation.

**[HCS-0005-R004]** A host MUST NOT let an extension member on a request turn the request into a grant.

R001, R002, R004, R007 and R008 are checked by the reference evaluator in the runner (`authority` cases); the evaluator has no input for a one-operation answer, which is checked by `host` cases (R005). R003, R005, R006, R009, R010, R012, R014 and R015 need a running host and have `host` cases only.

### 3.2 Answers from a person

A host may ask a person before an operation, for example through ACP `session/request_permission`, whose options have the kinds `allow_once`, `allow_always`, `reject_once` and `reject_always` ([docs/standards/acp.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/acp.md)). ACP says an "always" option means "remember the choice" without saying who remembers it or for how long. If the engine or client remembers it, the host cannot revoke it and R003 no longer holds. The rules below make the host the only place where a remembered answer lives ([ADR-0023](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0023-always-answers-become-host-grants.md)).

**[HCS-0005-R005]** A host MUST apply an `allow_once` or `reject_once` answer to the one operation it was given for, and MUST NOT store it as a grant or a denial.

**[HCS-0005-R006]** A host MUST store an `allow_always` answer as a grant for that entry and permission, limited to the session in which it was given and carrying in `origin` the decision record of the answer, and MUST revoke it like any other grant (R003).

**[HCS-0005-R007]** A host MUST store a `reject_always` answer as a denial for that entry and permission, limited like R006, and MUST deny with `permission_denied` every later check the denial matches, even when a grant also matches.

**[HCS-0005-R008]** A host MUST NOT let a grant or denial that has a `session` match a check from another session or a check without a session.

A host that stores session-limited answers gives every check made inside a session that session; a check without a session is outside every session-limited grant and denial.

**[HCS-0005-R009]** A host MUST NOT let the engine or the client keep an "always" answer where the host cannot revoke it; an operation the host has not decided under section 3.1 or 3.2 MUST be denied even if the engine or client remembers an earlier answer.

A host that cannot meet R009 with a given engine does not offer the `allow_always` and `reject_always` options on that engine. Whether some permissions refuse "always" answers entirely is issue #16.

An approval path fails open easily: engines commonly block a tool call when a pre-call hook throws but let other hooks continue on error, and a closed dialog or a lost connection leaves the call waiting on nobody. Only an explicit allow lets the operation through ([ADR-0025](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0025-host-guards-fail-closed.md)).

**[HCS-0005-R014]** A host MUST NOT run an operation that waits for a person's answer unless an `allow_once` or `allow_always` answer for that operation arrives before the host's deadline. A reject answer, the deadline passing, an ACP `cancelled` outcome, an error in the host's approval handling and a lost connection to the person MUST each deny the operation with `permission_denied`.

**[HCS-0005-R015]** A host MUST take a person's answer only from its own channel to that person (for example the ACP client connection), never from text the model, a tool or a package produced.

The decision record of an operation denied under R014 has `outcome` `deny` for a reject answer or an ACP `cancelled` outcome, `timeout` when the deadline passed and `error` when the approval handling failed or the connection was lost. Its `decider` is `{ "kind": "subject", ... }` when a person chose an option, and `{ "kind": "policy", "id": "HCS-0005-R014" }` otherwise; `cancelled` is not an `answer`.

### 3.3 Decision records

After an incident, the question is "who allowed this, when, and through which channel". A host can answer it only if it recorded every decision when it made it ([ADR-0024](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0024-permission-decisions-are-recorded.md)).

A decision record has these members:

| Member | Required | Meaning |
| --- | --- | --- |
| `id` | yes | the host's identifier for the record |
| `time` | yes | RFC 3339 UTC timestamp of the decision |
| `entry`, `permission` | yes | what was decided |
| `outcome` | yes | `allow`, `deny`, `timeout` or `error` for an operation; `revoked` when a stored grant or denial is removed |
| `operation` | unless `revoked` | the operation's identifier (for example an MCP or ACP tool call id), never its content |
| `session` | no | the session the operation belongs to |
| `decider` | yes | `{ "kind": "subject" \| "grant" \| "denial" \| "policy", "id": <string> }`: the person's subject id, the stored grant or denial that decided, or the host rule (for example `HCS-0005-R001` when no grant exists) |
| `channel` | yes | how the decision was reached, for example `acp:session/request_permission` or `grant-store` |
| `answer` | no | the ACP option kind the person chose, when a person answered |
| `x-*` | no | extension members |

**[HCS-0005-R010]** A host MUST write a decision record for every permission decision it acts on and for every grant or denial it removes.

**[HCS-0005-R011]** A decision record MUST validate against the HCS-0005 `decisionRecord` schema and MUST NOT contain the operation's arguments, its result or any content of the conversation, in any member, extension members included.

**[HCS-0005-R012]** A host that cannot write a decision record for an operation MUST deny that operation with `permission_denied`.

**[HCS-0005-R013]** A host that sends decision records to its distribution SHOULD send each one as a CloudEvents 1.0.2 event whose `data` is the record, as HCS-0007-R021 does for observed state.

The schema checks the shape of a record, not that its members agree with each other (for example `outcome` `allow` with `decider.kind` `denial`); keeping them consistent is the host's job. A record whose `decider.kind` is `grant` names the grant; that grant's `origin` names the record of the person's answer that created it, so a decision made by a remembered answer can be traced back to the person and the moment.

## 4. Open questions

- OQ-0005-1: a minimal shared permission vocabulary (filesystem, network, process, secrets). Every harness needs one; defining it before a second consumer exists risks encoding the first host's choices. HCS-0010 settles how tools are grouped: an action type is a permission id, and one grant or denial covers every tool the host maps to it. It defines no ids, because the two consumers' ids are domain ones.
- OQ-0005-2: grant scope dimensions (subject, project, operation) named in the composition map. HCS-0005 defines `session` only (section 3.2).
- OQ-0005-3: whether requests live in the catalog manifest entry or in a document inside the package. A catalog-side list lets the host decide before fetching; a package-side list travels with the code.
- OQ-0005-4: whether a grant survives a package version change. The current rule (grants keyed by entry id) means it does; a host can still re-ask through policy.
- OQ-0005-5: mapping a grant to OAuth tokens when the operation calls a remote MCP server (TOOLS-1).
- OQ-0005-6: retention of decision records, and whether `decider.id` for a person is a pseudonymous subject id.
- OQ-0005-7: whether decision records stay on the host or go to a control plane, and the CloudEvents `type` and OpenTelemetry attributes for them (with OQ-0004-3).
