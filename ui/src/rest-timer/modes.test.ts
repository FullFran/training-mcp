// Timer-only compact mode and the full workout mode (#132).
import { test } from "node:test";
import assert from "node:assert/strict";
import { modeToggleLabel, nextMode, project, resolveMode, timerLabel, type RestTimerData } from "./model.ts";

function snapshot(rest: RestTimerData["rest"], extra: Partial<RestTimerData> = {}): RestTimerData {
  return { now: "2026-11-14T20:00:00Z", idle_after_seconds: 1200, rest, ...extra };
}

test("without a mode the view is the full one, as before", () => {
  assert.equal(resolveMode(undefined, undefined), "full");
  assert.equal(resolveMode("full", undefined), "full");
  assert.equal(resolveMode("compact", undefined), "compact");
  assert.equal(resolveMode("tiny", undefined), "full");
});

test("the mode the person selected outlives later results", () => {
  assert.equal(resolveMode("full", "compact"), "compact");
  assert.equal(resolveMode("compact", "full"), "full");
});

test("expanding and collapsing swap the mode and say what they do", () => {
  assert.equal(nextMode("compact"), "full");
  assert.equal(nextMode("full"), "compact");
  assert.equal(modeToggleLabel("compact"), "Show workout");
  assert.equal(modeToggleLabel("full"), "Timer only");
});

test("the timer has a spoken label for every state, without a changing second in the status", () => {
  const resting = snapshot({ target_seconds: 90, state: "resting", elapsed_seconds: 35, remaining_seconds: 55 });
  assert.equal(timerLabel(project(resting, 0), resting), "Rest timer: 0:55 left of 1:30");
  const ready = snapshot({ target_seconds: 90, state: "ready", elapsed_seconds: 100, over_by_seconds: 10 });
  assert.equal(timerLabel(project(ready, 0), ready), "Rest done: 0:10 over the 1:30 target");
  const stopped = snapshot({ target_seconds: 90, state: "stopped", rested_seconds: 75 });
  assert.equal(timerLabel(project(stopped, 0), stopped), "Rest stopped after 1:15");
  const superset = snapshot({ target_seconds: 90, state: "superset" }, { next_exercise: "Pull-up" });
  assert.equal(timerLabel(project(superset, 0), superset), "Superset: go to Pull-up");
  const none = snapshot({ target_seconds: 120, state: "none" });
  assert.equal(timerLabel(project(none, 0), none), "No set logged today yet. Rest target 2:00");
  const idle = snapshot({ target_seconds: 120, state: "idle", elapsed_seconds: 1300 });
  assert.equal(timerLabel(project(idle, 0), idle), "No rest running");
});

test("both modes derive the countdown from the same snapshot", () => {
  const d = snapshot({ target_seconds: 90, state: "resting", elapsed_seconds: 35, remaining_seconds: 55 }, { mode: "compact" });
  const full = { ...d, mode: "full" as const };
  for (const ms of [0, 12_000, 70_000]) assert.deepEqual(project(d, ms), project(full, ms));
});
