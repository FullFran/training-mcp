import { test } from "node:test";
import assert from "node:assert/strict";
import { clock, headline, parseTimerData, project, type RestTimerData } from "./model.ts";

function data(rest: RestTimerData["rest"], extra: Partial<RestTimerData> = {}): RestTimerData {
  return { now: "2026-11-12T20:00:00Z", rest, idle_after_seconds: 1200, ...extra };
}

test("a render 35 s after the set shows the real remaining time, not the full target", () => {
  const d = data({ target_seconds: 90, state: "resting", since: "2026-11-12T19:59:25Z", elapsed_seconds: 35, remaining_seconds: 55 });
  assert.deepEqual(project(d, 0), { state: "resting", targetSeconds: 90, elapsedSeconds: 35, remainingSeconds: 55, overBySeconds: 0 });
});

test("the countdown advances with local time and turns ready at the target", () => {
  const d = data({ target_seconds: 90, state: "resting", elapsed_seconds: 35, remaining_seconds: 55 });
  assert.equal(project(d, 10_400).remainingSeconds, 45);
  const at = project(d, 55_000);
  assert.equal(at.state, "ready");
  assert.equal(at.overBySeconds, 0);
  assert.equal(project(d, 70_000).overBySeconds, 15);
});

test("a rest past the idle threshold stops showing a timer", () => {
  const d = data({ target_seconds: 120, state: "ready", elapsed_seconds: 1190, over_by_seconds: 1070 });
  assert.equal(project(d, 11_000).state, "idle");
});

test("none, superset and idle never count down", () => {
  for (const state of ["none", "superset", "idle"] as const) {
    const v = project(data({ target_seconds: 120, state, elapsed_seconds: 10 }), 30_000);
    assert.equal(v.state, state);
    assert.equal(v.remainingSeconds, 0);
    assert.equal(v.elapsedSeconds, 10);
  }
});

test("a negative local delta (clock moved back) never adds or removes time", () => {
  const d = data({ target_seconds: 90, state: "resting", elapsed_seconds: 35, remaining_seconds: 55 });
  assert.equal(project(d, -5_000).remainingSeconds, 55);
});

test("each target is the snapshot's own: 90 s and 180 s accounts count independently", () => {
  const alice = data({ target_seconds: 90, state: "resting", elapsed_seconds: 0, remaining_seconds: 90 });
  const bob = data({ target_seconds: 180, state: "resting", elapsed_seconds: 0, remaining_seconds: 180 });
  assert.equal(project(alice, 100_000).state, "ready");
  assert.equal(project(bob, 100_000).remainingSeconds, 80);
});

test("parseTimerData refuses anything that is not a render_rest_timer result", () => {
  assert.equal(parseTimerData(undefined), undefined);
  assert.equal(parseTimerData({ now: "x" }), undefined);
  assert.equal(parseTimerData({ now: "x", rest: { state: "running", target_seconds: 90 }, idle_after_seconds: 1200 }), undefined);
  assert.equal(parseTimerData({ now: "x", rest: { state: "resting", target_seconds: 0 }, idle_after_seconds: 1200 }), undefined);
  assert.equal(parseTimerData({ now: "x", rest: { state: "resting", target_seconds: 90 } }), undefined);
  const ok = { now: "x", rest: { state: "resting", target_seconds: 90 }, idle_after_seconds: 1200 };
  assert.equal(parseTimerData(ok), ok);
});

test("clock and headline", () => {
  assert.equal(clock(0), "0:00");
  assert.equal(clock(65), "1:05");
  assert.equal(clock(-3), "0:00");
  const sup = data({ target_seconds: 90, state: "superset", elapsed_seconds: 5 }, { next_exercise: "Pull-up" });
  assert.equal(headline(project(sup, 0), sup), "Superset: go to Pull-up");
  const ready = data({ target_seconds: 90, state: "ready", elapsed_seconds: 100 });
  assert.equal(headline(project(ready, 0), ready), "Rest done");
  assert.equal(headline(project(data({ target_seconds: 90, state: "none" }), 0), data({ target_seconds: 90, state: "none" })), "No set logged today yet");
});
