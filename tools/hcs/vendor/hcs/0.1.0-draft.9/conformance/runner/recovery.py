"""Reference recovery decision for HCS-0008 section 7.1 (R031 to R035), used by run.py for `recovery` cases.

Input (case `input`):
  persisted: {"active": int|null, "confirmed": int|null, "pending": int|null, "failed": [int, ...],
              "stopped": {"generation": int, "reason": str}|null}   ("stopped" is optional: a recorded stop, R016/R035)
             the facts a host reconstructs after a restart, as generation ids. No storage format is implied.
  onDisk:    generation ids whose content is present (the cleanup input).

Output:
  action:     one of "start" (the active generation is confirmed and nothing is pending), "health-check"
              (R033 items 1 and 3), "discard-pending" (item 2), "return" (item 4), "stop-and-report" (R035).
              A host with no active generation has nothing to start; it gets "start" with generation null.
  generation: the generation the action is about (the active one; the pending one for "discard-pending").
  returnTo:   the confirmed generation a failure returns to, or null when there is none.
  retain:     generations a cleanup must keep (R034): active, pending and confirmed that are on disk.
  failed:     the failed records, which a cleanup keeps.
"""


def recover(inp):
    facts = inp["persisted"]
    active, confirmed, pending = facts.get("active"), facts.get("confirmed"), facts.get("pending")
    failed = sorted(set(facts.get("failed") or []))
    on_disk = set(inp.get("onDisk") or [])
    retain = sorted({g for g in (active, pending, confirmed) if g is not None and g in on_disk})
    can_return = confirmed is not None and confirmed != active and confirmed not in failed

    def out(action, generation):
        return {"action": action, "generation": generation, "returnTo": confirmed, "retain": retain, "failed": failed}

    if facts.get("stopped"):  # R033 item 5 outranks every other item
        return out("stop-and-report", active)
    if active is None:
        return out("discard-pending", pending) if pending is not None else out("start", None)
    if active in failed:  # R033 item 4 outranks item 1: a failed generation is never started (R032)
        return out("return" if can_return else "stop-and-report", active)
    if pending == active:  # item 1
        return out("health-check", active)
    if pending is not None:  # item 2
        return out("discard-pending", pending)
    if active != confirmed:  # item 3
        return out("health-check", active)
    return out("start", active)


def selftest():
    base = {"active": 42, "confirmed": 41, "pending": 42, "failed": []}
    assert recover({"persisted": base, "onDisk": [40, 41, 42]}) == {
        "action": "health-check", "generation": 42, "returnTo": 41, "retain": [41, 42], "failed": []}
    assert recover({"persisted": dict(base, failed=[42])})["action"] == "return"
    assert recover({"persisted": dict(base, failed=[42], confirmed=None)})["action"] == "stop-and-report"
    assert recover({"persisted": dict(base, pending=43)})["action"] == "discard-pending"
    assert recover({"persisted": dict(base, pending=None)})["action"] == "health-check"
    assert recover({"persisted": dict(base, pending=None, confirmed=42)})["action"] == "start"
    assert recover({"persisted": dict(base, pending=None, confirmed=42, stopped={"generation": 42, "reason": "return-failed"})})["action"] == "stop-and-report"
    print("selftest: HCS-0008 reference recovery passes")
