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

test("+30 s extends only the countdown on screen", async () => {
  const { project } = await import("./model.ts");
  const d = data({ target_seconds: 90, state: "resting", elapsed_seconds: 80, remaining_seconds: 10 });
  const v = project(d, 0, 30);
  assert.equal(v.state, "resting");
  assert.equal(v.targetSeconds, 120);
  assert.equal(v.remainingSeconds, 40);
  // Pressed after the target was reached, the rest runs again.
  const ready = data({ target_seconds: 90, state: "ready", elapsed_seconds: 100, over_by_seconds: 10 });
  assert.equal(project(ready, 0, 30).state, "resting");
  assert.equal(project(ready, 0, 30).remainingSeconds, 20);
});

test("a stopped rest shows how long it was and does not run", async () => {
  const { project, headline } = await import("./model.ts");
  const d = data({ target_seconds: 90, state: "stopped", elapsed_seconds: 300, rested_seconds: 75 });
  const v = project(d, 60_000);
  assert.equal(v.state, "stopped");
  assert.equal(v.elapsedSeconds, 75);
  assert.equal(headline(v, d), "Rest stopped");
});

test("weights are shown and typed in the person's unit and sent in kg", async () => {
  const { toUnit, toKg, weightStep } = await import("./model.ts");
  assert.equal(toUnit(80, "kg"), 80);
  assert.equal(toUnit(81.6466, "lb"), 180);
  assert.equal(toUnit(60, "lb"), 132.3);
  assert.equal(toKg(80, "kg"), 80);
  assert.equal(toKg(180, "lb"), 81.6466);
  assert.equal(weightStep("kg"), 2.5);
  assert.equal(weightStep("lb"), 5);
  assert.equal(weightStep(undefined), 2.5);
});

test("a write's outcome is read from the proposal status", async () => {
  const { writeOutcome } = await import("./model.ts");
  assert.equal(writeOutcome({ isError: false, structuredContent: { status: "accepted", proposal_id: 3 } }), "accepted");
  assert.equal(writeOutcome({ isError: false, structuredContent: { status: "pending", proposal_id: 4 } }), "pending");
  assert.equal(writeOutcome({ isError: false, structuredContent: { status: "rejected" } }), "rejected");
  assert.equal(writeOutcome({ isError: true, content: [{ type: "text", text: "validation error" }] }), "error");
  assert.equal(writeOutcome({ isError: false }), "error");
});

test("v2 results parse with the stopped state, the set ids and the next set", async () => {
  const { parseTimerData } = await import("./model.ts");
  const d = parseTimerData({
    now: "2026-11-12T20:00:00Z", idle_after_seconds: 1200, weight_unit: "kg",
    rest: { target_seconds: 90, state: "stopped", rested_seconds: 40 },
    last_set: { id: 7, exercise: "banca", weight_kg: 80, reps: 9 },
    next_set: { exercise: "banca", weight_kg: 80, reps: 9, done_sets: 1, target_sets: 3 },
  });
  assert.equal(d?.last_set?.id, 7);
  assert.equal(d?.next_set?.target_sets, 3);
});
