"""Run the conformance cases against an external system under test (SUT).

Usage (through run.py):
  python3 conformance/runner/run.py [--cases DIR] [--timeout SECONDS] [--strict] [--report PATH] [-v] --sut COMMAND [ARG...]

Protocol (documented in conformance/README.md, "System under test"): for each non-host case the runner starts COMMAND
once, without a shell, writes one JSON request to its stdin and reads one JSON result from its stdout. The request
carries the case object unchanged. The runner, not the SUT, compares the observed result with the case's `expect`.
"""
import json
import pathlib
import subprocess

STATUSES = ("ok", "not-implemented", "not-applicable")
OUTCOMES = ("pass", "fail", "crash", "not-implemented", "not-applicable", "pending", "malformed")
DEFAULT_TIMEOUT = 30.0
STDERR_LIMIT = 2000


def protocol_version(run):
    """The protocol version is the case schema version: the last component of the case schema URN."""
    return run.CASE.rsplit(":", 1)[1]


def build_request(case, version="0.1"):
    return {"protocolVersion": version, "case": case}


def parse_result(stdout, version):
    """Returns (result, None) or (None, reason). Output is exactly one JSON object, surrounded by whitespace only."""
    try:
        result = json.loads(stdout.decode("utf-8"))
    except (UnicodeDecodeError, ValueError) as exc:
        return None, f"stdout is not one JSON document ({exc})"
    if not isinstance(result, dict):
        return None, "result is not a JSON object"
    if result.get("protocolVersion") != version:
        return None, f"protocolVersion {result.get('protocolVersion')!r} is not {version!r}"
    if result.get("status") not in STATUSES:
        return None, f"status {result.get('status')!r} is not one of {', '.join(STATUSES)}"
    if result["status"] == "ok" and not isinstance(result.get("observed"), dict):
        return None, "status ok requires an `observed` object"
    if "reason" in result and not isinstance(result["reason"], str):
        return None, "`reason` must be a string"
    return result, None


def exchange(command, case, version, timeout):
    """One process per case. Returns {"status": ..., "observed"?, "reason"?, "stderr"}; status `crash` covers a failure
    to start, a timeout, a non-zero exit, unparsable output and a protocol violation."""
    request = json.dumps(build_request(case, version)).encode("utf-8")
    try:
        done = subprocess.run(command, input=request, capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        return {"status": "crash", "reason": f"timed out after {timeout:g} s", "stderr": _text(exc.stderr)}
    except OSError as exc:
        return {"status": "crash", "reason": f"cannot start the command ({exc})", "stderr": ""}
    stderr = _text(done.stderr)
    if done.returncode != 0:
        return {"status": "crash", "reason": f"exit code {done.returncode}", "stderr": stderr}
    result, problem = parse_result(done.stdout, version)
    if problem:
        return {"status": "crash", "reason": problem, "stderr": stderr}
    result["stderr"] = stderr
    return result


def _text(data):
    return (data or b"").decode("utf-8", "replace")[:STDERR_LIMIT]


def judge(run, case, polarity, observed):
    """pass or fail: every member of `expect` must equal the same member of `observed`, with the same rules as the
    in-process run (errors compare as an unordered set of code/entry/capability; a denial compares allow and code; a
    recovery `invalid/` case passes when the observed decision differs from the stated one)."""
    level, exp = case["level"], case["expect"]
    try:
        if level == "authority":
            got, want = observed["decision"], exp["decision"]
            ok = got["allow"] == want["allow"] and (got["allow"] or got["error"]["code"] == want["error"]["code"])
        elif level == "recovery":
            ok = all(k in observed and observed[k] == v for k, v in exp.items()) == (polarity == "valid")
        else:
            ok = all(k in observed and (run.norm(observed[k]) == run.norm(v) if k == "errors" else observed[k] == v)
                     for k, v in exp.items())
    except (KeyError, TypeError, AttributeError):
        ok = False
    return "pass" if ok else "fail"


def parse_args(argv):
    """Returns (options, command). Everything after `--sut` is the command, never interpreted."""
    i = argv.index("--sut")
    options = {"cases": None, "timeout": DEFAULT_TIMEOUT, "strict": False, "report": None, "verbose": False}
    rest, command = argv[:i], argv[i + 1:]
    j = 0
    while j < len(rest):
        arg = rest[j]
        if arg == "--strict":
            options["strict"] = True
        elif arg == "-v":
            options["verbose"] = True
        elif arg in ("--cases", "--timeout", "--report") and j + 1 < len(rest):
            j += 1
            options[arg[2:]] = float(rest[j]) if arg == "--timeout" else rest[j]
        else:
            raise ValueError(f"unexpected argument {arg!r}")
        j += 1
    if not command:
        raise ValueError("--sut needs a command")
    return options, command


def main(argv, run):
    """Entry point called by run.py when `--sut` is present. `run` is the runner module (reference semantics)."""
    try:
        options, command = parse_args(argv)
    except ValueError as exc:
        print(f"error: {exc}\nusage: run.py [--cases DIR] [--timeout SECONDS] [--strict] [--report PATH] [-v] --sut COMMAND [ARG...]")
        return 2
    version = protocol_version(run)
    base = pathlib.Path(options["cases"] or run.ROOT / "conformance" / "cases")
    case_validator = run.validator_for(run.CASE)
    counts, results = dict.fromkeys(OUTCOMES, 0), []
    for path in sorted(base.rglob("*.json")):
        try:
            rel = str(path.relative_to(run.ROOT))
        except ValueError:
            rel = str(path)
        polarity = path.parent.name
        case = json.loads(path.read_text(encoding="utf-8"))
        problems = [e.message for e in case_validator.iter_errors(case)]
        if polarity not in ("valid", "invalid"):
            problems.append("case must live in a valid/ or invalid/ directory")
        if problems:
            status, item = "malformed", {"problems": problems}
            print(f"MALFORMED {rel}: {problems}")
        elif case["level"] == "host":
            status, item = "pending", {}  # host-only requirements are never sent to the SUT
        else:
            answer = exchange(command, case, version, options["timeout"])
            status = answer["status"]
            item = {k: answer[k] for k in ("observed", "reason", "stderr") if k in answer}
            if status == "ok":
                status = judge(run, case, polarity, answer["observed"])
            if status in ("fail", "crash") or options["verbose"]:
                detail = item.get("reason") or json.dumps(item.get("observed"))
                print(f"{status.upper():8} {rel} {detail}")
                if status == "crash" and item.get("stderr"):
                    print(f"         stderr: {item['stderr']!r}")
        counts[status] += 1
        results.append({"case": rel, "level": case.get("level"), "status": status, **item})
    print(f"cases: {sum(counts.values())} | pass {counts['pass']} | fail {counts['fail']} | crash {counts['crash']} | "
          f"not-implemented {counts['not-implemented']} | not-applicable {counts['not-applicable']} | "
          f"pending(host) {counts['pending']} | malformed {counts['malformed']}")
    print(f"target: {' '.join(command)} (protocol {version})")
    if options["report"]:
        pathlib.Path(options["report"]).write_text(json.dumps(
            {"hcsVersion": version, "protocolVersion": version, "runner": "conformance/runner/run.py",
             "target": command, "counts": counts, "results": results}, indent=2))
    bad = counts["fail"] + counts["crash"] + counts["malformed"] + (counts["not-implemented"] if options["strict"] else 0)
    return 0 if bad == 0 else 1
