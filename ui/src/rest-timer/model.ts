// The rest timer's pure model: what the view shows at a given moment, from the
// snapshot render_rest_timer returned and the time elapsed locally since it
// arrived. It never compares the device clock with the server clock (they can
// disagree by minutes); it adds a locally measured duration to the server's own
// elapsed seconds. The server stays the truth: a refresh replaces the snapshot.

export type RestState = "none" | "resting" | "ready" | "superset" | "idle" | "stopped";

// Rest as live_workout reports it (internal/training/live.go, Rest).
export interface Rest {
  target_seconds: number;
  state: RestState;
  since?: string;
  elapsed_seconds?: number;
  remaining_seconds?: number;
  over_by_seconds?: number;
  // How long the person rested, once they stopped the rest (state stopped).
  rested_seconds?: number;
}

// The last logged set; the view edits it and stops the rest after it by id.
export interface LastSet {
  id: number;
  exercise: string;
  weight_kg: number;
  reps: number;
}

// The set the view offers to log next (internal/mcpsrv, RestTimerNext).
export interface NextSet {
  exercise: string;
  weight_kg?: number;
  reps?: number;
  done_sets?: number;
  target_sets?: number;
}

// The structured result of render_rest_timer (internal/mcpsrv, RestTimerOut).
export interface RestTimerData {
  now: string;
  rest: Rest;
  idle_after_seconds: number;
  next_exercise?: string;
  next_superset?: boolean;
  last_set?: LastSet;
  next_set?: NextSet;
  weight_unit?: string;
}

export interface TimerView {
  state: RestState;
  targetSeconds: number;
  elapsedSeconds: number;
  remainingSeconds: number;
  overBySeconds: number;
}

const STATES: readonly RestState[] = ["none", "resting", "ready", "superset", "idle", "stopped"];

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
// not run: nothing was logged, or the next exercise follows without rest. A
// stopped rest shows the seconds the person rested. extraSeconds is what +30 s
// added on this screen: it moves the target of this countdown only.
export function project(data: RestTimerData, localMs: number, extraSeconds = 0): TimerView {
  const target = data.rest.target_seconds + Math.max(0, extraSeconds);
  if (data.rest.state === "stopped") {
    const rested = data.rest.rested_seconds ?? 0;
    return { state: "stopped", targetSeconds: target, elapsedSeconds: rested, remainingSeconds: 0, overBySeconds: 0 };
  }
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
    case "stopped":
      return "Rest stopped";
  }
}

const KG_PER_LB = 0.45359237;

// toUnit is a weight stored in kg as the person reads it, to 0.1.
export function toUnit(kg: number, unit: string | undefined): number {
  const v = unit === "lb" ? kg / KG_PER_LB : kg;
  return Math.round(v * 10) / 10;
}

// toKg is a weight the person typed in their unit, in kg as every tool takes it.
export function toKg(value: number, unit: string | undefined): number {
  const v = unit === "lb" ? value * KG_PER_LB : value;
  return Math.round(v * 10_000) / 10_000;
}

// weightStep is one tap of the weight stepper: the smallest usual plate pair.
export function weightStep(unit: string | undefined): number {
  return unit === "lb" ? 5 : 2.5;
}

export type WriteOutcome = "accepted" | "pending" | "rejected" | "error";

// writeOutcome reads a write tool's result: every write is a proposal, applied
// at once (accepted), waiting for the person (pending) or refused (rejected).
export function writeOutcome(result: { isError?: boolean; structuredContent?: unknown; [key: string]: unknown }): WriteOutcome {
  if (result.isError) return "error";
  const status = (result.structuredContent as { status?: unknown } | undefined)?.status;
  return status === "accepted" || status === "pending" || status === "rejected" ? status : "error";
}

// RPE_CHOICES are the efforts the next-set card offers. Anything else is left
// to the chat, where the person can say 6.5 or 9.5.
export const RPE_CHOICES: readonly number[] = [7, 8, 9, 10];

export interface SetInput {
  weightKg: number;
  reps: number;
  warmup: boolean;
  // undefined: the person did not rate it. Then rpe is not sent at all, and the
  // server stores the set as unrated (rpe_imputed), never as a stated 8.
  rpe?: number;
}

// setPayload is one set of a log_sets call from the card.
export function setPayload(input: SetInput): Record<string, unknown> {
  const set: Record<string, unknown> = { weight_kg: input.weightKg, reps: input.reps };
  if (input.warmup) set.category = "warmup";
  if (input.rpe !== undefined) set.rpe = input.rpe;
  return set;
}
