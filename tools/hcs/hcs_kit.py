#!/usr/bin/env python3
"""Consumer kit for HCS releases. Non-normative: copy it, change it, or ignore it.

  hcs_kit.py sync <version> <sha256> [--tarball FILE | --url URL] [--root DIR] [--force]
      Take a release tarball (a local file, or downloaded), check it against the sha256 you pinned,
      check the unpacked tree against the release's own SHA256SUMS, then vendor it under
      <root>/vendor/hcs/<version>/ and write <root>/vendor/hcs/PIN.json {version, url, sha256}.
  hcs_kit.py verify [--root DIR]
      Re-check every vendored file against SHA256SUMS (edited, missing and extra files fail).
  hcs_kit.py baseline --report FILE --divergences FILE --out FILE
      Write a baseline from the runner's --report output.
  hcs_kit.py compare --report FILE --baseline FILE --divergences FILE [--update]
      Fail only on regression against the baseline. --update rewrites the baseline afterwards.

Exit codes: 0 ok, 1 refused or regression, 2 usage or bad input. Python 3.10+ standard library only.
Formats: see README.md and schemas/.
"""
import hashlib
import io
import json
import pathlib
import re
import shutil
import sys
import tarfile
import tempfile
import urllib.request

RELEASE_URL = "https://github.com/FullFran/hcs/releases/download/v{v}/hcs-{v}.tar.gz"
VERSION = re.compile(r"^[0-9A-Za-z][0-9A-Za-z.+-]*$")
DIGEST = re.compile(r"^[0-9a-f]{64}$")
REQUIREMENT = re.compile(r"^HCS-\d{4}-R\d{3}$")
CASE_KEY = re.compile(r"(HCS-\d{4}/HCS-\d{4}-R\d{3}/(?:valid|invalid)/[^/]+\.json)$")
# 0 = good, 1 = not implemented (or declared nowhere), 2 = failing. A case may only move up the scale if a rule says so.
RANK = {"pass": 0, "pending": 0, "not-applicable": 0, "divergence": 0, "not-implemented": 1,
        "fail": 2, "crash": 2, "malformed": 2}
LIMIT = 512 * 1024 * 1024  # bytes read from a tarball; releases are well below this


class Refused(Exception):
    pass


class Usage(Exception):
    pass


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def read_json(path, what):
    try:
        return json.loads(pathlib.Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise Usage(f"cannot read {what} {path}: {exc}")


# ---- sync and verify -------------------------------------------------------------------------

def fetch(url):
    try:
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read(LIMIT + 1)
    except (OSError, ValueError) as exc:
        raise Refused(f"cannot download {url}: {exc}")
    if len(data) > LIMIT:
        raise Refused(f"{url} is larger than {LIMIT} bytes")
    return data


def unpack(data, version, dest):
    """Extract a release tarball into dest, accepting only regular files and directories under hcs-<version>/."""
    prefix = f"hcs-{version}/"
    try:
        tar = tarfile.open(fileobj=io.BytesIO(data), mode="r:gz")
    except (tarfile.TarError, OSError) as exc:
        raise Refused(f"not a gzip tarball: {exc}")
    with tar:
        for member in tar.getmembers():
            name = member.name
            parts = pathlib.PurePosixPath(name).parts
            if not (name + "/").startswith(prefix) or ".." in parts or name.startswith("/"):
                raise Refused(f"member outside {prefix}: {name}")
            if not (member.isfile() or member.isdir()):
                raise Refused(f"member is not a regular file or directory: {name}")
        for member in tar.getmembers():
            target = dest / member.name[len(prefix):]  # the top directory itself maps to dest
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(tar.extractfile(member).read())


def check_tree(tree):
    """Compare a directory with its SHA256SUMS; return a list of problems."""
    sums = tree / "SHA256SUMS"
    if not sums.is_file():
        return ["SHA256SUMS is missing"]
    problems, listed = [], set()
    for line in sums.read_text(encoding="utf-8").splitlines():
        m = re.match(r"^([0-9a-f]{64})  (.+)$", line)
        if not m:
            problems.append(f"malformed SHA256SUMS line: {line!r}")
            continue
        digest, rel = m.groups()
        listed.add(rel)
        path = tree / rel
        if ".." in pathlib.PurePosixPath(rel).parts or rel.startswith("/"):
            problems.append(f"unsafe path in SHA256SUMS: {rel}")
        elif not path.is_file():
            problems.append(f"missing: {rel}")
        elif sha256(path.read_bytes()) != digest:
            problems.append(f"changed: {rel}")
    present = {p.relative_to(tree).as_posix() for p in tree.rglob("*") if p.is_file()} - {"SHA256SUMS"}
    problems += [f"not in SHA256SUMS: {rel}" for rel in sorted(present - listed)]
    return problems


def option(args, name, default=None):
    if name in args:
        i = args.index(name)
        if i + 1 >= len(args):
            raise Usage(f"{name} needs a value")
        value = args[i + 1]
        del args[i:i + 2]
        return value
    return default


def flag(args, name):
    if name in args:
        args.remove(name)
        return True
    return False


def cmd_sync(args):
    tarball, url = option(args, "--tarball"), option(args, "--url")
    root, force = pathlib.Path(option(args, "--root", ".")), flag(args, "--force")
    if len(args) != 2 or any(a.startswith("--") for a in args):
        raise Usage("expected <version> <sha256>")
    version, expected = args
    if not VERSION.match(version):
        raise Usage(f"invalid version {version!r}")
    if not DIGEST.match(expected):
        raise Usage("sha256 must be 64 lowercase hex characters")
    if tarball and url:
        raise Usage("use --tarball or --url, not both")
    url = url or RELEASE_URL.format(v=version)
    if tarball:
        try:
            data = pathlib.Path(tarball).read_bytes()
        except OSError as exc:
            raise Usage(f"cannot read {tarball}: {exc}")
    else:
        data = fetch(url)
    if sha256(data) != expected:
        raise Refused(f"sha256 of the tarball is {sha256(data)}, expected {expected}")
    vendor = root / "vendor" / "hcs"
    target = vendor / version
    if target.exists() and not force:
        raise Refused(f"{target} exists; use --force to replace it")
    root.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=root, prefix=".hcs-sync-") as scratch:
        staged = pathlib.Path(scratch) / "tree"
        staged.mkdir()
        unpack(data, version, staged)
        problems = check_tree(staged)
        if problems:
            raise Refused("the release does not match its SHA256SUMS:\n  " + "\n  ".join(problems))
        vendor.mkdir(parents=True, exist_ok=True)
        if target.exists():
            shutil.rmtree(target)
        staged.rename(target)
    (vendor / "PIN.json").write_text(
        json.dumps({"version": version, "url": url, "sha256": expected}, indent=2) + "\n", encoding="utf-8")
    count = sum(1 for p in target.rglob("*") if p.is_file())
    print(f"HCS {version}: {count} files checked against SHA256SUMS, vendored in {target}, pin in {vendor / 'PIN.json'}")
    return 0


def cmd_verify(args):
    root = pathlib.Path(option(args, "--root", "."))
    if args:
        raise Usage(f"unexpected arguments: {args}")
    vendor = root / "vendor" / "hcs"
    try:
        pin = json.loads((vendor / "PIN.json").read_text(encoding="utf-8"))
        version = pin["version"]
        assert VERSION.match(version)
    except (OSError, ValueError, KeyError, AssertionError, TypeError):
        raise Refused(f"{vendor / 'PIN.json'} is missing or invalid")
    tree = vendor / version
    if not tree.is_dir():
        raise Refused(f"{tree} is missing")
    problems = check_tree(tree)
    if problems:
        raise Refused(f"vendored HCS {version} differs from its SHA256SUMS:\n  " + "\n  ".join(problems))
    print(f"HCS {version}: vendored tree matches SHA256SUMS")
    return 0


# ---- baseline and compare --------------------------------------------------------------------

def load_rules(path):
    """Validate divergences.json by hand (schemas/divergences.schema.json is the published contract)."""
    doc = read_json(path, "divergences file")
    if not isinstance(doc, dict):
        raise Usage(f"{path}: expected an object")
    rules = {}
    for group, label in (("divergences", "divergence"), ("notApplicable", "not-applicable")):
        entries = doc.get(group, [])
        if not isinstance(entries, list):
            raise Usage(f"{path}: {group} must be an array")
        for i, rule in enumerate(entries):
            where = f"{path}: {group}[{i}]"
            if not isinstance(rule, dict) or not REQUIREMENT.match(str(rule.get("requirement", ""))):
                raise Usage(f"{where}: `requirement` must be a requirement id such as HCS-0005-R001")
            if not isinstance(rule.get("reason"), str) or not rule["reason"].strip():
                raise Usage(f"{where}: `reason` must be a non-empty string")
            cases = rule.get("cases")
            if cases is not None and not (isinstance(cases, list) and all(isinstance(c, str) for c in cases)):
                raise Usage(f"{where}: `cases` must be an array of case keys")
            rules.setdefault(rule["requirement"], []).append((label, None if cases is None else set(cases)))
    return rules


def case_key(case):
    m = CASE_KEY.search(case.replace("\\", "/"))
    return m.group(1) if m else case


def effective(results, rules):
    """{case key: status}. A rule turns any non-pass result of its cases into `divergence` or `not-applicable`."""
    out = {}
    for item in results:
        key, status = case_key(item["case"]), item["status"]
        if status not in RANK:
            raise Usage(f"unknown status {status!r} for {key}")
        if status != "pass":
            requirement = key.split("/")[1] if "/" in key else ""
            for label, cases in rules.get(requirement, []):
                if cases is None or key in cases:
                    status = label
                    break
        out[key] = status
    return out


def read_report(path):
    doc = read_json(path, "report")
    if not isinstance(doc, dict) or not isinstance(doc.get("results"), list):
        raise Usage(f"{path}: not a runner report (no `results` array)")
    for item in doc["results"]:
        if not isinstance(item, dict) or "case" not in item or "status" not in item:
            raise Usage(f"{path}: every result needs `case` and `status`")
    return doc


def write_baseline(path, report, current):
    doc = {"hcsVersion": str(report.get("hcsVersion", "")), "cases": dict(sorted(current.items()))}
    pathlib.Path(path).write_text(json.dumps(doc, indent=2) + "\n", encoding="utf-8")


def cmd_baseline(args):
    report_path, div, out = option(args, "--report"), option(args, "--divergences"), option(args, "--out")
    if args or not (report_path and div and out):
        raise Usage("baseline needs --report, --divergences and --out")
    report = read_report(report_path)
    current = effective(report["results"], load_rules(div))
    write_baseline(out, report, current)
    print(f"baseline of {len(current)} cases written to {out}")
    return 0


def cmd_compare(args):
    report_path, base_path = option(args, "--report"), option(args, "--baseline")
    div, update = option(args, "--divergences"), flag(args, "--update")
    if args or not (report_path and base_path and div):
        raise Usage("compare needs --report, --baseline and --divergences")
    report = read_report(report_path)
    current = effective(report["results"], load_rules(div))
    if not pathlib.Path(base_path).exists():
        if not update:
            raise Usage(f"{base_path} does not exist; create it with --update or `baseline`")
        baseline = {}
    else:
        doc = read_json(base_path, "baseline")
        baseline = doc.get("cases") if isinstance(doc, dict) else None
        if not isinstance(baseline, dict):
            raise Usage(f"{base_path}: expected an object with a `cases` object")
    regressions, improved, new = [], [], []
    for key, status in sorted(current.items()):
        before = baseline.get(key)
        if before is None:
            new.append(key)
            if RANK[status] > 0:
                regressions.append(f"{key}: new case is {status}")
        elif RANK[status] > RANK.get(before, 0):
            regressions.append(f"{key}: {before} -> {status}")
        elif RANK[status] < RANK.get(before, 0):
            improved.append(f"{key}: {before} -> {status}")
    removed = sorted(set(baseline) - set(current))
    for label, items in (("improved", improved), ("new", new), ("removed from the run", removed)):
        if items:
            print(f"{label} ({len(items)}):\n  " + "\n  ".join(items))
    if regressions:
        print(f"REGRESSION ({len(regressions)}):\n  " + "\n  ".join(regressions))
    else:
        print(f"no regression against the baseline ({len(current)} cases)")
    if update:
        write_baseline(base_path, report, current)
        print(f"baseline rewritten: {base_path}")
    return 1 if regressions and not update else 0


COMMANDS = {"sync": cmd_sync, "verify": cmd_verify, "baseline": cmd_baseline, "compare": cmd_compare}


def main(argv):
    if not argv or argv[0] not in COMMANDS:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        return COMMANDS[argv[0]](list(argv[1:]))
    except Usage as exc:
        print(f"hcs_kit: {exc}", file=sys.stderr)
        return 2
    except Refused as exc:
        print(f"hcs_kit: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
