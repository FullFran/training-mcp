# HCS spec map

What each document covers and when a harness needs it. The specs are authoritative; this map only routes.

| Spec | Covers | Needed when |
|---|---|---|
| [HCS-0000](../../../spec/HCS-0000-conventions.md) conventions | Requirement language, IDs, versioning, schema ids, `x-` extensions | Writing anything normative |
| [HCS-0001](../../../spec/HCS-0001-catalog-manifest.md) catalog manifest | Desired state of a distribution: packages, `contentHash`, `size`, contributions, `requires` | Publishing or consuming packages |
| [HCS-0002](../../../spec/HCS-0002-capability-ids.md) capability ids | `dev.example.search@1` grammar; match by capability, not by package | Declaring or requiring contracts |
| [HCS-0003](../../../spec/HCS-0003-resolution.md) resolution | Deterministic selection of providers before execution; fails closed | Dependency logic in a host |
| [HCS-0004](../../../spec/HCS-0004-lifecycle.md) lifecycle | Per-entry states and desired-set transitions | Hosts that run packages |
| [HCS-0005](../../../spec/HCS-0005-authority.md) authority | Requests, grants, denials, checks, decision records | Anything that gates an operation or asks a person |
| [HCS-0006](../../../spec/HCS-0006-errors.md) errors | Closed code set over JSON-RPC, HTTP (RFC 9457) and MCP tool results | Any error surface |
| [HCS-0007](../../../spec/HCS-0007-signed-catalog.md) signed catalog | Signed envelope, key sets, rollback and freeze protection, key lifecycle | Remote or updatable distributions |
| [HCS-0008](../../../spec/HCS-0008-host-adapter.md) host adapter (Provisional) | Projecting a generation into an engine: skills, `server.json`, secrets, host-owned settings, health, rollback | Starting or configuring an agent engine |
| [HCS-0009](../../../spec/HCS-0009-tool-surface.md) tool surface | Effect classes `read`, `additive`, `destructive` of MCP tools | Shipping an MCP server |
| [HCS-0010](../../../spec/HCS-0010-proposals.md) proposals | The record an agent write becomes and how a person answers it | An agent that changes a person's data |

## Explicitly out of scope of HCS

Permission vocabularies, policy languages, trust tiers, approval modes, action types, secret storage, engine process supervision, transport, enrollment and package signing. A harness decides these itself and documents them as product policy.

## Adopting a release

Pin one release tag and its tarball sha256, vendor the tree unedited, verify `SHA256SUMS` in CI, run the cases of the profile you implement (`conformance/profiles/`), and record deliberate differences. Upgrade in one pull request per release. See [adoption](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adoption.md).
