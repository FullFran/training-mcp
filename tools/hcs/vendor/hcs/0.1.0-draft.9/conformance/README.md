# HCS conformance

Cases are data (`case.schema.json`), one file per case, in `cases/<document>/<requirement-id>/{valid,invalid}/`. The directory gives the polarity ([ADR-0001](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0001-requirement-ids-and-case-polarity.md)).

| Level | Executed by the reference runner | What it checks |
| --- | --- | --- |
| `document` | yes | a JSON document (`input`, or its exact bytes in `documentBase64`) against an HCS schema (`target`), plus semantic checks: `hcsVersion` first, duplicate entry ids, problem-details `type`/code equality, and that an error tool result's `structuredContent` validates against the tool's `outputSchema` (HCS-0006-R008, target `toolCallOutput`) |
| `integrity` | yes | `size` and `contentHash` against base64 bytes, then optional `contentId` against the bounded uncompressed tar stream (HCS-0001-R007/R018) |
| `resolution` | yes | the HCS-0003 algorithm: activation order and typed errors |
| `authority` | yes | the HCS-0005 evaluation: grants, stored denials (a matching denial wins) and session limits |
| `distribution` | yes | HCS-0007: signature input, key set and envelope verification with fixture keys and a stated clock |
| `projection` | yes | HCS-0008: the reference projection of a generation (`runner/projection.py`): errors per entry and the entries the host may report active |
| `recovery` | yes | HCS-0008 section 7.1: the reference decision after a restart (`runner/recovery.py`): from the persisted facts, the action (`start`, `health-check`, `discard-pending`, `return`, `stop-and-report`) and the generations a cleanup retains |
| `client` | yes | HCS-0006-R006, R009: the HCS error a client extracts from a JSON-RPC response (`input.response`, an error response or a `tools/call` response with a `result`) or an RFC 9457 problem (`input.problem`); `expect.hcsError` is the error or `null`. Client test suites can run the same fixtures against their own parser |
| `host` | no, reported `pending` | a scenario and expected behaviour that needs a running host |

## Run

```sh
# python-jsonschema >= 4.18 (uses the `referencing` registry)
python3 -m venv .venv && .venv/bin/pip install 'jsonschema>=4.18'
.venv/bin/python tools/extract_requirements.py --check
.venv/bin/python conformance/runner/run.py --selftest [--report report.json] [-v]
.venv/bin/python conformance/runner/gen_distro1_cases.py --check

# optional second validator for schema-only verdicts
npm i --prefix /some/dir ajv@8.17.1
node conformance/runner/xcheck-ajv.mjs . /some/dir
```

The runner exits non-zero on any failing or malformed case, and when a MUST/MUST NOT requirement lacks a `valid` or an `invalid` case, or a case names an unknown requirement (HCS-0000-R001/R002).

## Integrity cases (HCS-0001)

`input.entry` gives the expected `id`, `size`, `contentHash` and optional `contentId`; `input.contentBase64` contains the fetched compressed bytes. Transport verification precedes decompression. Without `contentId`, the integrity level retains transport-only behavior (the existing valid non-gzip fixture remains valid). With it, gzip/decompression-limit failures or a mismatched identity yield `integrity_mismatch`. This level hashes exact tar bytes; it never extracts an archive.

When `input.unpack` is `true`, the level also checks the archive members after those verifications (HCS-0001-R019 to R023) and expects `package_invalid` for a forbidden member, a tar that cannot be parsed or an exceeded host limit. `input.maxMembers` and `input.maxUnpackedBytes` set the host limits for the case; without them the runner uses 256 MiB of unpacked data and 10,000 members, which are runner defaults and not HCS numbers. Cases without `unpack` behave as before.

The reference runner bounds total decompressed output to 256 MiB + 10,000 × 1,023 + 1,024 bytes and hashes fixed-size chunks. Fixtures can set `input.maxUncompressedBytes` to a smaller nonnegative whole-stream cap to exercise boundaries without allocating large archives. The SDK conformance adapter applies the same fixture cap. The focused runner tests are `python3 -B -m unittest discover -s conformance/runner -p test_content_identity.py` (requires the same jsonschema dependency as the runner).

`contentId` is a draft schema addition, not a compatibility guarantee: older closed validators reject it. See HCS-0001 section 3.5 for migration and the authenticated catalog binding required for identity-based decisions.

## Distribution cases (HCS-0007)

`input.check` selects what is verified:

- `signing-input`: `payloadType` and `document`; `expect.signingInput` is the expected signature input as text, or `expect.outcome` is `invalid` when the document has no canonical form.
- `key-set`: `trustAnchor`, `keySet`, optional `lastKeySet` (`{version, digest}`) and `now`.
- `envelope`: the same plus `envelope`, `assignment` and optional `lastAccepted` (`{sequence, digest}`); the key set is verified first, then section 5.2 of HCS-0007 in order.

`input.limits` (optional, `{maxKeys, maxSignatures}`) states the bounds a host has set on the number of keys of a key set and of items of `signatures` (HCS-0007-R023); without it no bound applies.

### Cases that carry the document as text

Number spelling, a duplicated member name and an unpaired surrogate (HCS-0007 section 4, R003, R004) cannot be told apart once a document is a parsed JSON value, so a case may carry the exact bytes of the document in the optional member `documentBase64`:

- At `document` level it replaces `input`; `input` is then absent.
- At `distribution` level `input` keeps every other member and `documentBase64` replaces `input.envelope` (`check: envelope`) or `input.keySet` (`check: key-set`).
- A runner decodes the base64 and reads the bytes in the order of HCS-0007 section 5.2: first the text must be readable (invalid UTF-8, a syntax error, a duplicated member name and an unpaired surrogate are `manifest_invalid`, R003); then `hcsVersion` (another version is `unsupported_hcs_version`, whatever else is wrong); then a number not spelled `-?(0|[1-9][0-9]*)` or outside the safe range (`manifest_invalid`, R004); then it continues as with a parsed document. The reference does this where the document is first read: the key set is read before the envelope.
- Base64 of the bytes was chosen over a JSON string because the case file is itself JSON: an unpaired surrogate cannot be a string value in every tool, a pretty-printer or a loader may merge a duplicated member name or rewrite `1.0`, and bytes are what a host receives. It is the same encoding as `contentBase64` at the integrity level. The text may use JSON escapes (`\ud800`), so a case stays easy to regenerate and to decode by hand: `base64 -d`.
- Existing cases have no `documentBase64` and are unchanged.

The cases are generated by `runner/gen_distro1_cases.py` from fixture keys derived from public seeds; they are test keys only. Ed25519 signatures are deterministic, so the generator is reproducible (`--check` fails on drift). Host-level HCS-0007 cases are written by hand. Ed25519, RFC 8785 and DSSE PAE are implemented in `runner/distro1.py` without dependencies; `--selftest` checks them against RFC 8032 test vector 1 ([ADR-0015](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0015-distro-1-is-a-single-envelope-tuf-profile.md)).

## Projection cases (HCS-0008)

`input` states the `generation` (entries in activation order with their contributions), `projectableKinds`, `hostOwned` engine-level names, `grantedSecrets` per entry and the `activationPolicy`; `expect` gives `errors` and `selected`. An `mcp-server` contribution with a `registryType: "file"` package also lists the regular `files` in its directory, and `hostRuntimes` lists the `runtimeHint` values the host provides (HCS-0008 6.4.1). Optional `clientLimits` (`{"client": ..., "toolListBytes": n}`, HCS-0008 6.9) with a `toolListBytes` figure the host measured on each `mcp-server` contribution: a server over the limit (R037), or with no figure while a limit is declared (R038), is reported `unsupported_contribution`. A skill projects the engine-level name `skill/<name>`, an MCP server `mcp/<server.json name>`; extension kinds list their `settings` with an `effect` (`add`, `replace`, `remove`, `widen`, `narrow`). The reference checks only the members `server.json` requires; full MCP Registry schema validation is the adapter's.

## Recovery cases (HCS-0008)

`input.persisted` states the facts a host reconstructs after a restart as generation ids (`active`, `confirmed`, `pending`, `failed`); `input.onDisk` lists the generations whose content is present. `expect.action` is one of `start`, `health-check`, `discard-pending`, `return`, `stop-and-report`; `generation`, `returnTo`, `retain` and `failed` are optional and compared only when present. No storage format is implied. `invalid/` cases state the action or the retained set of a non-conforming host; the runner passes them when the reference decision differs from `expect`. Writes and faults (a crash between the pending record and the pointer change, a pointer that cannot be replaced) need a running host and are `host` cases.

## Tool effect cases (HCS-0009)

No level of its own. `document` cases with target `urn:hcs:schema:hcs-0009:tool-effects:0.1#/$defs/serverMeta` check the `_meta["io.github.fullfran.hcs"]` member (R001). Cases with target `.../tool-effects:0.1#/$defs/classification` take `input.declared` (absent when the tool has no declaration) and `input.annotations`, the four MCP hints a host presents: the case is `valid` when the annotations are the ones the mapping gives (R003, R004) and `invalid` when they are not; the schema cannot copy a member, so each case spells out the expected hints. `authority` cases show that a declared class gives no grant (R006) and `host` cases describe what a host presents for a server's tools (R003, R005); the latter stay `pending`.

## System under test

A host in any language runs the same cases through an external command. The reference semantics of `runner/run.py` use the same interface, so a host and the reference are compared by the same code.

```sh
python3 conformance/runner/run.py [--cases DIR] [--timeout SECONDS] [--strict] [--report PATH] [-v] --sut COMMAND [ARG...]

# the reference semantics as a system under test (about 45 s: one process per case)
python3 conformance/runner/run.py --sut python3 conformance/runner/reference_sut.py
# a minimal example that implements one case kind (documentation, Node 18+)
python3 conformance/runner/run.py --sut node conformance/examples/minimal-sut.mjs
```

Everything after `--sut` is the command. It is started without a shell, with the arguments as given; the runner never puts case content on the command line. `--cases` selects another directory of cases (default `cases/`), `--timeout` the per-case limit (default 30 s), `--strict` makes `not-implemented` count as a failure for the exit code, and `--report` writes the per-case results as JSON.

### Process model

One process per case, started fresh: a case cannot depend on state left by an earlier one, a hang or crash affects only that case, and a host needs no read loop. The cost is process start-up, about 300 starts for the full set; a host with a slow start can keep the cost down with `--cases`. The runner writes one request to the command's standard input, closes it, and waits at most `--timeout` seconds; on timeout it kills the process. Standard error is not part of the protocol: the runner keeps its first 2,000 characters in the report and prints them when the case crashes. The environment and working directory are inherited.

Cases of level `host` are never sent: they need a running host and stay `pending`, as in the in-process run. A system that can run a host scenario reports it outside this protocol.

### Messages

The request is one JSON object, on standard input:

| Member | Meaning |
| --- | --- |
| `protocolVersion` | string, `"0.1"` |
| `case` | the case file exactly as it is on disk, including `expect`. The system MUST NOT read `expect` to compute its answer |

The result is one JSON object and nothing else on standard output (whitespace around it is allowed):

| Member | Meaning |
| --- | --- |
| `protocolVersion` | string, MUST equal the request's |
| `status` | `ok`, `not-implemented` (the system does not implement this case's level or kind) or `not-applicable` (the case does not apply to this system, for example it needs a capability the system does not offer) |
| `observed` | object, required for `ok`: what the system computed, shaped like the case's `expect` (below) |
| `reason` | optional string explaining `not-implemented` or `not-applicable` |

`observed` has the members that `expect` has for the case's level; the runner compares each member that `expect` states and ignores the others.

| Level | `observed` |
| --- | --- |
| `document`, `integrity` | `outcome` and `errors` |
| `distribution` | `outcome`, `errors`, and `signingInput` for `check: signing-input` |
| `resolution` | `activationOrder`, `errors`, and `selected` when the host has an `activationPolicy` |
| `authority` | `decision`: `{allow}` and, when denied, `error` with `code` |
| `projection` | `errors` and `selected` |
| `recovery` | the members of `expect` (`action` and the optional ones). An `invalid/` case states a non-conforming decision and passes when the observed one differs |
| `client` | `hcsError`: the error or `null` |

`errors` is compared as an unordered set of `code`, `entry` and `capability`. Worked example, for `cases/HCS-0005/HCS-0005-R001/valid/granted.json` (shortened):

```json
{"protocolVersion": "0.1", "case": {"level": "authority", "input": {"grants": [{"entry": "dev.example.a", "permission": "dev.example.fs.read"}], "check": {"entry": "dev.example.a", "permission": "dev.example.fs.read"}, "requests": []}, "expect": {"decision": {"allow": true}}}}
```

```json
{"protocolVersion": "0.1", "status": "ok", "observed": {"decision": {"allow": true}}}
```

### Outcomes and exit codes

| Outcome | Meaning | Exit code |
| --- | --- | --- |
| `pass` | `ok`, and the observed result satisfies `expect` | 0 |
| `fail` | `ok`, and the observed result does not satisfy `expect` | 1 |
| `crash` | the command did not start, timed out, exited non-zero, printed anything but one JSON object, or broke the message rules above (wrong `protocolVersion`, unknown `status`, `ok` without `observed`). A crash is not a failed expectation | 1 |
| `not-implemented`, `not-applicable` | the system declined the case. Never a pass | 0, or 1 with `--strict` |
| `pending` | a `host` case, not sent | 0 |
| `malformed` | the case file violates `case.schema.json` | 1 |

The summary prints one count per outcome, and `-v` lists every case. The exit code is 0 only when no case failed, crashed or is malformed (and, with `--strict`, none was `not-implemented`). A claim of conformance to a profile needs every case of its requirements to pass, so a non-zero `not-implemented` count means the profile is not claimed. The coverage check of the in-process run (HCS-0000-R001/R002) concerns the case set, not a system, and is not repeated here.

### Versioning

`protocolVersion` equals the case schema version, the last component of `urn:hcs:schema:conformance:case:0.1`. A change to the case schema that a system must handle (a new level, a new input form, a new `expect` member) changes both together. A system built for another version answers with its own `protocolVersion`, and the runner reports a crash for every case that names both versions.

### Reporting what a host does not implement

Answer `{"protocolVersion": "0.1", "status": "not-implemented", "reason": "..."}` for any case the system cannot evaluate, such as a level it has not built. Do not guess an `observed` result and do not exit non-zero: the first can pass by accident or fail for the wrong reason, the second is reported as a crash. Use `not-applicable` when the case is outside what this system claims to do. `conformance/examples/minimal-sut.mjs` implements the `authority` level and answers `not-implemented` for the others.

### Consumer kit

[`tools/consumer-kit/`](../tools/consumer-kit/README.md) is optional, non-normative tooling for a harness that vendors a release: it verifies the tarball against `SHA256SUMS`, records a baseline of this runner's `--report` output plus a `divergences.json` (with `notApplicable`), and fails only on regression. See also [docs/adoption.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adoption.md).

## Profiles

`profiles/core-0.json` lists the requirement ids of the CORE-0 draft profile. `profiles/distro-1.json` lists DISTRO-1 (HCS-0007), which builds on CORE-0 and fixes the host property `activationPolicy` to `all-or-nothing`. A host claims a profile by passing every case of every listed requirement, host-level cases included; no host does yet.

A requirement enters a profile when a host can pass all of its cases, host-level ones included. Until then it stays out, and the release `index.json` lists it under `unprofiled` (each requirement also carries its `profiles` and `cases` count), so a host that selects cases by profile can still see and track it. A new profile is defined when a host can pass it, not before (README rule 3).
