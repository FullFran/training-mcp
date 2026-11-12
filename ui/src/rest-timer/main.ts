// The rest-timer view, rendered by an MCP Apps host for render_rest_timer
// (ui://vibetraining/rest-timer/v2). It talks to the host only through the
// official ext-apps App bridge: the tool result arrives as a notification, and
// every action calls a server tool through the host, which runs it as the
// signed-in person. The view holds no credentials and calls no URL.
//
// Writes use the same tools the agent uses (stop_rest, log_sets, update_set),
// so each is a proposal decided by the person's approval mode: applied at once,
// or waiting on the review page, which the view says.

import {
  App,
  applyDocumentTheme,
  applyHostFonts,
  applyHostStyleVariables,
  type McpUiHostContext,
} from "@modelcontextprotocol/ext-apps";
import {
  clock,
  headline,
  parseTimerData,
  project,
  toKg,
  toUnit,
  weightStep,
  writeOutcome,
  type RestTimerData,
  type TimerView,
  type WriteOutcome,
} from "./model.ts";

const RENDER = "render_rest_timer";
const RING = 326.73; // circumference of the r=52 ring

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const el = {
  status: $("status"),
  clock: $("clock"),
  sub: $("sub"),
  arc: $("arc") as unknown as SVGCircleElement,
  controls: $("controls"),
  plus: $<HTMLButtonElement>("plus"),
  stop: $<HTMLButtonElement>("stop"),
  next: $("next"),
  nextName: $("next-name"),
  nextCount: $("next-count"),
  weight: $<HTMLInputElement>("weight"),
  reps: $<HTMLInputElement>("reps"),
  unit: $("unit"),
  warmup: $<HTMLInputElement>("warmup"),
  done: $<HTMLButtonElement>("done"),
  last: $("last"),
  lastText: $("last-text"),
  edit: $<HTMLButtonElement>("edit"),
  editForm: $<HTMLFormElement>("edit-form"),
  editWeight: $<HTMLInputElement>("edit-weight"),
  editReps: $<HTMLInputElement>("edit-reps"),
  editCancel: $<HTMLButtonElement>("edit-cancel"),
  notice: $("notice"),
  refresh: $<HTMLButtonElement>("refresh"),
};

let data: RestTimerData | undefined;
let receivedAt = 0;
let tick: ReturnType<typeof setInterval> | undefined;
// Seconds +30 s added to this rest, on this screen only.
let extra = 0;
// The next set the inputs were filled from, so a refresh never overwrites
// what the person is typing for the same set.
let filledFor = "";
// One key per set to log: a retry after a lost answer replays, never doubles.
// It is only reused for the same body: a retry with other values is a new set.
let logKey = crypto.randomUUID();
let logBody = "";

function unit(): string {
  return data?.weight_unit === "lb" ? "lb" : "kg";
}

function view(): TimerView | undefined {
  return data && project(data, performance.now() - receivedAt, extra);
}

function draw(): void {
  const v = view();
  if (!data || !v) return;
  const text = headline(v, data);
  // aria-live: headline is constant within a state, so only changes announce.
  if (el.status.textContent !== text) el.status.textContent = text;
  document.body.dataset.state = v.state;
  const running = v.state === "resting" || v.state === "ready";
  el.clock.textContent = clockText(v);
  el.sub.textContent = subText(v);
  const progress = running ? Math.min(1, v.elapsedSeconds / v.targetSeconds) : v.state === "stopped" ? 1 : 0;
  el.arc.style.strokeDashoffset = String(RING * (1 - progress));
  el.controls.hidden = !running;
  if (running && !tick) tick = setInterval(draw, 1000);
  if (!running && tick) {
    clearInterval(tick);
    tick = undefined;
  }
}

function clockText(v: TimerView): string {
  switch (v.state) {
    case "resting":
      return clock(v.remainingSeconds);
    case "ready":
      return `+${clock(v.overBySeconds)}`;
    case "stopped":
      return clock(v.elapsedSeconds);
    default:
      return clock(v.targetSeconds);
  }
}

function subText(v: TimerView): string {
  switch (v.state) {
    case "resting":
      return `of ${clock(v.targetSeconds)}${extra ? ` (+${extra} s)` : ""}`;
    case "ready":
      return "Time to lift";
    case "stopped":
      return "rested";
    case "superset":
      return "No rest in a superset";
    case "idle":
      return "Log a set to start a rest";
    default:
      return "rest target";
  }
}

function title(name: string): string {
  return name.charAt(0).toUpperCase() + name.slice(1);
}

function weightText(kg: number): string {
  return `${toUnit(kg, unit())} ${unit()}`;
}

function drawSets(): void {
  if (!data) return;
  el.unit.textContent = unit();
  const next = data.next_set;
  el.next.hidden = !next;
  if (next) {
    el.nextName.textContent = next.exercise;
    const count = next.target_sets ? `${Math.min((next.done_sets ?? 0) + 1, next.target_sets)} / ${next.target_sets}` : "";
    el.nextCount.hidden = !count;
    el.nextCount.textContent = count ? `Set ${count}` : "";
    const key = `${next.exercise}|${next.done_sets ?? 0}|${data.last_set?.id ?? 0}`;
    if (key !== filledFor) {
      filledFor = key;
      el.weight.value = next.weight_kg ? String(toUnit(next.weight_kg, unit())) : "";
      el.reps.value = next.reps ? String(next.reps) : "";
      el.warmup.checked = false;
      logKey = crypto.randomUUID();
    }
  }
  const last = data.last_set;
  el.last.hidden = !last;
  if (last) el.lastText.textContent = `${title(last.exercise)} · ${weightText(last.weight_kg)} × ${last.reps}`;
}

function accept(structured: unknown): void {
  const parsed = parseTimerData(structured);
  if (!parsed) return;
  // +30 s belongs to the rest it was pressed in.
  if (parsed.rest.since !== data?.rest.since || parsed.rest.state === "stopped") extra = 0;
  data = parsed;
  receivedAt = performance.now();
  draw();
  drawSets();
}

function notify(kind: "ok" | "pending" | "error", text: string): void {
  el.notice.dataset.kind = kind;
  el.notice.textContent = text;
  el.notice.hidden = false;
}

function applyContext(ctx: McpUiHostContext | undefined): void {
  if (!ctx) return;
  if (ctx.theme) applyDocumentTheme(ctx.theme);
  if (ctx.styles?.variables) applyHostStyleVariables(ctx.styles.variables);
  if (ctx.styles?.css?.fonts) applyHostFonts(ctx.styles.css.fonts);
}

const app = new App({ name: "vibeTraining rest timer", version: "2.0.0" }, {});

app.ontoolresult = (params) => accept(params.structuredContent);
app.onhostcontextchanged = (params) => applyContext(params as McpUiHostContext);

async function refresh(): Promise<void> {
  el.refresh.disabled = true;
  try {
    const result = await app.callServerTool({ name: RENDER, arguments: {} });
    if (!result.isError) accept(result.structuredContent);
  } catch {
    // The host may refuse tool calls from views; keep what is shown.
  } finally {
    el.refresh.disabled = false;
  }
}

// write calls one write tool and says what happened. The model is told too,
// so the chat knows what the person did in the view.
async function write(button: HTMLButtonElement, name: string, args: Record<string, unknown>, done: string): Promise<WriteOutcome> {
  button.disabled = true;
  let outcome: WriteOutcome = "error";
  let detail = "";
  try {
    const result = await app.callServerTool({ name, arguments: args });
    outcome = writeOutcome(result);
    if (outcome === "error") {
      const first = result.content?.[0];
      detail = first && first.type === "text" ? first.text : "";
    }
  } catch (err) {
    detail = err instanceof Error ? err.message : "";
  } finally {
    button.disabled = false;
  }
  switch (outcome) {
    case "accepted":
      notify("ok", done);
      break;
    case "pending":
      notify("pending", `${done.replace(/\.$/, "")}: waiting for your approval in vibeTraining.`);
      break;
    case "rejected":
      notify("error", "Not saved: this action is set to be refused.");
      break;
    default:
      notify("error", detail ? `Not saved: ${detail}` : "Not saved. Try again.");
  }
  if (outcome !== "error") {
    void app
      .updateModelContext({ content: [{ type: "text", text: `From the rest timer (${outcome}): ${done}` }] })
      .catch(() => {});
    await refresh();
  }
  return outcome;
}

function readNumber(input: HTMLInputElement): number {
  const v = Number(input.value.replace(",", "."));
  return Number.isFinite(v) ? v : NaN;
}

el.plus.addEventListener("click", () => {
  extra += 30;
  draw();
});

el.stop.addEventListener("click", () => {
  const v = view();
  const last = data?.last_set;
  if (!v || !last) return;
  const rested = v.elapsedSeconds;
  void write(el.stop, "stop_rest", { set_id: last.id, rest_seconds: rested }, `Stopped the rest after ${last.exercise} at ${clock(rested)}.`);
});

for (const button of document.querySelectorAll<HTMLButtonElement>(".stepper button")) {
  button.addEventListener("click", () => {
    const weight = button.dataset.field === "weight";
    const input = weight ? el.weight : el.reps;
    const step = weight ? weightStep(unit()) : 1;
    const current = readNumber(input) || 0;
    const next = Math.max(weight ? 0 : 1, Math.round((current + step * Number(button.dataset.dir)) * 10) / 10);
    input.value = String(next);
  });
}

el.done.addEventListener("click", () => {
  const next = data?.next_set;
  if (!next) return;
  const weight = readNumber(el.weight);
  const reps = readNumber(el.reps);
  if (!(weight > 0) || !Number.isInteger(reps) || reps < 1) {
    notify("error", "Enter a weight above 0 and whole reps.");
    (weight > 0 ? el.reps : el.weight).focus();
    return;
  }
  // An untouched weight keeps its stored kg; converting the rounded lb back
  // would log 80.0137 kg for 80.
  const kg = next.weight_kg && weight === toUnit(next.weight_kg, unit()) ? next.weight_kg : toKg(weight, unit());
  const set: Record<string, unknown> = { weight_kg: kg, reps };
  if (el.warmup.checked) set.category = "warmup";
  const body = JSON.stringify([next.exercise, set]);
  if (body !== logBody) {
    logBody = body;
    logKey = crypto.randomUUID();
  }
  const label = `${el.warmup.checked ? "warm-up " : ""}${next.exercise} ${weight} ${unit()} × ${reps}`;
  void write(el.done, "log_sets", { idempotency_key: logKey, exercise: next.exercise, sets: [set] }, `Logged ${label}.`).then(
    (outcome) => {
      if (outcome !== "error") logKey = crypto.randomUUID();
    },
  );
});

el.edit.addEventListener("click", () => {
  const last = data?.last_set;
  if (!last) return;
  el.editWeight.value = String(toUnit(last.weight_kg, unit()));
  el.editReps.value = String(last.reps);
  el.editForm.hidden = false;
  el.edit.hidden = true;
  el.editWeight.focus();
});

function closeEdit(): void {
  el.editForm.hidden = true;
  el.edit.hidden = false;
}
el.editCancel.addEventListener("click", closeEdit);

el.editForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const last = data?.last_set;
  if (!last) return;
  const weight = readNumber(el.editWeight);
  const reps = readNumber(el.editReps);
  if (!(weight > 0) || !Number.isInteger(reps) || reps < 1) {
    notify("error", "Enter a weight above 0 and whole reps.");
    return;
  }
  // Only what changed is sent: a weight shown rounded in lb must not rewrite
  // the stored kg when the person only fixed the reps.
  const args: Record<string, unknown> = { set_id: last.id };
  if (weight !== toUnit(last.weight_kg, unit())) args.weight_kg = toKg(weight, unit());
  if (reps !== last.reps) args.reps = reps;
  if (Object.keys(args).length === 1) {
    closeEdit();
    return;
  }
  const save = el.editForm.querySelector<HTMLButtonElement>("button[type=submit]")!;
  void write(save, "update_set", args, `Corrected ${last.exercise} to ${weight} ${unit()} × ${reps}.`).then(
    (outcome) => {
      if (outcome !== "error") closeEdit();
    },
  );
});

el.refresh.addEventListener("click", () => void refresh());
// Coming back to the tab re-reads the server truth: a set logged elsewhere, a
// changed target. While hidden nothing is polled.
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && data) void refresh();
});

await app.connect();
applyContext(app.getHostContext());
