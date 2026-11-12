#!/usr/bin/env node
// A minimal system under test for the HCS conformance runner (conformance/README.md, "System under test").
// It implements one case kind, the `authority` level (HCS-0005 evaluation), and answers "not-implemented" for the rest.
// Run:  python3 conformance/runner/run.py --sut node conformance/examples/minimal-sut.mjs
// Node 18 or later, no dependencies. Documentation only: it does not validate its input.
import { readFileSync } from "node:fs";

const PROTOCOL_VERSION = "0.1"; // the case schema version
const { protocolVersion, case: c } = JSON.parse(readFileSync(0, "utf8"));

function answer(result) {
  process.stdout.write(JSON.stringify({ protocolVersion: PROTOCOL_VERSION, ...result }) + "\n");
}

function evaluate({ check, grants, denials = [] }) {
  // A stored record matches the check on entry and permission; a session-limited record matches only its session.
  const matches = (r) =>
    r.entry === check.entry && r.permission === check.permission && (r.session === undefined || r.session === check.session);
  if (denials.some(matches)) return { allow: false, error: { code: "permission_denied", entry: check.entry } };
  if (grants.some(matches)) return { allow: true };
  return { allow: false, error: { code: "permission_denied", entry: check.entry } };
}

if (protocolVersion !== PROTOCOL_VERSION) {
  answer({ status: "not-implemented", reason: `protocol ${protocolVersion} is not supported` });
} else if (c.level === "authority") {
  answer({ status: "ok", observed: { decision: evaluate(c.input) } });
} else {
  answer({ status: "not-implemented", reason: `level ${c.level} is not implemented` });
}
