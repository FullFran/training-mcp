#!/usr/bin/env python3
"""The runner's reference semantics as a system under test (protocol: conformance/README.md, "System under test").

Reads one request on stdin and writes one result on stdout, using the same evaluation as the in-process run.
Run all cases through it with:
  python3 conformance/runner/run.py --sut python3 conformance/runner/reference_sut.py
"""
import json
import sys

import run
import sut


def observe(case):
    """The `observed` object: the members that `expect` has for this level, taken from the reference evaluation."""
    _, got = run.run_case(case)
    if case["level"] == "authority":
        return {"decision": got}
    if case["level"] == "client":
        return {"hcsError": got}
    return got


def main():
    request = json.load(sys.stdin)
    version = sut.protocol_version(run)
    if request.get("protocolVersion") != version:
        print(json.dumps({"protocolVersion": version, "status": "not-implemented",
                          "reason": f"protocol {request.get('protocolVersion')!r} is not supported"}))
        return
    print(json.dumps({"protocolVersion": version, "status": "ok", "observed": observe(request["case"])}))


if __name__ == "__main__":
    main()
