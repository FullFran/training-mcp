#!/usr/bin/env python3
"""HCS reference conformance runner (document, integrity, resolution, authority, distribution, projection, recovery and client levels).

Usage:
  python3 conformance/runner/run.py [--report PATH] [--selftest] [-v]
  python3 conformance/runner/run.py [options] --sut COMMAND [ARG...]   (external system under test: see sut.py)

It validates every case in conformance/cases against conformance/case.schema.json, executes
document/integrity/resolution/authority/distribution/projection/recovery/client cases with the reference semantics below
(distribution: HCS-0007, implemented in distro1.py; projection: HCS-0008, in projection.py), reports host
cases as pending, and enforces HCS-0000-R001/R002 (case coverage) against
requirements/requirements.json. Exit code 0 only when every executed case passes and coverage holds.

Validator: python-jsonschema >= 4.18 (Draft202012Validator with a `referencing` registry; older
releases mis-resolve relative $refs under urn: base URIs). Formats are not asserted, as in the
2020-12 default (format-annotation).
"""
import base64
import hashlib
import gzip
import io
import json
import pathlib
import sys
import zlib

from jsonschema import Draft202012Validator
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import distro1  # noqa: E402
import package  # noqa: E402
import projection  # noqa: E402
import recovery  # noqa: E402

ROOT = pathlib.Path(__file__).resolve().parents[2]
SUPPORTED_HCS = {"0.1"}
CATALOG = "urn:hcs:schema:hcs-0001:catalog-manifest:0.1"
ERROR = "urn:hcs:schema:hcs-0006:error:0.1"
RESOLUTION = "urn:hcs:schema:hcs-0003:resolution:0.1"
AUTHORITY = "urn:hcs:schema:hcs-0005:authority:0.1"
CASE = "urn:hcs:schema:conformance:case:0.1"
SIGNED = "urn:hcs:schema:hcs-0007:signed-catalog:0.1"
ENVELOPE = SIGNED + "#/$defs/envelope"
KEY_SET = SIGNED + "#/$defs/keySet"
COVERAGE_EXEMPT = {"HCS-0000-R001", "HCS-0000-R002"}


def load_store():
    store = {}
    paths = list((ROOT / "schemas").rglob("*.schema.json")) + [ROOT / "conformance" / "case.schema.json"]
    for path in paths:
        schema = json.loads(path.read_text(encoding="utf-8"))
        Draft202012Validator.check_schema(schema)
        store[schema["$id"]] = schema
    return store


STORE = load_store()
REGISTRY = Registry().with_resources(
    (uri, Resource.from_contents(schema, default_specification=DRAFT202012)) for uri, schema in STORE.items())


def validator_for(uri):
    return Draft202012Validator({"$ref": uri}, registry=REGISTRY)


def schema_valid(uri, instance):
    return not any(True for _ in validator_for(uri).iter_errors(instance))


def err(code, entry=None, capability=None):
    e = {"code": code}
    if entry:
        e["entry"] = entry
    if capability:
        e["capability"] = capability
    return e


# ---------- document level (HCS-0001, HCS-0006) ----------

def check_catalog(doc):
    if isinstance(doc, dict) and isinstance(doc.get("hcsVersion"), str) and doc["hcsVersion"] not in SUPPORTED_HCS:
        return [err("unsupported_hcs_version")]
    if not schema_valid(CATALOG, doc):
        return [err("manifest_invalid")]
    errors, seen = [], set()
    for entry in doc["entries"]:
        if entry["id"] in seen:
            errors.append(err("duplicate_entry", entry["id"]))
        seen.add(entry["id"])
    return errors


def check_document(target, doc, limits=None, violation=None):
    """limits: optional host bounds {"maxKeys", "maxSignatures"} (HCS-0007-R023).

    violation: the number-token violation (R004) found in the received text, if any. Order for an
    envelope or key set (HCS-0007 section 5.2): hcsVersion, R004, shape, repeated key keyid (R002),
    R022, R023.
    """
    if target == CATALOG:
        if violation:
            raise ValueError("textual catalogs are not supported")
        return check_catalog(doc)
    if target in (ENVELOPE, KEY_SET) and isinstance(doc, dict) and isinstance(doc.get("hcsVersion"), str) \
            and doc["hcsVersion"] not in SUPPORTED_HCS:
        return [err("unsupported_hcs_version")]
    if violation:
        return [err("manifest_invalid")]
    if not schema_valid(target, doc):
        return [err("manifest_invalid")]
    if target == KEY_SET and len({k["keyid"] for k in doc["keys"]}) != len(doc["keys"]):
        return [err("manifest_invalid")]
    if target in (ENVELOPE, KEY_SET):
        ids = [s["keyid"] for s in doc.get("signatures", [])]
        if len(set(ids)) != len(ids):  # HCS-0007-R022, before any signature is verified
            return [err("manifest_invalid")]
        limits = limits or {}
        if len(ids) > limits.get("maxSignatures", len(ids)) or (
                target == KEY_SET and len(doc["keys"]) > limits.get("maxKeys", len(doc["keys"]))):  # R023
            return [err("manifest_invalid")]
    if target == ERROR + "#/$defs/problemDetails" and doc["type"] != "urn:hcs:error:" + doc["hcsError"]["code"]:
        return [err("manifest_invalid")]
    if target == ERROR + "#/$defs/toolCallOutput" and "outputSchema" in doc:  # HCS-0006-R008
        if not Draft202012Validator(doc["outputSchema"]).is_valid(doc["result"]["structuredContent"]):
            return [err("manifest_invalid")]
    return []


# ---------- integrity level (HCS-0001-R007/R018; member safety R019-R023 when input.unpack is true) ----------

MAX_UNCOMPRESSED_BYTES = 256 * 1024 * 1024 + 10000 * 1023 + 1024

def check_integrity(inp):
    entry, data = inp["entry"], base64.b64decode(inp["contentBase64"], validate=True)
    algo, _, want = entry["contentHash"].partition(":")
    if len(data) != entry["size"] or hashlib.new(algo, data).hexdigest() != want:
        return [err("integrity_mismatch", entry["id"])]
    if "contentId" in entry:
        identity = entry["contentId"]
        if not schema_valid(CATALOG + "#/$defs/contentHash", identity) or identity.endswith("\n") or not data.startswith(b"\x1f\x8b"):
            return [err("integrity_mismatch", entry["id"])]
        algorithm, _, want = identity.partition(":")
        cap = inp.get("maxUncompressedBytes", MAX_UNCOMPRESSED_BYTES)
        if type(cap) is not int or cap < 0 or cap > MAX_UNCOMPRESSED_BYTES:
            raise ValueError("invalid decompression limit")
        # Stream through a fixed buffer, with a one-byte overflow probe. GzipFile
        # validates trailers and handles concatenated gzip members as one stream.
        digest, total = hashlib.new(algorithm), 0
        try:
            with gzip.GzipFile(fileobj=io.BytesIO(data)) as stream:
                while True:
                    chunk = stream.read(min(65536, cap - total + 1))
                    if not chunk:
                        break
                    total += len(chunk)
                    if total > cap:
                        return [err("integrity_mismatch", entry["id"])]
                    digest.update(chunk)
        except (OSError, EOFError, zlib.error):
            return [err("integrity_mismatch", entry["id"])]
        if digest.hexdigest() != want:
            return [err("integrity_mismatch", entry["id"])]
    if inp.get("unpack"):
        found = package.check_archive(data, inp.get("maxUnpackedBytes", 256 * 1024 * 1024), inp.get("maxMembers", 10000))
        if found:
            return [err("package_invalid", entry["id"])]
    return []


# ---------- resolution level (HCS-0003) ----------

def platform_matches(item, host):
    if item["os"] != host["os"] or item["architecture"] != host["architecture"]:
        return False
    return "variant" not in item or item["variant"] == host.get("variant")


def resolve(inp):
    if not schema_valid(RESOLUTION + "#/$defs/resolutionInput", inp):
        raise ValueError("resolution input does not match its schema")
    catalog, host = inp["catalog"], inp["host"]
    entries = {e["id"]: e for e in catalog["entries"]}
    errors, failed = [], set()
    for eid, e in entries.items():
        if e["mediaType"] not in host["mediaTypes"] or any(
                c["kind"] not in host["contributionKinds"] for c in e["contributions"]):
            errors.append(err("unsupported_contribution", eid)); failed.add(eid)
        elif "platforms" in e.get("compatibility", {}) and not any(
                platform_matches(p, host["platform"]) for p in e["compatibility"]["platforms"]):
            errors.append(err("platform_incompatible", eid)); failed.add(eid)
        elif "hosts" in e.get("compatibility", {}) and not any(
                h["id"] == host.get("id") and host.get("version") in h["versions"] for h in e["compatibility"]["hosts"]):
            errors.append(err("version_incompatible", eid)); failed.add(eid)

    def provides(eid):
        return {r for c in entries[eid]["contributions"] for r in c.get("provides", [])}

    builtins = set(host["builtins"])
    edges = {}
    while True:
        live = [eid for eid in entries if eid not in failed]
        changed = False
        edges = {}
        for eid in live:
            edges[eid] = set()
            for ref in entries[eid].get("requires", []):
                cands = [p for p in live if p != eid and ref in provides(p)]
                count = len(cands) + (1 if ref in builtins else 0)
                if count == 1:
                    edges[eid].update(cands)
                    continue
                if count > 1:
                    code = "ambiguous_provider"
                else:
                    cid = ref.rsplit("@", 1)[0]
                    others = builtins | {r for p in live if p != eid for r in provides(p)}
                    code = "version_incompatible" if any(r.rsplit("@", 1)[0] == cid for r in others) else "missing_dependency"
                errors.append(err(code, eid, ref)); failed.add(eid); changed = True
                break
        if changed:
            continue
        cyclic = find_cycle_members(edges)
        if not cyclic:
            break
        for eid in sorted(cyclic):
            errors.append(err("dependency_cycle", eid)); failed.add(eid)
    order, done = [], set()
    remaining = set(edges)
    while remaining:
        ready = sorted((e for e in remaining if edges[e] <= done), key=lambda s: s.encode())
        nxt = ready[0]
        order.append(nxt); done.add(nxt); remaining.discard(nxt)
    result = {"activationOrder": order, "errors": errors}
    policy = host.get("activationPolicy")
    if policy is not None:
        result["selected"] = [] if policy == "all-or-nothing" and errors else list(order)
    return result


def find_cycle_members(edges):
    index, low, stack, on, out, counter = {}, {}, [], set(), set(), [0]

    def strong(v):
        index[v] = low[v] = counter[0]; counter[0] += 1
        stack.append(v); on.add(v)
        for w in edges[v]:
            if w not in index:
                strong(w); low[v] = min(low[v], low[w])
            elif w in on:
                low[v] = min(low[v], index[w])
        if low[v] == index[v]:
            comp = []
            while True:
                w = stack.pop(); on.discard(w); comp.append(w)
                if w == v:
                    break
            if len(comp) > 1 or v in edges[v]:
                out.update(comp)

    for v in sorted(edges):
        if v not in index:
            strong(v)
    return out


# ---------- authority level (HCS-0005) ----------

def evaluate(inp):
    if not schema_valid(AUTHORITY + "#/$defs/evaluationInput", inp):
        raise ValueError("authority input does not match its schema")
    chk = inp["check"]

    def matches(stored):  # HCS-0005 section 2; a session-limited record matches only its session (R008)
        return stored["entry"] == chk["entry"] and stored["permission"] == chk["permission"] and (
            "session" not in stored or stored["session"] == chk.get("session"))

    if any(matches(d) for d in inp.get("denials", [])):  # a matching denial wins (R007)
        return {"allow": False, "error": {"code": "permission_denied", "entry": chk["entry"],
                                          "message": f"{chk['entry']} has a stored denial for {chk['permission']}"}}
    if any(matches(g) for g in inp["grants"]):
        return {"allow": True}
    return {"allow": False, "error": {"code": "permission_denied", "entry": chk["entry"],
                                      "message": f"{chk['entry']} has no grant for {chk['permission']}"}}


# ---------- client level (HCS-0006-R006) ----------

def extract_hcs_error(inp):
    """The HCS error a client takes from a response, or None. Only hcsError counts, never other members."""
    if "problem" in inp:
        problem = inp["problem"]
        hcs = problem.get("hcsError")
        # only hcsError and type count; other members of the wrong type are ignored (RFC 9457 section 3.1)
        if schema_valid(ERROR, hcs) and problem.get("type") == "urn:hcs:error:" + hcs["code"]:
            return hcs
        return None
    response = inp["response"]
    if schema_valid(ERROR + "#/$defs/jsonrpcErrorResponse", response):
        return response["error"]["data"]["hcsError"]
    # an MCP tools/call response: only an isError result with a valid structuredContent.hcsError (R009)
    result = response.get("result") if isinstance(response, dict) else None
    if isinstance(result, dict) and result.get("isError") is True and isinstance(result.get("structuredContent"), dict):
        hcs = result["structuredContent"].get("hcsError")
        if schema_valid(ERROR, hcs):
            return hcs
    return None


# ---------- distribution level (HCS-0007) ----------

def parse_received(data):
    """(document, errors, number violation): the received bytes read strictly (HCS-0007 R003, R004).

    Unreadable bytes give manifest_invalid at once (stage 1). A number violation is returned for
    check_document to report after the version has been read (stage 2); errors never quote the bytes.
    """
    try:
        value, violation = distro1.parse_text(data)
        return value, [], violation
    except distro1.StrictJsonError:
        return None, [err("manifest_invalid")], None


def run_distribution(inp, document=None):
    """Returns (errors, signing input or None). See conformance/README.md for the input shapes.

    document: the exact bytes of the envelope (check envelope) or key set (check key-set) when the case
    carries them as text (documentBase64); they are parsed where the document is first read.
    """
    check = inp["check"]
    limits = inp.get("limits")
    if check == "signing-input":
        try:
            return [], distro1.signing_input(inp["payloadType"], inp["document"]).decode("utf-8")
        except distro1.CanonicalError:
            return [err("manifest_invalid")], None
    now, anchor = inp["now"], inp["trustAnchor"]
    if not schema_valid(SIGNED + "#/$defs/trustAnchor", anchor):
        raise ValueError("trust anchor does not match its schema")
    key_set, violation = inp.get("keySet"), None
    if document is not None and check == "key-set":
        key_set, errors, violation = parse_received(document)
        if errors:
            return errors, None
    errors = check_document(KEY_SET, key_set, limits, violation)
    if errors:
        return errors, None
    code = distro1.check_key_set(key_set, anchor, inp.get("lastKeySet"), now)
    if code or check == "key-set":
        return ([err(code)] if code else []), None
    envelope, violation = inp.get("envelope"), None
    if document is not None:
        envelope, errors, violation = parse_received(document)
        if errors:
            return errors, None
    errors = check_document(ENVELOPE, envelope, limits, violation)
    if errors:
        return errors, None
    code = distro1.check_envelope(envelope, key_set, inp["assignment"], inp.get("lastAccepted"), now)
    if code:
        return [err(code)], None
    return check_catalog(envelope["catalog"]), None


# ---------- case execution ----------

def norm(errors):
    return sorted(json.dumps({k: e[k] for k in ("code", "entry", "capability") if k in e}, sort_keys=True) for e in errors)


def run_case(case, polarity="valid"):
    level, inp, exp = case["level"], case.get("input"), case["expect"]
    document = base64.b64decode(case["documentBase64"], validate=True) if "documentBase64" in case else None
    if level == "host":
        return "pending", None
    if level in ("document", "integrity"):
        errors, violation = [], None
        if document is not None:  # the received text first: an unreadable document has no hcsVersion
            inp, errors, violation = parse_received(document)
        if not errors:
            errors = check_document(case["target"], inp, violation=violation) if level == "document" else check_integrity(inp)
        outcome = "invalid" if errors else "valid"
        ok = outcome == exp["outcome"] and ("errors" not in exp or norm(errors) == norm(exp["errors"]))
        return ("pass" if ok else "fail"), {"outcome": outcome, "errors": errors}
    if level == "distribution":
        errors, signing = run_distribution(inp, document)
        got = {"outcome": "invalid" if errors else "valid", "errors": errors}
        if signing is not None:
            got["signingInput"] = signing
        ok = ("outcome" not in exp or got["outcome"] == exp["outcome"]) and (
            "errors" not in exp or norm(errors) == norm(exp["errors"])) and (
            "signingInput" not in exp or signing == exp["signingInput"])
        return ("pass" if ok else "fail"), got
    if level == "resolution":
        got = resolve(inp)
        assert schema_valid(RESOLUTION + "#/$defs/resolutionResult",
                            dict(got, errors=[dict(e, message=e["code"]) for e in got["errors"]]))
        ok = got["activationOrder"] == exp["activationOrder"] and norm(got["errors"]) == norm(exp["errors"]) and (
            "selected" not in exp or got.get("selected") == exp["selected"])
        return ("pass" if ok else "fail"), got
    if level == "authority":
        got = evaluate(inp)
        assert schema_valid(AUTHORITY + "#/$defs/decision", got)
        ok = got["allow"] == exp["decision"]["allow"] and (
            got["allow"] or got["error"]["code"] == exp["decision"]["error"]["code"])
        return ("pass" if ok else "fail"), got
    if level == "client":
        got = extract_hcs_error(inp)
        return ("pass" if got == exp["hcsError"] else "fail"), got
    if level == "projection":
        got = projection.project(inp)
        ok = norm(got["errors"]) == norm(exp["errors"]) and got["selected"] == exp["selected"]
        return ("pass" if ok else "fail"), got
    if level == "recovery":
        got = recovery.recover(inp)
        # An invalid case states the decision a non-conforming host takes; the reference must differ.
        ok = all(got[k] == v for k, v in exp.items()) == (polarity == "valid")
        return ("pass" if ok else "fail"), got
    raise ValueError(level)


def coverage(requirements, refs):
    """HCS-0000-R001/R002. refs: list of (case path, requirement id, polarity)."""
    problems = []
    known = {r["id"] for r in requirements}
    for path, rid, _ in refs:
        if rid not in known:
            problems.append(f"{path}: unknown requirement {rid} (HCS-0000-R002)")
    for r in requirements:
        if r["keyword"] in ("MUST", "MUST NOT") and r["id"] not in COVERAGE_EXEMPT:
            for pol in ("valid", "invalid"):
                if not any(rid == r["id"] and p == pol for _, rid, p in refs):
                    problems.append(f"{r['id']}: no {pol} case (HCS-0000-R001)")
    return problems


def selftest():
    reqs = [{"id": "HCS-0009-R001", "keyword": "MUST"}, {"id": "HCS-0009-R002", "keyword": "SHOULD"}]
    assert coverage(reqs, [("a", "HCS-0009-R001", "valid"), ("b", "HCS-0009-R001", "invalid")]) == []
    assert len(coverage(reqs, [("a", "HCS-0009-R001", "valid")])) == 1
    assert len(coverage(reqs, [("a", "HCS-0009-R001", "valid"), ("b", "HCS-0009-R001", "invalid"),
                               ("c", "HCS-0009-R777", "valid")])) == 1
    assert coverage(reqs, [("a", "HCS-0009-R001", "valid"), ("b", "HCS-0009-R001", "invalid")]) == []
    print("selftest: coverage checks pass")


def main(argv):
    if "--sut" in argv:  # external system under test: conformance/runner/sut.py
        import sut
        return sut.main(argv, sys.modules[__name__])
    if "--selftest" in argv:
        selftest()
        distro1.selftest()
        projection.selftest()
        recovery.selftest()
    verbose = "-v" in argv
    requirements = json.loads((ROOT / "requirements" / "requirements.json").read_text())["requirements"]
    case_validator = validator_for(CASE)
    results, refs, counts = [], [], {"pass": 0, "fail": 0, "pending": 0, "malformed": 0}
    for path in sorted((ROOT / "conformance" / "cases").rglob("*.json")):
        rel = str(path.relative_to(ROOT))
        polarity = path.parent.name
        case = json.loads(path.read_text(encoding="utf-8"))
        problems = [e.message for e in case_validator.iter_errors(case)]
        if polarity not in ("valid", "invalid"):
            problems.append("case must live in a valid/ or invalid/ directory")
        if problems:
            counts["malformed"] += 1
            results.append({"case": rel, "status": "malformed", "problems": problems})
            print(f"MALFORMED {rel}: {problems}")
            continue
        for rid in case["requirements"]:
            refs.append((rel, rid, polarity))
        try:
            status, got = run_case(case, polarity)
        except Exception as exc:  # a crash is a failure, reported with its reason
            status, got = "fail", {"exception": repr(exc)}
        counts[status] += 1
        results.append({"case": rel, "requirements": case["requirements"], "level": case["level"],
                        "status": status, "observed": got})
        if status == "fail" or verbose:
            print(f"{status.upper():8} {rel} observed={json.dumps(got)}")
    cov = coverage(requirements, refs)
    for c in cov:
        print(f"COVERAGE {c}")
    print(f"cases: {sum(counts.values())} | pass {counts['pass']} | fail {counts['fail']} | "
          f"pending(host) {counts['pending']} | malformed {counts['malformed']} | coverage problems {len(cov)}")
    print(f"validator: python-jsonschema {jsonschema_version()} Draft202012Validator")
    if "--report" in argv:
        out = pathlib.Path(argv[argv.index("--report") + 1])
        out.write_text(json.dumps({"hcsVersion": "0.1", "runner": "conformance/runner/run.py",
                                   "counts": counts, "coverage": cov, "results": results}, indent=2))
    return 0 if counts["fail"] == 0 and counts["malformed"] == 0 and not cov else 1


def jsonschema_version():
    from importlib.metadata import version
    return version("jsonschema")


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
