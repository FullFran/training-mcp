// Safe retries, clear errors and no duplicate sets from the rest timer (#130).
import { test } from "node:test";
import assert from "node:assert/strict";
import { afterWrite, attemptFor, canSend, isStale, newKey, writeNotice, type RestTimerData } from "./model.ts";

function snapshot(lastSetId?: number): RestTimerData {
  return {
    now: "2026-11-14T20:00:00Z",
    idle_after_seconds: 1200,
    rest: { target_seconds: 90, state: "resting" },
    ...(lastSetId === undefined ? {} : { last_set: { id: lastSetId, exercise: "banca", weight_kg: 80, reps: 9 } }),
  };
}

test("a key can be minted where crypto.randomUUID does not exist (a view outside a secure context)", () => {
  const withoutUUID = { getRandomValues: (a: Uint8Array) => a.fill(0xab) } as unknown as Crypto;
  const key = newKey(withoutUUID);
  assert.match(key, /^[0-9a-f]{32}$/);
  assert.equal(newKey({ randomUUID: () => "fixed-uuid" } as unknown as Crypto), "fixed-uuid");
  assert.notEqual(newKey(crypto), newKey(crypto));
});

test("the same body keeps its key through a retry; other values mint a new key", () => {
  let n = 0;
  const mint = () => `k${++n}`;
  const first = attemptFor(undefined, "a", mint);
  assert.deepEqual(first, { key: "k1", body: "a", pending: false });
  assert.deepEqual(attemptFor(first, "a", mint), first);
  assert.equal(n, 1);
  assert.deepEqual(attemptFor(first, "b", mint), { key: "k2", body: "b", pending: false });
});

test("a pending attempt is not sent again, and an unknown error replays the same key", () => {
  const attempt = attemptFor(undefined, "a", () => "k1");
  assert.equal(canSend(attempt), true);
  const waiting = afterWrite(attempt, "pending");
  assert.ok(waiting);
  assert.equal(canSend(waiting), false);
  // The same values after pending: still the waiting attempt, same key.
  assert.deepEqual(attemptFor(waiting, "a", () => "other"), waiting);
  // Other values are a deliberate new set and may be sent.
  assert.equal(canSend(attemptFor(waiting, "b", () => "k2")), true);
  // An error keeps the key, so the retry is a replay.
  assert.deepEqual(afterWrite(attempt, "error"), attempt);
  // Accepted and rejected end the attempt: the next set is a new one.
  assert.equal(afterWrite(attempt, "accepted"), undefined);
  assert.equal(afterWrite(attempt, "rejected"), undefined);
});

test("error notices say whether the set was saved and how to retry", () => {
  const server = writeNotice("error", { done: "Logged x.", retry: "Press Done again.", idempotent: true, failure: "server", detail: "weight must be positive" });
  assert.equal(server.kind, "error");
  assert.match(server.text, /^Not saved: weight must be positive\./);
  assert.match(server.text, /Press Done again\./);
  const lost = writeNotice("error", { done: "Logged x.", retry: "Press Done again.", idempotent: true, failure: "unknown" });
  assert.match(lost.text, /may or may not have been saved/);
  assert.match(lost.text, /Press Done again/);
  assert.match(lost.text, /nothing is recorded twice/);
  const lostEdit = writeNotice("error", { done: "Corrected x.", retry: "Press Save again.", idempotent: false, failure: "unknown" });
  assert.match(lostEdit.text, /may or may not have been applied/);
  assert.match(lostEdit.text, /Refresh/);
  assert.doesNotMatch(lostEdit.text, /nothing is recorded twice/);
  assert.deepEqual(writeNotice("accepted", { done: "Logged x.", retry: "", idempotent: true, failure: "server" }), { kind: "ok", text: "Logged x." });
  const pending = writeNotice("pending", { done: "Logged x.", retry: "", idempotent: true, failure: "server" });
  assert.equal(pending.kind, "pending");
  assert.match(pending.text, /waiting for your approval/);
  assert.equal(writeNotice("rejected", { done: "", retry: "", idempotent: true, failure: "server" }).kind, "error");
});

test("a set logged elsewhere makes the card stale before it logs", () => {
  assert.equal(isStale(7, snapshot(7)), false);
  assert.equal(isStale(7, snapshot(8)), true);
  assert.equal(isStale(undefined, snapshot()), false);
  assert.equal(isStale(undefined, snapshot(3)), true);
  assert.equal(isStale(7, snapshot()), true);
});
