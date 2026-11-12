#!/usr/bin/env python3
"""Generate the HCS-0007 (DISTRO-1) conformance cases with fixture keys.

Usage: python3 conformance/runner/gen_distro1_cases.py [--check]

Fixture private keys are derived from public seeds (sha256 of a label) so the output is reproducible;
they are test keys only and protect nothing. Ed25519 signatures are deterministic, so running this
script twice writes identical files. --check exits 1 when a committed case differs from a fresh
generation. Host-level HCS-0007 cases are written by hand and are not touched.
"""
import base64
import copy
import hashlib
import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import distro1  # noqa: E402

ROOT = pathlib.Path(__file__).resolve().parents[2]
CASES = ROOT / "conformance" / "cases" / "HCS-0007"
ENV_T = "urn:hcs:schema:hcs-0007:signed-catalog:0.1#/$defs/envelope"
KS_T = "urn:hcs:schema:hcs-0007:signed-catalog:0.1#/$defs/keySet"
OCI = "application/vnd.oci.image.layer.v1.tar+gzip"
NOW = "2026-11-06T12:00:00Z"
ASSIGNMENT = {"tenant": "t-0001", "profile": "desktop", "channel": "stable"}


def seed(label):
    return hashlib.sha256(("hcs-distro-1 fixture key " + label).encode()).digest()


def b64(data):
    return base64.b64encode(data).decode()


def pub(label):
    return b64(distro1.public_key(seed(label)))


def sign(doc, payload_type, *labels, keyids=None):
    doc = {k: v for k, v in doc.items() if k != "signatures"}
    msg = distro1.signing_input(payload_type, doc)
    keyids = keyids or labels
    doc["signatures"] = [{"keyid": kid, "sig": b64(distro1.sign(seed(lab), msg))} for lab, kid in zip(labels, keyids)]
    return doc


def online(label, nb="2026-01-01T00:00:00Z", na="2027-01-01T00:00:00Z"):
    return {"keyid": label, "keytype": "ed25519", "public": pub(label), "notBefore": nb, "notAfter": na}


ANCHOR = {"keys": [{"keyid": "root-1", "keytype": "ed25519", "public": pub("root-1")}], "threshold": 1}


def key_set(keys=None, version=3, threshold=None, signers=("root-1",)):
    ks = {"hcsVersion": "0.1", "version": version, "keys": keys or [online("online-1")]}
    if threshold is not None:
        ks["threshold"] = threshold
    return sign(ks, distro1.KEY_SET_PAYLOAD_TYPE, *signers)


def catalog(*entries):
    entries = entries or ({"id": "dev.example.notes", "version": "1.4.0", "contentHash": "sha256:" + "4f" * 32,
                           "size": 18342, "mediaType": OCI,
                           "contributions": [{"kind": "skill", "path": "skills/take-notes"}]},)
    return {"hcsVersion": "0.1", "id": "dev.example.desktop", "version": "2026.11.06-1", "entries": list(entries)}


def envelope(sequence=42, expires="2026-11-13T00:00:00Z", binding=None, cat=None, signers=("online-1",), **extra):
    env = {"hcsVersion": "0.1", "sequence": sequence, "expires": expires, "binding": binding or dict(ASSIGNMENT),
           "catalog": cat or catalog()}
    env.update(extra)
    return sign(env, distro1.ENVELOPE_PAYLOAD_TYPE, *signers)


def state(doc, field):
    return {field: doc[field], "digest": distro1.content_digest(doc)}


def verify_input(env, ks=None, last=None, assignment=None, now=NOW):
    inp = {"check": "envelope", "now": now, "trustAnchor": ANCHOR, "keySet": ks or key_set(), "envelope": env,
           "assignment": assignment or dict(ASSIGNMENT)}
    if last is not None:
        inp["lastAccepted"] = last
    return inp


def key_set_input(ks, last=None, now=NOW, anchor=ANCHOR):
    inp = {"check": "key-set", "now": now, "trustAnchor": anchor, "keySet": ks}
    if last is not None:
        inp["lastKeySet"] = last
    return inp


def ok():
    return {"outcome": "valid"}


def bad(code):
    return {"outcome": "invalid", "errors": [{"code": code}]}


def cases():
    out = []

    def add(req, pol, name, description, level, inp, expect, target=None, text=None):
        case = {"description": description, "requirements": [req], "level": level}
        if target:
            case["target"] = target
        if text is not None:
            case["documentBase64"] = b64(text)
        if inp is not None:
            case["input"] = inp
        case["expect"] = expect
        out.append((req, pol, name, case))

    env = envelope()
    # R001 envelope shape
    add("HCS-0007-R001", "valid", "full-envelope", "A signed envelope with every member.", "document", env, ok(), ENV_T)
    e = copy.deepcopy(env); del e["sequence"]
    add("HCS-0007-R001", "invalid", "missing-sequence", "No sequence member.", "document", e, bad("manifest_invalid"), ENV_T)
    e = copy.deepcopy(env); e["expires"] = "2026-11-13T00:00:00+01:00"
    add("HCS-0007-R001", "invalid", "expires-with-offset", "expires must be UTC with Z.", "document", e, bad("manifest_invalid"), ENV_T)
    e = copy.deepcopy(env); e["signatures"][0]["sig"] = e["signatures"][0]["sig"][:-4] + "=="
    add("HCS-0007-R001", "invalid", "short-signature", "A signature that is not 64 bytes.", "document", e, bad("manifest_invalid"), ENV_T)
    e = copy.deepcopy(env); e["tenant"] = "t-0001"
    add("HCS-0007-R001", "invalid", "binding-member-at-top-level", "Binding members belong in binding, not at top level.", "document", e, bad("manifest_invalid"), ENV_T)
    e = copy.deepcopy(env); e["hcsVersion"] = "0.2"
    add("HCS-0007-R001", "invalid", "unsupported-hcs-version", "hcsVersion is read before the shape.", "document", e, bad("unsupported_hcs_version"), ENV_T)

    # R002 key set shape
    ks = key_set()
    add("HCS-0007-R002", "valid", "full-key-set", "A root-signed key set with one online key.", "document", ks, ok(), KS_T)
    k = copy.deepcopy(ks); k["keys"][0]["keytype"] = "rsa"
    add("HCS-0007-R002", "invalid", "rsa-key", "Only ed25519 keys.", "document", k, bad("manifest_invalid"), KS_T)
    k = copy.deepcopy(ks); k["keys"].append(dict(k["keys"][0], public=pub("online-2")))
    add("HCS-0007-R002", "invalid", "duplicate-keyid", "Two keys share a keyid.", "document", k, bad("manifest_invalid"), KS_T)
    k = copy.deepcopy(ks); k["keys"] = []
    add("HCS-0007-R002", "invalid", "no-keys", "A key set lists at least one key.", "document", k, bad("manifest_invalid"), KS_T)

    # R003 signature input
    small = {"hcsVersion": "0.1", "sequence": 1, "b": "z", "a": [1, True, None], "signatures": []}
    add("HCS-0007-R003", "valid", "signing-input", "PAE over sorted, whitespace-free JSON without signatures.", "distribution",
        {"check": "signing-input", "payloadType": distro1.ENVELOPE_PAYLOAD_TYPE, "document": small},
        {"signingInput": distro1.signing_input(distro1.ENVELOPE_PAYLOAD_TYPE, small).decode()})
    tricky = {"x-example": {"": 1, "\U0001f600": 2, "é": "\u000f\"\\"}, "sequence": 1}
    add("HCS-0007-R003", "invalid", "utf16-key-order", "Keys sort by UTF-16 code units: U+1F600 before U+E000, the reverse of code point order; control characters escape as \\u00XX.",
        "distribution", {"check": "signing-input", "payloadType": distro1.ENVELOPE_PAYLOAD_TYPE, "document": tricky},
        {"signingInput": distro1.signing_input(distro1.ENVELOPE_PAYLOAD_TYPE, tricky).decode()})
    unsigned = {k: v for k, v in env.items() if k != "signatures"}
    pretty = json.dumps(unsigned, indent=2).encode()
    wrong = dict(unsigned, signatures=[{"keyid": "online-1", "sig": b64(distro1.sign(
        seed("online-1"), distro1.pae(distro1.ENVELOPE_PAYLOAD_TYPE, pretty)))}])
    add("HCS-0007-R003", "invalid", "signed-over-pretty-json", "Signed over indented JSON instead of the canonical form.", "distribution",
        verify_input(wrong), bad("signature_invalid"))
    wrong = dict(unsigned, signatures=[{"keyid": "online-1", "sig": b64(distro1.sign(
        seed("online-1"), distro1.signing_input(distro1.KEY_SET_PAYLOAD_TYPE, unsigned)))}])
    add("HCS-0007-R003", "invalid", "wrong-payload-type", "Signed with the key-set payload type: domain separation rejects it.", "distribution",
        verify_input(wrong), bad("signature_invalid"))

    # R004 number domain
    e = envelope(**{"x-example-limit": 9007199254740991})
    add("HCS-0007-R004", "valid", "max-safe-integer", "The largest safe integer is in the domain.", "document", e, ok(), ENV_T)
    e = copy.deepcopy(env); e["x-example-ratio"] = 0.5
    add("HCS-0007-R004", "invalid", "fraction", "A non-integer number anywhere is rejected.", "document", e, bad("manifest_invalid"), ENV_T)
    e = copy.deepcopy(env); e["catalog"]["x-example-big"] = 9007199254740992
    add("HCS-0007-R004", "invalid", "beyond-safe-range", "2^53 inside the catalog is outside the domain.", "document", e, bad("manifest_invalid"), ENV_T)

    # R005 signatures and threshold
    add("HCS-0007-R005", "valid", "one-of-one", "One valid signature, threshold 1.", "distribution", verify_input(env), ok())
    ks2 = key_set([online("online-1"), online("online-2")], threshold=2)
    add("HCS-0007-R005", "valid", "two-of-two", "Two distinct keys meet threshold 2.", "distribution",
        verify_input(envelope(signers=("online-1", "online-2")), ks2), ok())
    add("HCS-0007-R005", "invalid", "unsigned", "No signatures.", "distribution",
        verify_input({k: v for k, v in env.items() if k != "signatures"}), bad("signature_invalid"))
    e = copy.deepcopy(env); e["sequence"] = 43
    add("HCS-0007-R005", "invalid", "content-changed-after-signing", "sequence edited after signing.", "distribution",
        verify_input(e), bad("signature_invalid"))
    add("HCS-0007-R005", "invalid", "unknown-key", "Signed by a key outside the key set.", "distribution",
        verify_input(envelope(signers=("online-2",))), bad("signature_invalid"))
    add("HCS-0007-R005", "invalid", "root-key-signs-catalog", "The root key signs key sets, not catalogs.", "distribution",
        verify_input(envelope(signers=("root-1",))), bad("signature_invalid"))
    twin = key_set([online("online-1"), dict(online("online-1"), keyid="online-1-alias")], threshold=2)
    add("HCS-0007-R005", "invalid", "same-key-twice", "One key listed under two keyids counts once; threshold 2 is not met.", "distribution",
        verify_input(sign(env, distro1.ENVELOPE_PAYLOAD_TYPE, "online-1", "online-1",
                          keyids=("online-1", "online-1-alias")), twin), bad("signature_invalid"))

    # R006 binding
    add("HCS-0007-R006", "valid", "binding-matches", "Binding equals the host assignment.", "distribution", verify_input(env), ok())
    add("HCS-0007-R006", "invalid", "other-channel", "Signed for channel beta; the host is assigned stable.", "distribution",
        verify_input(envelope(binding=dict(ASSIGNMENT, channel="beta"))), bad("permission_denied"))
    add("HCS-0007-R006", "invalid", "missing-member", "A binding without tenant does not match a host assigned to a tenant.", "distribution",
        verify_input(envelope(binding={"profile": "desktop", "channel": "stable"})), bad("permission_denied"))

    # R007 expiry
    add("HCS-0007-R007", "valid", "one-second-left", "One second before expiry.", "distribution",
        verify_input(envelope(expires="2026-11-06T12:00:01Z")), ok())
    add("HCS-0007-R007", "invalid", "expires-now", "expires equal to the current time is expired.", "distribution",
        verify_input(envelope(expires=NOW)), bad("signature_invalid"))
    add("HCS-0007-R007", "invalid", "frozen-catalog", "A validly signed catalog that expired a week ago (freeze attack).", "distribution",
        verify_input(envelope(expires="2026-10-30T00:00:00Z")), bad("signature_invalid"))

    # R008 sequence
    add("HCS-0007-R008", "valid", "newer", "sequence 42 after 41.", "distribution",
        verify_input(env, last={"sequence": 41, "digest": "sha256:" + "00" * 32}), ok())
    add("HCS-0007-R008", "valid", "same-content-again", "The same envelope fetched again is accepted.", "distribution",
        verify_input(env, last=state(env, "sequence")), ok())
    add("HCS-0007-R008", "invalid", "older", "sequence 42 after 43 (rollback).", "distribution",
        verify_input(env, last={"sequence": 43, "digest": "sha256:" + "00" * 32}), bad("rollback_rejected"))
    other = envelope(cat=catalog({"id": "dev.example.other", "version": "1", "contentHash": "sha256:" + "11" * 32, "size": 1,
                                  "mediaType": OCI, "contributions": [{"kind": "skill", "path": "s"}]}))
    add("HCS-0007-R008", "invalid", "reused-sequence", "sequence 42 again with a different catalog.", "distribution",
        verify_input(other, last=state(env, "sequence")), bad("rollback_rejected"))

    # R009 order
    add("HCS-0007-R009", "valid", "binding-before-expiry", "Wrong channel and expired: binding is checked first.", "distribution",
        verify_input(envelope(binding=dict(ASSIGNMENT, channel="beta"), expires="2026-10-30T00:00:00Z")), bad("permission_denied"))
    e = copy.deepcopy(env); e["sequence"] = 40
    add("HCS-0007-R009", "invalid", "signature-before-sequence", "Bad signature and an old sequence: only signature_invalid is reported.", "distribution",
        verify_input(e, last={"sequence": 41, "digest": "sha256:" + "00" * 32}), bad("signature_invalid"))

    # R010 catalog validated after the envelope
    add("HCS-0007-R010", "valid", "catalog-valid", "Envelope and catalog both valid.", "distribution", verify_input(env), ok())
    entry = catalog()["entries"][0]
    add("HCS-0007-R010", "invalid", "signed-duplicate-entry", "A correctly signed catalog with a repeated entry id still fails HCS-0001.", "distribution",
        verify_input(envelope(cat=catalog(entry, entry))), {"outcome": "invalid", "errors": [{"code": "duplicate_entry", "entry": entry["id"]}]})
    e2 = copy.deepcopy(entry); del e2["mediaType"]
    add("HCS-0007-R010", "invalid", "signed-invalid-catalog", "A correctly signed catalog without mediaType fails HCS-0001.", "distribution",
        verify_input(envelope(cat=catalog(e2))), bad("manifest_invalid"))

    # R011 key set root signatures
    add("HCS-0007-R011", "valid", "root-signed", "Key set signed by the enrolled root key.", "distribution", key_set_input(ks), ok())
    add("HCS-0007-R011", "invalid", "online-key-signs-key-set", "An online key cannot sign a key set.", "distribution",
        key_set_input(key_set(signers=("online-1",))), bad("signature_invalid"))
    anchor2 = {"keys": ANCHOR["keys"] + [{"keyid": "root-2", "keytype": "ed25519", "public": pub("root-2")}], "threshold": 2}
    add("HCS-0007-R011", "invalid", "root-threshold-not-met", "Trust anchor threshold 2, one root signature.", "distribution",
        key_set_input(ks, anchor=anchor2), bad("signature_invalid"))

    # R012 key set version
    add("HCS-0007-R012", "valid", "rotation", "Version 4 replaces version 3.", "distribution",
        key_set_input(key_set([online("online-2")], version=4), last=state(ks, "version")), ok())
    add("HCS-0007-R012", "invalid", "older-key-set", "Version 2 after version 3.", "distribution",
        key_set_input(key_set(version=2), last=state(ks, "version")), bad("rollback_rejected"))
    add("HCS-0007-R012", "invalid", "same-version-new-keys", "Version 3 again with other keys.", "distribution",
        key_set_input(key_set([online("online-2")], version=3), last=state(ks, "version")), bad("rollback_rejected"))

    # R013 no currently valid key
    add("HCS-0007-R013", "valid", "one-key-valid", "One expired key and one valid key.", "distribution",
        key_set_input(key_set([online("online-old", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z"), online("online-1")])), ok())
    add("HCS-0007-R013", "invalid", "all-keys-expired", "Every online key has expired.", "distribution",
        key_set_input(key_set([online("online-old", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z")])), bad("signature_invalid"))

    # R014 key validity window
    future = key_set([online("online-1"), online("online-2", nb=NOW)])
    add("HCS-0007-R014", "valid", "not-before-inclusive", "A key whose notBefore equals now already signs.", "distribution",
        verify_input(envelope(signers=("online-2",)), future), ok())
    edge = key_set([online("online-1"), online("online-2", nb="2026-01-01T00:00:00Z", na=NOW)])
    add("HCS-0007-R014", "invalid", "not-after-exclusive", "A key whose notAfter equals now no longer signs.", "distribution",
        verify_input(envelope(signers=("online-2",)), edge), bad("signature_invalid"))
    early = key_set([online("online-1"), online("online-2", nb="2026-12-01T00:00:00Z")])
    add("HCS-0007-R014", "invalid", "not-yet-valid", "A key that becomes valid next month does not sign today.", "distribution",
        verify_input(envelope(signers=("online-2",)), early), bad("signature_invalid"))
    rotation_cases(add)
    textual_cases(add)
    signature_list_cases(add)
    return out


def corrupt(sig):
    """A signature of the right length that does not verify."""
    raw = bytearray(base64.b64decode(sig))
    raw[0] ^= 0x01
    return b64(bytes(raw))


def rotation_cases(add):
    """R024 (signatures a host ignores) and R025 (the trust anchor never comes from a document)."""
    add("HCS-0007-R024", "valid", "key-set-old-and-new-root", "A key set signed by the enrolled root and by a new root the host does not hold yet.", "distribution",
        key_set_input(key_set(signers=("root-1", "root-2"))), ok())
    add("HCS-0007-R024", "valid", "envelope-unknown-signer", "An envelope signed by online-1 and by a key that is not in the key set.", "distribution",
        verify_input(envelope(signers=("online-1", "online-9"))), ok())
    two = key_set([online("online-1"), online("online-2")])
    e = envelope(signers=("online-1", "online-2"))
    e["signatures"][1]["sig"] = corrupt(e["signatures"][1]["sig"])
    add("HCS-0007-R024", "valid", "envelope-extra-signature-fails", "Threshold 1: a valid signature by online-1 and one by online-2 that does not verify.", "distribution",
        verify_input(e, two), ok())
    old = key_set([online("online-old", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z"), online("online-1")])
    add("HCS-0007-R024", "valid", "envelope-extra-signature-expired-key", "A valid signature by online-1 and one by a key of the set whose window has closed.", "distribution",
        verify_input(envelope(signers=("online-1", "online-old")), old), ok())
    add("HCS-0007-R024", "invalid", "key-set-only-new-root", "A key set signed only by a root the host does not hold.", "distribution",
        key_set_input(key_set(signers=("root-2",))), bad("signature_invalid"))
    both = key_set([online("online-1"), online("online-2")], threshold=2)
    add("HCS-0007-R024", "invalid", "ignored-signature-does-not-count", "Threshold 2: online-1 and a key that is not in the key set do not make two.", "distribution",
        verify_input(envelope(signers=("online-1", "online-9")), both), bad("signature_invalid"))

    claim = {"keys": [{"keyid": "root-2", "keytype": "ed25519", "public": pub("root-2")}]}
    ks = {"hcsVersion": "0.1", "version": 3, "keys": [online("online-1")], "x-example-trust-anchor": claim}
    add("HCS-0007-R025", "valid", "anchor-claim-ignored", "A key set signed by the enrolled root carries an extension that names another root; the host ignores it.", "distribution",
        key_set_input(sign(ks, distro1.KEY_SET_PAYLOAD_TYPE, "root-1")), ok())
    add("HCS-0007-R025", "invalid", "anchor-from-document", "A key set signed only by the root that its own extension names as trust anchor.", "distribution",
        key_set_input(sign(ks, distro1.KEY_SET_PAYLOAD_TYPE, "root-2")), bad("signature_invalid"))


def compact(doc):
    return json.dumps(doc, separators=(",", ":"), ensure_ascii=True)


def respell(doc, old, new):
    """Compact text of a signed document with one token respelled; the signature stays that of the plain document."""
    text = compact(doc)
    assert text.count(old) == 1, old
    return text.replace(old, new).encode()


def textual_envelope_input(check_input=None):
    inp = verify_input(None) if check_input is None else check_input
    del inp["envelope"]
    return inp


def textual_cases(add):
    """Cases whose document is carried as exact text (documentBase64): spelling, duplicate names, surrogates, versions."""
    def base(n=1, **more):
        return envelope(**{"x-example-n": n, "x-example-m": -12, "x-example-s": "a"}, **more)

    def ks_input(_unused, **kw):
        inp = key_set_input(None, **kw)
        del inp["keySet"]
        return inp

    plain = base()
    add("HCS-0007-R004", "valid", "text-plain-integers", "Plain integers in the received text, one negative: the signature verifies.", "distribution",
        textual_envelope_input(), ok(), text=compact(plain).encode())
    for name, value, spelling, why in (
            ("text-fraction-zero", 1, "1.0", "1.0 has an integral value but a fraction in its spelling."),
            ("text-exponent", 100, "1e2", "1e2 has an integral value but an exponent in its spelling."),
            ("text-exponent-negative", 1, "100e-2", "100e-2 has an integral value but an exponent in its spelling."),
            ("text-negative-zero", 0, "-0", "-0 is refused even though 0 is allowed.")):
        doc = base(value)
        add("HCS-0007-R004", "invalid", name, why + " The signature covers the plain integer, so only the spelling is wrong.", "distribution",
            textual_envelope_input(), bad("manifest_invalid"), text=respell(doc, f'"x-example-n":{value},', f'"x-example-n":{spelling},'))
    ks = key_set()
    add("HCS-0007-R004", "valid", "text-key-set-plain-integer", "A key set with plain integers in its text.", "distribution",
        ks_input(None), ok(), text=compact(ks).encode())
    add("HCS-0007-R004", "invalid", "text-key-set-fraction-zero", "version 3.0 in a key set: the same rule as for an envelope.", "distribution",
        ks_input(None), bad("manifest_invalid"), text=respell(ks, '"version":3,', '"version":3.0,'))
    add("HCS-0007-R004", "invalid", "text-document-level-exponent", "The spelling rule at document level: 1e2 for sequence.", "document", None,
        bad("manifest_invalid"), ENV_T, text=respell(envelope(sequence=100), '"sequence":100,', '"sequence":1e2,'))

    add("HCS-0007-R003", "valid", "text-no-duplicate-names", "The same member name in two different objects is not a duplicate.", "distribution",
        textual_envelope_input(), ok(), text=compact(base(binding=dict(ASSIGNMENT))).encode())
    add("HCS-0007-R003", "invalid", "text-duplicate-member-name", "A member name written twice in one object has no canonical form, whatever the signature says.", "distribution",
        textual_envelope_input(), bad("manifest_invalid"), text=respell(plain, '"x-example-n":1,', '"x-example-n":1,"x-example-n":1,'))
    add("HCS-0007-R003", "invalid", "text-duplicate-name-in-catalog", "A duplicated member name inside the embedded catalog.", "distribution",
        textual_envelope_input(), bad("manifest_invalid"), text=respell(plain, '"hcsVersion":"0.1","id"', '"hcsVersion":"0.1","hcsVersion":"0.1","id"'))
    add("HCS-0007-R003", "invalid", "text-unpaired-surrogate", "A string with the escape \\ud800 alone has no UTF-8 form, so no canonical form.", "distribution",
        textual_envelope_input(), bad("manifest_invalid"), text=respell(plain, '"x-example-s":"a"', '"x-example-s":"\\ud800"'))
    add("HCS-0007-R003", "valid", "text-surrogate-pair", "A surrogate pair escape is one character and has a canonical form.", "distribution",
        textual_envelope_input(), {"outcome": "invalid", "errors": [{"code": "signature_invalid"}]},
        text=respell(plain, '"x-example-s":"a"', '"x-example-s":"\\ud83d\\ude00"'))

    add("HCS-0007-R002", "invalid", "text-key-set-other-hcs-version", "A key set with hcsVersion 0.2 is unsupported_hcs_version, as an envelope is.", "distribution",
        ks_input(None), bad("unsupported_hcs_version"), text=respell(ks, '"hcsVersion":"0.1"', '"hcsVersion":"0.2"'))
    shapeless = {k: v for k, v in ks.items() if k != "keys"}
    add("HCS-0007-R002", "invalid", "text-key-set-version-before-shape", "hcsVersion 0.2 and no keys: the version is read before the shape.", "distribution",
        ks_input(None), bad("unsupported_hcs_version"), text=respell(shapeless, '"hcsVersion":"0.1"', '"hcsVersion":"0.2"'))
    # Reading order (section 5.2): readable, then hcsVersion, then R004, shape, R022, R023.
    v2 = ('"hcsVersion":"0.1","sequence"', '"hcsVersion":"0.2","sequence"')
    e_v2 = respell(plain, *v2)
    add("HCS-0007-R001", "invalid", "text-other-version-with-fraction", "hcsVersion 0.2 and a fraction: the version is read first, so unsupported_hcs_version, not manifest_invalid.", "distribution",
        textual_envelope_input(), bad("unsupported_hcs_version"), text=e_v2.replace(b'"x-example-n":1,', b'"x-example-n":1.5,'))
    add("HCS-0007-R001", "invalid", "text-other-version-with-repeated-keyid", "hcsVersion 0.2 and a repeated signature keyid: unsupported_hcs_version.", "distribution",
        textual_envelope_input(), bad("unsupported_hcs_version"),
        text=compact(dict(json.loads(e_v2), signatures=plain["signatures"] * 2)).encode())
    add("HCS-0007-R003", "invalid", "text-other-version-with-duplicate-name", "hcsVersion 0.2 and a duplicated member name: the document is not readable, so manifest_invalid.", "distribution",
        textual_envelope_input(), bad("manifest_invalid"), text=e_v2.replace(b'"x-example-n":1,', b'"x-example-n":1,"x-example-n":1,'))
    add("HCS-0007-R004", "invalid", "text-fraction", "hcsVersion 0.1 and 1.5: manifest_invalid for the number.", "distribution",
        textual_envelope_input(), bad("manifest_invalid"), text=respell(plain, '"x-example-n":1,', '"x-example-n":1.5,'))
    add("HCS-0007-R004", "invalid", "text-beyond-safe-range", "The plain integer 9007199254740992 is outside the range: R004, as for a spelling.", "distribution",
        textual_envelope_input(), bad("manifest_invalid"), text=respell(plain, '"x-example-n":1,', '"x-example-n":9007199254740992,'))
    ks_v2 = respell(ks, '"hcsVersion":"0.1"', '"hcsVersion":"0.2"')
    add("HCS-0007-R002", "invalid", "text-key-set-other-version-with-fraction", "A key set with hcsVersion 0.2 and version 3.5: unsupported_hcs_version.", "distribution",
        ks_input(None), bad("unsupported_hcs_version"), text=ks_v2.replace(b'"version":3,', b'"version":3.5,'))
    add("HCS-0007-R003", "invalid", "text-key-set-other-version-with-duplicate-name", "A key set with hcsVersion 0.2 and a duplicated member name: manifest_invalid.", "distribution",
        ks_input(None), bad("manifest_invalid"), text=ks_v2.replace(b'"version":3,', b'"version":3,"version":3,'))
    add("HCS-0007-R004", "invalid", "text-key-set-fraction", "A key set with hcsVersion 0.1 and version 3.5: manifest_invalid for the number.", "distribution",
        ks_input(None), bad("manifest_invalid"), text=respell(ks, '"version":3,', '"version":3.5,'))

    add("HCS-0007-R002", "invalid", "text-key-set-no-keys-same-version", "No keys with the supported version stays manifest_invalid.", "distribution",
        ks_input(None), bad("manifest_invalid"), text=compact(shapeless).encode())


def signature_list_cases(add):
    """R022 (one item per keyid in signatures) and R023 (limits), as parsed objects: the order of the list is the point."""
    env = envelope()
    good = env["signatures"][0]
    msg = distro1.signing_input(distro1.ENVELOPE_PAYLOAD_TYPE, {k: v for k, v in env.items() if k != "signatures"})
    wrong = {"keyid": "online-1", "sig": b64(distro1.sign(seed("online-2"), msg))}
    unsigned = {k: v for k, v in env.items() if k != "signatures"}
    add("HCS-0007-R022", "valid", "distinct-keyids", "Two signatures with different keyids.", "distribution",
        verify_input(envelope(signers=("online-1", "online-2")), key_set([online("online-1"), online("online-2")], threshold=2)), ok())
    add("HCS-0007-R022", "invalid", "invalid-signature-first", "keyid online-1 twice, the invalid signature first: manifest_invalid, not signature_invalid.", "distribution",
        verify_input(dict(unsigned, signatures=[wrong, good])), bad("manifest_invalid"))
    add("HCS-0007-R022", "invalid", "valid-signature-first", "keyid online-1 twice, the valid signature first: the document is refused all the same.", "distribution",
        verify_input(dict(unsigned, signatures=[good, wrong])), bad("manifest_invalid"))
    add("HCS-0007-R022", "invalid", "same-signature-twice", "The identical signature listed twice.", "distribution",
        verify_input(dict(unsigned, signatures=[good, good])), bad("manifest_invalid"))
    ks = key_set()
    root_good = ks["signatures"][0]
    root_wrong = {"keyid": "root-1", "sig": b64(distro1.sign(seed("online-1"), distro1.signing_input(
        distro1.KEY_SET_PAYLOAD_TYPE, {k: v for k, v in ks.items() if k != "signatures"})))}
    ks_unsigned = {k: v for k, v in ks.items() if k != "signatures"}
    add("HCS-0007-R022", "invalid", "key-set-valid-signature-first", "A key set with root-1 twice, the valid signature first.", "distribution",
        key_set_input(dict(ks_unsigned, signatures=[root_good, root_wrong])), bad("manifest_invalid"))
    add("HCS-0007-R022", "invalid", "key-set-invalid-signature-first", "A key set with root-1 twice, the invalid signature first.", "distribution",
        key_set_input(dict(ks_unsigned, signatures=[root_wrong, root_good])), bad("manifest_invalid"))

    two = key_set([online("online-1"), online("online-2")], threshold=2)
    both = envelope(signers=("online-1", "online-2"))
    add("HCS-0007-R023", "valid", "signatures-at-limit", "Two signatures with a host limit of two.", "distribution",
        dict(verify_input(both, two), limits={"maxSignatures": 2}), ok())
    add("HCS-0007-R023", "invalid", "signatures-over-limit", "Two valid signatures meet the threshold, but the host limit is one: manifest_invalid before any verification.", "distribution",
        dict(verify_input(both, two), limits={"maxSignatures": 1}), bad("manifest_invalid"))
    add("HCS-0007-R023", "valid", "keys-at-limit", "Two keys with a host limit of two.", "distribution",
        dict(key_set_input(two), limits={"maxKeys": 2}), ok())
    add("HCS-0007-R023", "invalid", "keys-over-limit", "Two keys with a host limit of one.", "distribution",
        dict(key_set_input(two), limits={"maxKeys": 1}), bad("manifest_invalid"))


def render(case):
    return json.dumps(case, indent=2, ensure_ascii=False) + "\n"


def main():
    drift = []
    for req, pol, name, case in cases():
        path = CASES / req / pol / f"{name}.json"
        text = render(case)
        if "--check" in sys.argv:
            if not path.exists() or path.read_text(encoding="utf-8") != text:
                drift.append(str(path.relative_to(ROOT)))
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
    if "--check" in sys.argv:
        if drift:
            sys.exit("HCS-0007 cases differ from a fresh generation:\n  " + "\n  ".join(drift))
        print("HCS-0007 generated cases up to date")
    else:
        print(f"wrote {len(cases())} HCS-0007 cases")


if __name__ == "__main__":
    main()
