// The rest timer's pure model: what the view shows at a given moment, from the
// snapshot render_rest_timer returned and the time elapsed locally since it
// arrived. It never compares the device clock with the server clock (they can
// disagree by minutes); it adds a locally measured duration to the server's own
// elapsed seconds. The server stays the truth: a refresh replaces the snapshot.

export type RestState = "none" | "resting" | "ready" | "superset" | "idle";

// Rest as live_workout reports it (internal/training/live.go, Rest).
export interface Rest {
  target_seconds: number;
  state: RestState;
  since?: string;
  elapsed_seconds?: number;
  remaining_seconds?: number;
  over_by_seconds?: number;
}

// The structured result of render_rest_timer (internal/mcpsrv, RestTimerOut).
export interface RestTimerData {
  now: string;
  rest: Rest;
  idle_after_seconds: number;
  next_exercise?: string;
  next_superset?: boolean;
  last_set?: { exercise: string; weight_kg: number; reps: number };
  weight_unit?: string;
}

export interface TimerView {
  state: RestState;
  targetSeconds: number;
  elapsedSeconds: number;
  remainingSeconds: number;
  overBySeconds: number;
}

const STATES: readonly RestState[] = ["none", "resting", "ready", "superset", "idle"];

// parseTimerData accepts a tool result's structuredContent and returns it typed,
// or undefined when it is not a render_rest_timer result. The view then keeps
// what it showed rather than inventing a state.
export function parseTimerData(value: unknown): RestTimerData | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  const v = value as Record<string, unknown>;
  const rest = v.rest as Record<string, unknown> | undefined;
  if (typeof v.now !== "string" || typeof rest !== "object" || rest === null) return undefined;
  if (!STATES.includes(rest.state as RestState)) return undefined;
  if (!Number.isInteger(rest.target_seconds) || (rest.target_seconds as number) <= 0) return undefined;
  if (!Number.isInteger(v.idle_after_seconds) || (v.idle_after_seconds as number) <= 0) return undefined;
  return value as RestTimerData;
}

// project is the view localMs milliseconds after the snapshot arrived. A
// running rest moves from resting to ready at the target and to idle past the
// idle threshold, the same rules as training.RestState. none and superset do
// not run: nothing was logged, or the next exercise follows without rest.
export function project(data: RestTimerData, localMs: number): TimerView {
  const target = data.rest.target_seconds;
  const base: TimerView = {
    state: data.rest.state,
    targetSeconds: target,
    elapsedSeconds: data.rest.elapsed_seconds ?? 0,
    remainingSeconds: 0,
    overBySeconds: 0,
  };
  if (data.rest.state !== "resting" && data.rest.state !== "ready") return base;
  const elapsed = (data.rest.elapsed_seconds ?? 0) + Math.max(0, Math.floor(localMs / 1000));
  if (elapsed > data.idle_after_seconds) return { ...base, state: "idle", elapsedSeconds: elapsed };
  if (elapsed < target) {
    return { ...base, state: "resting", elapsedSeconds: elapsed, remainingSeconds: target - elapsed };
  }
  return { ...base, state: "ready", elapsedSeconds: elapsed, overBySeconds: elapsed - target };
}

// clock formats seconds as m:ss.
export function clock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

// headline is the short status shown above the clock. It is constant within
// a state, so the aria-live region announces state changes, never every second.
export function headline(view: TimerView, data: RestTimerData): string {
  switch (view.state) {
    case "none":
      return "No set logged today yet";
    case "resting":
      return "Resting";
    case "ready":
      return "Rest done";
    case "superset":
      return data.next_exercise ? `Superset: go to ${data.next_exercise}` : "Superset: next exercise, no rest";
    case "idle":
      return "No rest running";
  }
}
