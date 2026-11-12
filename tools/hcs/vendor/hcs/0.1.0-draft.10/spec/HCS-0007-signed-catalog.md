# HCS-0007 Signed catalog distribution (DISTRO-1)

| Field | Value |
| --- | --- |
| Status | Draft |
| Version | 0.1.0-draft |
| Requires | HCS-0000, HCS-0001, HCS-0003, HCS-0004, HCS-0006 |
| Replaces | none |
| Schema | `schemas/hcs-0007/signed-catalog/v0.1/signed-catalog.schema.json` (`urn:hcs:schema:hcs-0007:signed-catalog:0.1`) |
| Profile | DISTRO-1 (`conformance/profiles/distro-1.json`) |

## 1. Scope

A catalog manifest (HCS-0001) says what a host should run; it does not say who published it, whether it is current, or whether it was meant for this host. This document defines how a distribution signs a catalog and how a host decides to accept it: a signed envelope around the catalog, a root-signed set of online keys, the verification order with its error codes, and how the host moves to the accepted set. It is the DISTRO-1 profile of [composition-map.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/research/composition-map.md) section 5.

It is derived from the signed envelope that the first host runs, adjusted where a neutral standard needs it ([lf-fit-distro-1-and-core-0.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/research/lf-fit-distro-1-and-core-0.md) section 2, row 7).

Out of scope: how a host is enrolled and receives its trust anchor and assignment; transport (any HTTP or file delivery); package signing (HCS-0001 `contentHash` already binds each package to the signed catalog); new contribution kinds (issue #3).

## 2. Relation to existing standards

HCS-0007 reuses, and does not restate:

| Need | Standard | How |
| --- | --- | --- |
| Rollback, freeze, threshold, key rotation, offline root | TUF 1.0.37 ([tuf.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/tuf.md)) | Same properties, one signed document instead of four roles. Mapping and differences: [lf-fit-distro-1-and-core-0.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/research/lf-fit-distro-1-and-core-0.md) section 3. HCS-0007 does not claim TUF conformance. |
| Domain separation of signatures | DSSE v1 pre-authentication encoding (PAE) | Signature input (section 4). |
| Canonical bytes | RFC 8785 (JSON Canonicalization Scheme) | Restricted to integers (section 4). |
| Signatures | Ed25519, RFC 8032 | The only key type in this version. |
| Timestamps | RFC 3339 | UTC with `Z`. |
| Audience binding | Uptane Director pattern ([uptane.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/uptane.md)) | `binding`, compared for equality. |
| Provenance | SLSA Provenance v1 in an in-toto Statement v1 ([slsa.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/slsa.md), [in-toto.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/in-toto.md)) | R020. |
| Reports | CloudEvents 1.0.2, RFC 9457 ([cloudevents.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/cloudevents.md), HCS-0006) | R021. |

## 3. Documents

### 3.1 Envelope

```json
{
  "hcsVersion": "0.1",
  "sequence": 42,
  "expires": "2026-11-13T00:00:00Z",
  "binding": { "tenant": "t-0001", "profile": "desktop", "channel": "stable" },
  "catalog": { "hcsVersion": "0.1", "id": "dev.example.desktop", "version": "2026.11.06-1", "entries": [] },
  "signatures": [ { "keyid": "online-1", "sig": "<base64 of 64 bytes>" } ]
}
```

| Member | Required | Type | Meaning |
| --- | --- | --- | --- |
| `hcsVersion` | yes | `"0.1"` | HCS contract of the envelope |
| `sequence` | yes | integer, 1 to 2^53-1 | publication counter; only increases (TUF `version`) |
| `expires` | yes | RFC 3339 UTC timestamp | the host rejects the envelope from this instant on |
| `binding` | yes | object of strings, at least one member | the assignment the envelope is addressed to; member names and values are distribution-defined |
| `catalog` | yes | object | an HCS-0001 catalog manifest, checked only after the envelope verifies |
| `signatures` | no | array of `{keyid, sig}` | Ed25519 signatures over the signature input; absent or empty means unsigned |
| `x-*` | no | any value in the canonical domain | extension members, covered by the signatures |

### 3.2 Key set

The root key signs a key set; the keys in it sign envelopes. Rotating online keys means the root signs a new key set with a higher `version`.

| Member | Required | Type | Meaning |
| --- | --- | --- | --- |
| `hcsVersion` | yes | `"0.1"` | HCS contract |
| `version` | yes | integer, 1 to 2^53-1 | only increases |
| `threshold` | no | integer, 1 to 255, default 1 | distinct online keys whose signatures an envelope needs |
| `keys` | yes | array of key, at least one, unique `keyid` | online keys |
| `signatures` | no | array of `{keyid, sig}` | root signatures |
| `x-*` | no | any value in the canonical domain | extension members |

A key has `keyid`, `keytype` (`ed25519`), `public` (32 bytes, base64), `notBefore` (inclusive) and `notAfter` (exclusive), both RFC 3339 UTC timestamps.

### 3.3 Trust anchor, assignment and accepted state

These are host inputs, not published documents; the schema defines their shape so conformance cases can state them.

- **Trust anchor:** the root public keys and their threshold (default 1), received at enrollment. Root keys sign key sets only. A host changes its trust anchor only by means outside this document, never from a document it verifies (R025, section 5.3.1).
- **Assignment:** the `binding` the host is enrolled for, received at enrollment.
- **Accepted state:** for the last accepted envelope, its `sequence` and the sha256 digest of its canonical serialization without `signatures`; for the last accepted key set, its `version` and the same digest. Two documents have the same content when these digests are equal, whoever signed them.

## 4. Signature input

The signature input of an envelope or key set is

```txt
PAE(payloadType, JCS(document without its "signatures" member))
```

where PAE is the DSSE v1 pre-authentication encoding (`"DSSEv1" SP LEN(type) SP type SP LEN(body) SP body`, lengths in ASCII decimal bytes), JCS is RFC 8785, and `payloadType` is `application/vnd.hcs.catalog-envelope+json` for envelopes and `application/vnd.hcs.key-set+json` for key sets. The two payload types keep a key-set signature from being accepted as an envelope signature, and the reverse.

JCS sorts object members by their UTF-16 code units, writes no whitespace, and escapes only `"`, `\` and control characters. Because every number in the canonical domain is an integer (R004), a number serialises as its decimal digits and no implementation needs the ECMAScript floating-point algorithm of RFC 8785 section 3.2.2.3. A document with an unpaired surrogate or a duplicated member name has no canonical form; a host rejects it as `manifest_invalid` (R003). A number whose spelling is not the one R004 allows is `manifest_invalid` as well, once the host has read `hcsVersion` (section 5.2).

## 5. Requirements

### 5.1 Documents

**[HCS-0007-R001]** A host MUST check an envelope's `hcsVersion` once the document is readable, and then its number spelling (R004) and its shape against the HCS-0007 envelope schema, all before verifying any signature, reporting `unsupported_hcs_version` or `manifest_invalid` as HCS-0001-R002 and HCS-0001-R001 do for catalogs.

**[HCS-0007-R002]** A host MUST check a key set's `hcsVersion` once the document is readable, before its number spelling and its shape, and report `unsupported_hcs_version` when it is not supported, as R001 requires for envelopes; a key set MUST then validate against the HCS-0007 key set schema and MUST NOT list two keys with the same `keyid`, and a host MUST report `manifest_invalid` otherwise.

**[HCS-0007-R003]** The signature input of an envelope or key set MUST be the DSSE v1 pre-authentication encoding of its payload type and of the RFC 8785 serialization of the document without its `signatures` member, as defined in section 4.

**[HCS-0007-R004]** An envelope or key set MUST NOT contain a number whose token in the received text is not spelled `-?(0|[1-9][0-9]*)` with a value between -(2^53-1) and 2^53-1, and `-0` is not allowed; a host MUST report `manifest_invalid` citing this requirement otherwise.

R004 judges the spelling of the token, not its value. A fraction or an exponent is refused even when the value is integral: `1.0`, `1e2`, `100e-2` and `-0` are all refused, and so is `1.0000000000000000001`. A signer that follows section 4 never writes those spellings, and a token rule needs no arithmetic, so two hosts cannot disagree about a value that one of them rounds. A host applies the rule to the text it received, not to a parsed value that has lost the spelling, and only after it has read `hcsVersion` (section 5.2).

R004 applies to every number in the document, at any depth, including numbers inside extension members and inside the embedded catalog. The signature covers the whole document except `signatures` (section 4), so a number outside the domain anywhere leaves the document without one canonical form that every implementation computes alike. Rejecting it does not conflict with HCS-0000-R003: the host rejects the document because it has no canonical form, not because it does not understand the extension member. The same holds for an unpaired surrogate or a duplicated member name inside an extension member.

### 5.2 Envelope verification

A host reads an envelope or a key set in this order and stops at the first failure.

1. The document is readable: valid UTF-8, valid JSON, no duplicated member name and no unpaired surrogate. Otherwise the host reports `manifest_invalid` (R003). A document that fails this has no readable `hcsVersion`.
2. `hcsVersion`. Another version is `unsupported_hcs_version` (R001, R002), even when the document also holds a number that breaks R004, has a wrong shape, repeats a `keyid` or exceeds a bound. A later contract may change those rules, so a host does not judge them for a version it does not implement.
3. The number rule R004, then the shape, then (for a key set) the unique key `keyid` of R002, then R022, then R023.

A host verifies an envelope in this order and stops at the first failure: (1) reading, version, number spelling and shape, R001, with the checks of R004, R022 and R023 in the order above; (2) signatures, R005; (3) binding, R006; (4) expiry, R007; (5) sequence, R008; (6) the catalog, R010. The current time is the host's clock; conformance cases state it explicitly.

**[HCS-0007-R005]** A host MUST NOT accept an envelope unless valid signatures from at least `threshold` distinct public keys of its accepted key set, each within its validity window, cover the signature input, and MUST report `signature_invalid` otherwise.

**[HCS-0007-R006]** A host MUST NOT accept an envelope whose `binding` is not exactly equal to the host's assignment (same member names, same values), and MUST report `permission_denied` otherwise.

**[HCS-0007-R007]** A host MUST NOT accept an envelope when the current time is equal to or later than `expires`, and MUST report `signature_invalid` otherwise.

**[HCS-0007-R008]** A host MUST NOT accept an envelope whose `sequence` is lower than the last accepted one, or equal to it with different content, and MUST report `rollback_rejected` otherwise.

**[HCS-0007-R009]** A host MUST apply the envelope checks in the order of section 5.2 and MUST report only the first check that fails.

**[HCS-0007-R010]** A host MUST validate the enclosed catalog as HCS-0001 requires, after every envelope check has passed, and MUST NOT accept an envelope whose catalog fails that validation.

An envelope equal in content to the last accepted one is accepted again; fetching the same envelope twice is not a rollback.

### 5.2.1 Signature lists and bounds

**[HCS-0007-R022]** An envelope or key set MUST NOT list two items of `signatures` with the same `keyid`; a host MUST report `manifest_invalid` and MUST do so before verifying any signature.

Validity does not depend on the order of the signatures: a list with an invalid and a valid signature for one `keyid` is refused in either order. TUF requires the `keyid` to be unique within `signatures` ([tuf.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/tuf.md), TUF 1.0.37, Document formats, Object format); HCS refuses the document instead of choosing which of the items to ignore.

**[HCS-0007-R023]** A host that limits the number of keys in a key set or of items in `signatures` MUST report `manifest_invalid` for a document that exceeds a limit and MUST do so before verifying any signature; HCS states no number, and a host MAY set such limits.

For an envelope the order of step (1) is: readable (R003), `hcsVersion`, R004, shape, R022, R023. A key set is checked in the same order, with R002 in place of R001, and its unique key `keyid` (R002) is judged with the shape, before R022 and R023. A document at a limit is within it.

### 5.3 Key set verification

**[HCS-0007-R011]** A host MUST NOT accept a key set unless valid signatures from at least the trust anchor's threshold of distinct root public keys cover its signature input, and MUST report `signature_invalid` otherwise.

**[HCS-0007-R012]** A host MUST NOT accept a key set whose `version` is lower than the last accepted one, or equal to it with different content, and MUST report `rollback_rejected` otherwise.

**[HCS-0007-R013]** A host MUST NOT accept a key set in which no key is within its validity window at the current time, and MUST report `signature_invalid` otherwise.

**[HCS-0007-R014]** A host MUST count a signature toward a threshold only when the current time is equal to or later than its key's `notBefore` and earlier than its `notAfter`.

**[HCS-0007-R024]** When a host counts signatures toward the threshold of R005 or R011, it MUST ignore a signature whose `keyid` names no key of the key set or trust anchor it verifies against, a signature that does not verify, and a signature whose key is outside its validity window (R014), and MUST NOT refuse the document because it carries such a signature when the signatures it counts meet the threshold.

R024 is what lets a distribution sign one key set with an old and a new root key (section 5.3.1): a host that holds only the old one counts the old signature and ignores the other. R022 and R023 still apply before any signature is counted, so a repeated `keyid` remains `manifest_invalid`. TUF counts signatures the same way: only signatures by the keys of the role, each key once ([tuf.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/tuf.md)).

**[HCS-0007-R025]** A host MUST NOT take its trust anchor, or any root key of it, from an envelope, a key set, an extension member or any other document it receives with them, and MUST verify every key set against the trust anchor it already holds.

**[HCS-0007-R026]** A host MUST record a key set as accepted only after every check of section 5.3 has passed; a host that refuses a key set MUST keep its accepted key set and its accepted state unchanged, and MUST go on verifying envelopes against that key set.

### 5.3.1 Rotating and revoking keys

This section is informative; it describes how the requirements above let a distribution change keys.

- **Online key rotation.** The root signs a key set with a higher `version` that adds the new key; once hosts have accepted it, envelopes can be signed with the new key, and a later version drops the old one. Overlapping validity windows (R014) avoid a gap.
- **Revocation.** HCS has no revocation list. A key is revoked by publishing a key set with a higher `version` that does not list it; from then on its signatures do not count (R005, R024). The content digest excludes `signatures` (section 3.3), so the distribution can re-sign the current envelope with a remaining key without raising its `sequence`, and a host accepts it as equal in content (section 5.2).
- **Root key replacement.** The trust anchor reaches a host outside this document (R025), for example at enrollment or with a host update. During the transition the distribution signs every key set with the threshold of the old and of the new trust anchor (R027), so hosts on either anchor accept it (R024). When every host it serves holds the new anchor, it publishes a key set signed by the new root only. A host still on the old anchor refuses that key set with `signature_invalid` and keeps its accepted key set (R026) until it is enrolled again. TUF reaches the same property with a chain of root documents, each signed by the threshold of the previous and of the new root ("Update the root role" in the TUF client workflow); HCS-0007 has no root document (OQ-0007-8).

### 5.4 Moving to the accepted set

**[HCS-0007-R015]** A host MUST record an envelope's `sequence` and content digest as accepted only after every check of section 5.2 has passed, and MUST keep the accepted state of envelopes and key sets across restarts.

**[HCS-0007-R016]** A host that claims DISTRO-1 MUST use the `all-or-nothing` activation policy of HCS-0003.

R016 governs the signed catalog and its resolution. An entry that an engine cannot take after resolution is reported and left inactive on that engine without failing the transition (HCS-0008-R017).

**[HCS-0007-R017]** A host MUST NOT modify the unpacked content of an active entry while moving to a new desired set; it MUST prepare the new set separately and switch to it as one step (HCS-0004-R003).

**[HCS-0007-R018]** A host that runs a health check after starting a new desired set MUST treat a failed check as a failed transition under HCS-0004-R003 and return to the previous set.

HCS-0008 section 7 refines R018 for hosts that start an agent engine: when a generation counts as confirmed, the one-rollback limit, and the error codes.

### 5.5 Deployment

**[HCS-0007-R019]** A distribution SHOULD keep root private keys offline and SHOULD NOT hold any private key on the endpoint that serves envelopes and key sets, so that compromising that endpoint cannot produce an acceptable envelope.

**[HCS-0007-R020]** A distribution SHOULD publish, for each package, SLSA Provenance v1 (`https://slsa.dev/provenance/v1`) in an in-toto Statement v1 in a DSSE envelope signed by an online key of its key set, with a subject digest equal to the entry's `contentHash`.

**[HCS-0007-R021]** A host that reports observed state to its distribution SHOULD send each report as a CloudEvents 1.0.2 event whose `data` is an HCS-0004 status document, and SHOULD carry errors over HTTP as RFC 9457 problem details as HCS-0006 section 4.2 defines.

**[HCS-0007-R027]** A distribution that replaces a root key SHOULD sign every key set it publishes during the transition with the threshold of both the old and the new trust anchor, until every host it serves holds the new trust anchor, and its signing tool SHOULD refuse to sign a key set with fewer root signatures than the previous version unless the operator asks for it explicitly.

**[HCS-0007-R028]** A distribution that learns an online key is compromised SHOULD publish a key set with a higher `version` that does not list it, and SHOULD re-sign the current envelope with a key that remains (section 5.3.1).

**[HCS-0007-R029]** A host SHOULD NOT let any input that a received document, a package or the serving endpoint can set replace its trust anchor, and a host in production SHOULD NOT hold a fixture root key of the conformance cases in its trust anchor; those keys are derived from public seeds and protect nothing.

Section 5.5 cannot be checked by a conformance case; it describes how a distribution keeps the guarantees of sections 5.2 and 5.3 meaningful.

## 6. Open questions

- OQ-0007-1: whether a TUF deployment that carries the catalog as a target with `custom` data (documented as a TUF POUF) should replace this envelope. Revisit at the quarterly kill review (README rule 6).
- OQ-0007-2: a recommended maximum lifetime for `expires`. Without a TUF timestamp role, freeze protection depends on it.
- OQ-0007-3: event type names for R021 reports (HCS-0004 OQ-0004-3).
- OQ-0007-4: whether hosts verify R020 provenance before activation, and which builder identities they trust.
- OQ-0007-5: key types besides Ed25519, and post-quantum signatures.
- OQ-0007-6: how a host recovers when every online key has expired and no newer key set is reachable (TUF fast-forward recovery has no equivalent here yet).
- OQ-0007-7: whether a host must refuse an Ed25519 public key of small order (a key whose multiples by the cofactor are the identity). With such a key, a signature can verify for more than one message, so the signature need not bind the document to a key holder; whether that matters depends on how a deployment issues keys. HCS-0007 does not say, and the reference implementation does not refuse them; no requirement is written until a second implementation or a deployment shows which behavior hosts need.
- OQ-0007-8: whether HCS-0007 should define a published root rotation chain, as TUF does with root documents each signed by the threshold of the previous and of the new root, so that a host reaches a new trust anchor without enrollment or a host update. Today a host that missed the overlap of R027 needs to be enrolled again.
- OQ-0007-9: whether a host must stop running, or may keep running, the desired set of an accepted envelope that no longer verifies once a key set removes its signing key, until a re-signed envelope arrives. TUF refreshes every role on each update; HCS-0007 does not say when a host verifies an accepted envelope again.
- OQ-0007-10: keys limited to one `binding` member, with accepted state kept per member. One harness limits each key to a tenant with an extension member and keeps its rollback state per tenant; no requirement is written until a second harness needs it (README rule 1).
