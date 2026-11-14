// Switching the next set to another exercise of today's plan, with progress (#129).
import { test } from "node:test";
import assert from "node:assert/strict";
import { cardFor, fillKeyOf, parseTimerData, pickerOptions, progressText, shouldRefill, type RestTimerData } from "./model.ts";

function snapshot(extra: Partial<RestTimerData> = {}): RestTimerData {
  return {
    now: "2026-11-14T20:00:00Z",
    idle_after_seconds: 1200,
    weight_unit: "kg",
    rest: { target_seconds: 90, state: "resting", elapsed_seconds: 5, remaining_seconds: 85 },
    last_set: { id: 7, exercise: "remo", weight_kg: 55, reps: 10 },
    next_set: { exercise: "remo", weight_kg: 55, reps: 10, done_sets: 2, target_sets: 3 },
    exercises: [
      { exercise: "remo", weight_kg: 55, reps: 10, done_sets: 2, target_sets: 3, planned: true },
      { exercise: "press de pecho", reps: 10, done_sets: 0, target_sets: 3, planned: true },
      { exercise: "curl", weight_kg: 12, reps: 12, done_sets: 1 },
    ],
    ...extra,
  };
}

test("the picker lists plan items first with progress, then what was done", () => {
  assert.deepEqual(pickerOptions(snapshot()), [
    { exercise: "remo", label: "Remo · 2/3" },
    { exercise: "press de pecho", label: "Press de pecho · 0/3" },
    { exercise: "curl", label: "Curl · 1 set done" },
  ]);
});

test("without a plan the picker shows the exercises done today", () => {
  const d = snapshot({
    next_set: { exercise: "curl", weight_kg: 12, reps: 12 },
    exercises: [{ exercise: "curl", weight_kg: 12, reps: 12, done_sets: 3 }, { exercise: "remo", weight_kg: 55, reps: 10, done_sets: 1 }],
  });
  assert.deepEqual(pickerOptions(d).map((o) => o.label), ["Curl · 3 sets done", "Remo · 1 set done"]);
});

test("the next exercise is offered even when the server listed no exercises", () => {
  const d = snapshot({ exercises: undefined });
  assert.deepEqual(pickerOptions(d).map((o) => o.exercise), ["remo"]);
  assert.deepEqual(pickerOptions(snapshot({ exercises: undefined, next_set: undefined })), []);
});

test("picking an exercise refills weight and reps from its last set today and its own progress", () => {
  const d = snapshot();
  assert.deepEqual(cardFor(d, "curl"), { exercise: "curl", weight_kg: 12, reps: 12, done_sets: 1, target_sets: undefined });
  assert.deepEqual(cardFor(d, "press de pecho"), { exercise: "press de pecho", weight_kg: undefined, reps: 10, done_sets: 0, target_sets: 3 });
  // Nothing picked, or an exercise that is gone: the server's next set.
  assert.equal(cardFor(d), d.next_set);
  assert.equal(cardFor(d, "unknown"), d.next_set);
});

test("progress reads as sets done", () => {
  assert.equal(progressText({ exercise: "remo", done_sets: 2, target_sets: 3 }), "2/3 sets done");
  assert.equal(progressText({ exercise: "remo", done_sets: 0, target_sets: 3 }), "0/3 sets done");
  assert.equal(progressText({ exercise: "curl", done_sets: 1 }), "1 set done");
  assert.equal(progressText({ exercise: "curl", done_sets: 4 }), "4 sets done");
  assert.equal(progressText({ exercise: "curl" }), "No sets done yet");
});

test("a refresh never overwrites what the person is editing", () => {
  const before = snapshot();
  const card = { fillKey: fillKeyOf(before), dirty: false };
  assert.equal(shouldRefill(undefined, before), true);
  // Nothing changed on the server: keep.
  assert.equal(shouldRefill(card, snapshot()), false);
  // A set was logged elsewhere and the person has not touched the card: refill.
  const moved = snapshot({ last_set: { id: 8, exercise: "remo", weight_kg: 55, reps: 10 }, next_set: { exercise: "remo", weight_kg: 55, reps: 10, done_sets: 3, target_sets: 3 } });
  assert.equal(shouldRefill(card, moved), true);
  // Edited (picked another exercise, typed a value): never refilled.
  assert.equal(shouldRefill({ ...card, dirty: true }, moved), false);
});

test("exercises are optional in a timer result", () => {
  const parsed = parseTimerData({ now: "x", idle_after_seconds: 1200, rest: { state: "none", target_seconds: 90 } });
  assert.ok(parsed);
  assert.deepEqual(pickerOptions(parsed), []);
});
