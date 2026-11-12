# HCS-0010 Proposals: agent writes that a person answers

| Field | Value |
| --- | --- |
| Status | Draft |
| Version | 0.1.0-draft |
| Requires | HCS-0005, HCS-0006 |
| Replaces | none |
| Schema | `schemas/hcs-0010/proposal/v0.1/proposal.schema.json` (`urn:hcs:schema:hcs-0010:proposal:0.1`) |

## 1. Scope

A harness that lets an agent change a person's data has to decide who approves the change. Two consuming harnesses reached the same answer on their own: the agent never writes, it records a **proposal**, and only a person (or an answer a person gave earlier and the host still holds) turns the proposal into a change. This document defines the proposal record, its states, how an answer links to the HCS-0005 decision record, and four behaviours both harnesses need: refusing an answer on state that has changed, retrying without duplicates, undoing as a new entry, and grouping tools under an action type ([ADR-0036](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0036-proposals-record-agent-writes.md)).

It does not define what a change is, how a host applies it, how a person is asked, or which action types exist. Those stay with the host and the distribution (README rule 4).

## 2. Objects

- **Action type:** an HCS-0005 permission id (a dotted id) that names a kind of change, for example `dev.example.sets.log`. Several tools can map to one action type; a tool maps to exactly one per call.
- **Proposal** (`proposal`): the host's record of one requested change. Members:

| Member | Required | Meaning |
| --- | --- | --- |
| `id` | yes | the host's identifier for the proposal |
| `actionType` | yes | the action type (a permission id) |
| `proposedBy` | yes | the entry id or the host's identifier for the agent that proposed it |
| `entry` | no | the package entry whose tool proposed it; absent for a host built-in tool |
| `target` | yes | an identifier of what the change touches, never its content |
| `payloadDigest` | yes | digest of the proposed change (`sha256:` or `sha512:`, as HCS-0001 `contentHash`); the host chooses what bytes it hashes |
| `base` | no | an identifier of the target's state the proposal was made against (a revision, a content digest, a version counter) |
| `key` | no | the identity the host used to recognise a retry (section 3.5) |
| `session` | no | the session the proposal was made in |
| `time` | yes | RFC 3339 UTC timestamp of the proposal |
| `state` | yes | `pending`, `accepted` or `rejected` |
| `answer` | when answered | `{ "decision": <id of the HCS-0005 decision record>, "by": "subject" \| "grant" \| "denial", "time": <timestamp> }` |
| `undoes` | no | the `id` of the accepted proposal this one undoes (section 3.4) |
| `x-*` | no | extension members |

The record holds the digest of the change, not the change. Where the host keeps the change so a person can read it is the host's choice. The decision record never holds it (HCS-0005-R011).

## 3. Requirements

### 3.1 Record and states

**[HCS-0010-R001]** A proposal record MUST validate against the HCS-0010 `proposal` schema.

**[HCS-0010-R002]** A host MUST NOT apply the change of a proposal unless its state is `accepted`.

**[HCS-0010-R003]** A host MUST move a proposal only from `pending` to `accepted` or to `rejected`, MUST do so once, and MUST refuse any later answer to the same proposal.

The schema allows an `answer` only when the state is not `pending`, and requires it then. A host that needs states of its own between `pending` and `accepted` (for example while it applies the change) keeps them internal or reports them in an extension member (OQ-0010-2).

### 3.2 Who answers

A proposal exists because the agent must not decide. HCS-0005-R015 already says a person's answer comes only from the host's own channel to that person; the rules below apply it to proposals. The answer a proposal gets is an HCS-0005 decision: a host that accepts under a stored `allow_always` grant, or rejects under a stored `reject_always` denial, answers by that grant or denial (`by` is `grant` or `denial`), which traces back to the person through its `origin` (HCS-0005-R006).

**[HCS-0010-R004]** A host MUST answer a proposal only from a person's answer taken under HCS-0005-R015 or from a grant or denial it holds under HCS-0005 section 3.2, and MUST NOT offer an agent, a tool or a package any operation that answers or undoes a proposal.

**[HCS-0010-R005]** A host MUST write the HCS-0005 decision record of an answer before it applies the change, MUST carry its `id` in the proposal's `answer.decision`, and MUST NOT apply the change when it cannot write the record (HCS-0005-R012).

### 3.3 Stale state

A person answers some time after the agent proposed. The target may have changed in between, by the person, by another proposal or by another agent. Applying the proposal to a state it was not made for can overwrite the change that happened meanwhile.

**[HCS-0010-R006]** When a proposal carries a `base` and the target's current state no longer matches it, a host MUST refuse to apply the proposal, MUST leave the target unchanged, and MUST tell the person that the target changed.

A refused proposal stays `pending`; the person can reject it, or the agent can propose again against the new state. Which code the refusal carries is OQ-0010-1. A proposal with no `base` is a change that does not depend on the previous state, for example one that only adds a record.

### 3.4 Undo

The person can take back an accepted change. An answered proposal is a historical fact, so the undo is not an edit of it: it is a proposal of its own, with `undoes` naming the first, answered like any other (a person who undoes from the host's own interface answers it in the same act). The undone proposal stays `accepted`; a host finds that it was undone by looking for an accepted proposal that undoes it.

**[HCS-0010-R007]** A host MUST record an undo as a new proposal whose `undoes` names an accepted proposal, and MUST NOT change, remove or re-open the record of the proposal it undoes.

**[HCS-0010-R008]** A host MUST refuse an undo of a proposal that is not `accepted`, that was already undone, or whose change was altered or relied on by a later change, and MUST leave the target unchanged.

How a host knows a later change got in the way is its own mechanism: the harnesses studied compute an inverse change that must still locate its context, or keep a before-image and refuse when a later row depends on it (OQ-0010-5).

**[HCS-0010-R009]** A host MUST NOT edit or remove an answered proposal record or the link to its decision record; every later event about it, such as an undo, is a new record.

### 3.5 Retries

An agent whose response was lost sends the same proposal again. The two harnesses recognise a retry differently: one derives the proposal's id from a hash of the target, its base and the change, the other asks the client for an idempotency key. HCS requires the behaviour and not the mechanism (OQ-0010-3).

**[HCS-0010-R010]** When a proposal arrives with the same identity as one the host already holds and the same action type, target and payload digest, the host MUST return the existing proposal and MUST NOT create another, whatever its state.

**[HCS-0010-R011]** When a proposal arrives with the same identity as one the host already holds but a different action type, target or payload digest, the host MUST refuse it and MUST NOT change the existing proposal.

Where the identity is the content hash, a different content has a different identity and R011 never applies; where it is a client key, R011 stops a reused key from silently dropping a new change.

### 3.6 Action types

**[HCS-0010-R012]** A host MUST decide a proposal under the HCS-0005 permission id equal to its `actionType`, so that a grant or denial for an action type covers every tool the host maps to it and no other action type.

An action type is therefore not a new kind of object: the check, the grant, the denial and the decision record are HCS-0005's, with `permission` set to the action type. The mapping from tool to action type is the host's (or the package's declaration under a future HCS version); HCS defines no action types and no vocabulary of permission ids (HCS-0005 OQ-0005-1). `entry` in those objects is the proposing entry; for a host built-in tool it is open (OQ-0010-4).

## 4. Open questions

- OQ-0010-1: closed. A host that reports a refusal under R003, R006, R008 or R011 as an HCS error uses the HCS-0006 code `proposal_conflict`, with `requirement` set to the HCS-0010 requirement it applies. Both consumers already refuse these cases with a distinct error of their own.
- OQ-0010-2: intermediate states. training-mcp keeps `applying` (the change is being written) and `failed` (applying raised an error); RE;write has neither. Both are about applying, not about who answered, so they stay out of the three states.
- OQ-0010-3: whether a shared identity mechanism is worth fixing for interop. A content hash needs a canonical form of the change; a client key needs the client to supply and keep one.
- OQ-0010-4: the `entry` for a proposal made through a host built-in tool. HCS-0005 grants are keyed by entry; training-mcp's stored answers are keyed by action type and session alone.
- OQ-0010-5: how undo detects later dependence, and whether a person may undo an undo. The two harnesses use different mechanisms; this document fixes only the refusal.
- OQ-0010-6: a standing "accept everything" setting. training-mcp models it as an account-level host grant that a person sets and an agent never can, every proposal it accepts still recorded; HCS-0005 has no grant that covers every permission.
- OQ-0010-7: reopening a decision. RE;write lets a person move a decided claim back to `proposed` with a history entry; here a decision is final and a new proposal takes its place.
- OQ-0010-8: how long a stored answer lasts. training-mcp ends session-limited answers after at most twelve hours; HCS-0005 has no expiry (see OQ-0005-2).
