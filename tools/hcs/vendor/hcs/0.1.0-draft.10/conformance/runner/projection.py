"""Reference projection for HCS-0008 (host adapter), used by run.py for `projection` cases.

Input (case `input`):
  generation:       {"id": str, "entries": [{"id": str, "contributions": [contribution, ...]}, ...]}
                    entries in activation order.
  contribution:     {"kind": str, ...}
                    skill:       "name" (the Agent Skills name read from SKILL.md)
                    mcp-server:  "descriptor" (the server.json document at `path`); for a `file` package,
                                 "files": regular files in the contribution's directory, relative to it
                    other kinds: "settings": [{"name": str, "effect": "add"|"replace"|"remove"|"widen"|"narrow"}]
  projectableKinds: kinds the adapter can project into its engine.
  hostOwned:        engine-level names of host-owned settings.
  grantedSecrets:   {entry id: [input name, ...]} secrets the host grants per entry.
  hostRuntimes:     {runtimeHint: [control environment variable names]} for each runtime the host provides
                    (HCS-0008-R023); a package may not set those variables.
  clientLimits:     optional {"client": str, "toolListBytes": int, ...} declared for the MCP client (HCS-0008-R036);
                    an mcp-server contribution then carries "toolListBytes", the size the host measured (R037, R038).
  activationPolicy: "all-or-nothing" | "partial" (HCS-0003 step 6).

Output: {"errors": [HCS-0006 errors with entry], "selected": [entry ids the host may report active]}.
`unsupported_contribution` leaves its entry out of `selected` but does not empty it under all-or-nothing
(HCS-0008-R017); every other error is a failure that does.

Engine-level names: a skill projects "skill/<name>", an MCP server "mcp/<server.json name>"; both with
effect "add". Extension kinds state their settings, because their meaning is distribution-defined.
The server.json check covers the members the schema requires and the HCS-0008-R011 secret rule only;
full validation against the MCP Registry schema is the adapter's job (HCS-0008 section 8).
"""

import re

SERVER_REQUIRED = ("name", "description", "version")
# HCS-0001-R005 relative path (no leading '/', no backslash, no '..' segment) plus no empty segment (R021)
RELATIVE_PATH = re.compile(r"(?!/)(?!.*\\)(?!(.*/)?\.\.(/|\Z))(?!.*//)(?!.*/\Z)[^\x00]+", re.S)
OVERRIDING = {"add", "replace"}
FORBIDDEN_ON_HOST_OWNED = {"add", "replace", "remove", "widen"}


def _err(code, entry):
    return {"code": code, "entry": entry}


def _with_variables(name, item):
    """An Input and the Inputs in its `variables` (InputWithVariables in the 2025-12-11 schema)."""
    yield name, item
    for vname, var in (item.get("variables") or {}).items():
        yield vname, var


def _inputs(descriptor):
    """Yield (name, input) for every Input of a server.json document, variables included."""
    for package in descriptor.get("packages") or []:
        for key in ("environmentVariables", "runtimeArguments", "packageArguments"):
            for item in package.get(key) or []:
                yield from _with_variables(item.get("name") or item.get("valueHint"), item)
    for remote in descriptor.get("remotes") or []:
        for item in remote.get("headers") or []:
            yield from _with_variables(item.get("name"), item)
        for name, item in (remote.get("variables") or {}).items():
            yield name, item


def _check_descriptor(entry, descriptor, granted):
    """HCS-0008-R010 (required members) and R011 (no secret values)."""
    if not isinstance(descriptor, dict) or any(not isinstance(descriptor.get(k), str) for k in SERVER_REQUIRED):
        return [_err("manifest_invalid", entry)], None
    for _, item in _inputs(descriptor):
        if item.get("isSecret") and ("value" in item or "default" in item):
            return [_err("manifest_invalid", entry)], None
    return [], descriptor["name"]


def _check_grants(entry, descriptor, granted):
    """HCS-0008-R012, reported after the shape and startability checks (section 6.4.1 order)."""
    for name, item in _inputs(descriptor):
        if item.get("isSecret") and item.get("isRequired") and name not in granted:
            return [_err("permission_denied", entry)]
    return []


def _check_file_package(entry, descriptor, files, runtimes):
    """HCS-0008-R021 (shape, manifest_invalid), then R024 and R023 (unsupported_contribution).
    Returns (code or None). Links that resolve outside the folder are a host-level case."""
    packages = [p for p in descriptor.get("packages") or [] if p.get("registryType") == "file"]
    if not packages:
        return None
    if len(packages) > 1:
        return "manifest_invalid"
    pkg = packages[0]
    ident = pkg.get("identifier")
    transport = pkg.get("transport")
    if not isinstance(transport, dict) or transport.get("type") != "stdio" or not isinstance(ident, str) \
            or not RELATIVE_PATH.fullmatch(ident) or _normalise(ident) not in files:
        return "manifest_invalid"
    args = [a for key in ("packageArguments", "runtimeArguments") for a in pkg.get(key) or []]
    if any(v.get("isSecret") for a in args for _, v in _with_variables(None, a)):
        return "unsupported_contribution"                   # R024: secrets only through the environment
    hint = pkg.get("runtimeHint")
    control = runtimes.get(hint) if isinstance(hint, str) else None
    env_names = {e.get("name") for e in pkg.get("environmentVariables") or []}
    if control is None or pkg.get("runtimeArguments") or env_names & set(control):
        return "unsupported_contribution"                   # R023: the host owns the runtime and its flags
    return None


def _normalise(path):
    """Remove '.' segments (HCS-0008-R021); '..' and empty segments are already refused."""
    return "/".join(seg for seg in path.split("/") if seg != ".")


def project(inp):
    projectable = set(inp.get("projectableKinds", []))
    host_owned = set(inp.get("hostOwned", []))
    grants = inp.get("grantedSecrets", {})
    runtimes = inp.get("hostRuntimes", {})
    entries = inp["generation"]["entries"]
    errors, failed, unprojected, claims = [], set(), set(), {}

    def fail(code, eid):
        if {"code": code, "entry": eid} not in errors:
            errors.append(_err(code, eid))
        failed.add(eid)

    def report_unsupported(eid):                            # R004; R017: reported, not a failure
        if {"code": "unsupported_contribution", "entry": eid} not in errors:
            errors.append(_err("unsupported_contribution", eid))
        unprojected.add(eid)

    for entry in entries:
        eid = entry["id"]
        for c in entry["contributions"]:
            kind = c["kind"]
            if kind not in projectable:                     # R003, R004
                report_unsupported(eid)
                continue
            if kind == "skill":
                settings = [{"name": "skill/" + c["name"], "effect": "add"}]
            elif kind == "mcp-server":
                errs, name = _check_descriptor(eid, c.get("descriptor"), set(grants.get(eid, [])))
                if errs:
                    for e in errs:
                        fail(e["code"], eid)
                    continue
                code = _check_file_package(eid, c["descriptor"], set(c.get("files", [])), runtimes)
                if code == "manifest_invalid":
                    fail(code, eid)
                    continue
                if code == "unsupported_contribution":
                    report_unsupported(eid)
                    continue
                budget = (inp.get("clientLimits") or {}).get("toolListBytes")
                if budget is not None:                      # R037 over the budget, R038 not measured
                    measured = c.get("toolListBytes")
                    if measured is None or measured > budget:
                        report_unsupported(eid)
                        continue
                denied = _check_grants(eid, c["descriptor"], set(grants.get(eid, [])))
                if denied:
                    fail("permission_denied", eid)
                    continue
                settings = [{"name": "mcp/" + name, "effect": "add"}]
            else:
                settings = c.get("settings", [])
            for s in settings:
                if s["name"] in host_owned:
                    if s["effect"] in FORBIDDEN_ON_HOST_OWNED:  # R007; narrowing is allowed
                        fail("permission_denied", eid)
                elif s["effect"] in OVERRIDING:
                    claims.setdefault(s["name"], set()).add(eid)

    for name in sorted(claims):                             # R006: same engine-level name twice
        owners = claims[name]
        if len(owners) > 1:
            for eid in sorted(owners):
                fail("ambiguous_provider", eid)

    ids = [e["id"] for e in entries]
    if inp.get("activationPolicy", "all-or-nothing") == "partial":
        selected = [i for i in ids if i not in failed and i not in unprojected]       # R005
    else:                                                   # R017: only real failures empty the set
        selected = [] if failed else [i for i in ids if i not in unprojected]
    return {"errors": errors, "selected": selected}


def selftest():
    base = {"projectableKinds": ["skill"], "activationPolicy": "partial",
            "generation": {"id": "g1", "entries": [
                {"id": "a", "contributions": [{"kind": "skill", "name": "x"}]},
                {"id": "b", "contributions": [{"kind": "skill", "name": "x"}]},
                {"id": "c", "contributions": [{"kind": "mcp-server", "descriptor": {}}]}]}}
    got = project(base)
    assert got["selected"] == [], got
    assert {(e["code"], e["entry"]) for e in got["errors"]} == {
        ("ambiguous_provider", "a"), ("ambiguous_provider", "b"), ("unsupported_contribution", "c")}, got
    print("selftest: HCS-0008 reference projection passes")
