# HCS-0000 Conventions

| Field | Value |
| --- | --- |
| Status | Draft |
| Version | 0.1.0-draft |
| Requires | none |
| Replaces | none |

## 1. Scope

This document defines the conventions every other HCS document uses: requirement language, requirement identifiers, stability markers, versioning, schema identifiers and the extension namespace. It defines no runtime behaviour.

## 2. Requirement language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in HCS documents are to be interpreted as described in BCP 14 ([RFC 2119], [RFC 8174]) when, and only when, they appear in all capitals, as shown here. See [docs/standards/bcp14.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/standards/bcp14.md).

## 3. Requirement identifiers

- Every normative statement is written on its own line as `**[HCS-NNNN-RMMM]** <sentence>`, where `NNNN` is the document number and `MMM` a three-digit sequence local to that document.
- Each requirement describes one behaviour and has one level: the first BCP 14 keyword in the sentence. A sentence may repeat that keyword for the parts of the same behaviour (for example "MUST verify ... and MUST report ..."), but never mixes levels.
- Identifiers are never renumbered or reused. A removed requirement keeps its line with the text `Removed in <version>.`
- `tools/extract_requirements.py` extracts every requirement into `requirements/requirements.json` (`id`, `document`, `keyword`, `text`). The file is generated and checked in; CI regenerates it and fails on drift.

Requirements in this document:

**[HCS-0000-R001]** Every MUST or MUST NOT requirement on documents or hosts in an HCS document MUST be referenced by at least one conformance case that satisfies it and at least one conformance case that violates it.

**[HCS-0000-R002]** A conformance case MUST NOT reference a requirement identifier that does not exist in `requirements/requirements.json`.

R001 and R002 are process requirements on this repository, not on hosts. `python3 conformance/runner/run.py` enforces them on every run (HCS-0000 itself is exempt from R001), and `--selftest` checks the enforcement against synthetic inputs.

## 4. Stability markers

Each document carries a `Status` of `Draft`, `Candidate`, `Released` or `Deprecated`. A Released version is immutable; changes produce a new version of the same document number with a changelog entry. All documents are Draft until HCS 0.1 is released.

A document that defines something no existing standard covers carries, besides its `Status`, the fields `Provisional: yes` and `Superseded when`, which names the standard or event that would replace it. A Provisional document is a placeholder, not a claim that HCS owns the topic: when the named replacement exists, the document profiles it or is withdrawn (README rule 6).

## 5. Versioning

- The HCS specification is released as SemVer `0.MINOR.PATCH` until two independent hosts pass the same profile (README rule 3).
- Until a `0.MINOR.0` is released, its snapshots are published as SemVer pre-releases `0.MINOR.0-draft.N`. A pre-release carries Draft documents and is immutable once tagged.
- Within one `0.MINOR`, a new `draft.N` changes instance document schemas (catalog, envelope, key set, errors, requests, status) only additively: an instance valid under the previous `N` stays valid. Conformance cases, the case schema and the runner may change in any way.
- A change that makes a valid instance invalid, or changes what a valid instance means, moves to the next MINOR (`0.(MINOR+1).0-draft.1`) and to the matching `hcsVersion`, so a host can tell which contract an instance follows.
- Instance documents declare the HCS contract they follow in `hcsVersion` as `MAJOR.MINOR` (for example `"0.1"`). A patch release never changes a schema's accepted language.
- Package versions inside manifests are opaque strings unless an HCS document defines their semantics ([ADR-0004](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0004-opaque-package-versions.md)).

## 6. Schema identifiers

- Schemas are JSON Schema 2020-12 documents in `schemas/<document>/<name>/v<MAJOR.MINOR>/<name>.schema.json` and are the source of truth for document shape (README rule 2).
- Until the project controls a domain, every schema `$id` is a URN of the form `urn:hcs:schema:<document>:<name>:<MAJOR.MINOR>` ([ADR-0002](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0002-schema-ids-as-urns.md)).
- Instance documents identify their contract by `hcsVersion`, never by pointing `$schema` at the JSON Schema meta-schema.

## 7. Extension namespace

Distributions extend HCS documents only through extension members.

- An extension member name matches `^x-[a-z0-9]+(-[a-z0-9]+)*$`; the first segment after `x-` names the distribution (for example `x-example` or `x-example-trust`).
- HCS schemas reject unknown members that are not extension members (`additionalProperties: false` plus `patternProperties` for `^x-`).
- Where another protocol asks for reverse-DNS names for extensions (ACP `_meta` keys and `_`-prefixed methods, MCP `_meta`), HCS's own extensions use the namespace `io.github.fullfran.hcs`. It follows the GitHub-backed reverse-DNS convention the MCP Registry uses for publishers without a domain. Like the `urn:hcs:` schema identifiers ([ADR-0002](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0002-schema-ids-as-urns.md)), it is provisional until the project has a domain of its own ([ADR-0027](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0027-projection-over-acp.md)).

**[HCS-0000-R003]** A host MUST NOT reject a document because it contains extension members it does not understand.

**[HCS-0000-R004]** An extension member MUST NOT change the validity verdict a host reaches for the members defined by HCS.

R003 concerns the presence and meaning of an extension member, not its encoding: a rule that applies to a whole document still applies inside extension members. For example, a signed document with an out-of-range number inside an `x-` member is rejected under [HCS-0007](HCS-0007-signed-catalog.md) R004, because the signature input covers that member too.

R003 cases check that unknown extension members are accepted and that names outside the extension grammar are not. R004 cases check that an extension member cannot make an otherwise invalid document valid (for example an `x-` flag that claims to waive integrity checks).

## 8. Conformance cases

Cases live in `conformance/cases/<document>/<requirement-id>/{valid,invalid}/<name>.json` and validate against `conformance/case.schema.json`. The directory gives the polarity: `valid/` exercises the path where the requirement is satisfied, `invalid/` the path where it is violated or where a non-conforming implementation would produce a different result ([ADR-0001](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0001-requirement-ids-and-case-polarity.md)). A case names the `requirements` it exercises, its `level` (`document`, `integrity`, `resolution`, `authority`, `distribution`, `projection` or `host`) and its expected outcome. All levels except `host` are executed by the reference runner ([ADR-0008](https://github.com/FullFran/hcs/blob/v0.1.0-draft.9/docs/adr/0008-reference-runner.md)); `host` cases describe a scenario that needs a running host and engine and are reported as `pending` until a host adapter (HCS-0008) runs them.

## 9. Open questions

- OQ-0000-1: spec text license (README: Apache-2.0 candidate; MCP precedent splits code and docs).
- OQ-0000-2: canonical HTTPS base for schema `$id` values once a domain exists.

[RFC 2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC 8174]: https://www.rfc-editor.org/rfc/rfc8174
