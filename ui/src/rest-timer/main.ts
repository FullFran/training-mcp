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
  afterWrite,
  attemptFor,
  canSend,
  clock,
  headline,
  isStale,
  newKey,
  parseTimerData,
  project,
  RPE_CHOICES,
  setPayload,
  toKg,
  toUnit,
  weightStep,
  writeNotice,
  writeOutcome,
  type LogAttempt,
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
  rpe: $("rpe"),
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
// The set being logged: one key per set, so a retry after a lost answer replays
// and never doubles. It is only reused for the same body: other values are a new
// set. While it waits for approval, Done does not send it again.
let attempt: LogAttempt | undefined;
// The last set id the card was filled for. A different one on the server means
// a set was logged elsewhere (the PWA, the chat) since.
let baselineLastId: number | undefined;
let logging = false;

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
      baselineLastId = data.last_set?.id;
      el.weight.value = next.weight_kg ? String(toUnit(next.weight_kg, unit())) : "";
      el.reps.value = next.reps ? String(next.reps) : "";
      el.warmup.checked = false;
      setRPE(undefined);
      attempt = undefined;
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

// refresh re-reads the server's truth and returns it; undefined when the host
// refused the call or the result was not a timer, and the view keeps what it
// shows.
async function refresh(): Promise<RestTimerData | undefined> {
  el.refresh.disabled = true;
  try {
    const result = await app.callServerTool({ name: RENDER, arguments: {} });
    if (result.isError) return undefined;
    accept(result.structuredContent);
    return parseTimerData(result.structuredContent);
  } catch {
    // The host may refuse tool calls from views; keep what is shown.
    return undefined;
  } finally {
    el.refresh.disabled = false;
  }
}

// How a write is retried, for its error notice.
interface WriteOptions {
  retry: string;
  // The retry replays one idempotency key, so it cannot record twice.
  idempotent: boolean;
}

// write calls one write tool and says what happened, including whether it was
// saved and how to retry. The model is told too, so the chat knows what the
// person did in the view.
async function write(button: HTMLButtonElement, name: string, args: Record<string, unknown>, done: string, options: WriteOptions): Promise<WriteOutcome> {
  button.disabled = true;
  let outcome: WriteOutcome = "error";
  let failure: "server" | "unknown" = "unknown";
  let detail = "";
  try {
    const result = await app.callServerTool({ name, arguments: args });
    outcome = writeOutcome(result);
    if (outcome === "error" && result.isError) {
      failure = "server";
      const first = result.content?.[0];
      detail = first && first.type === "text" ? first.text : "";
    }
  } catch (err) {
    detail = err instanceof Error ? err.message : "";
  } finally {
    button.disabled = false;
  }
  const notice = writeNotice(outcome, { done, detail, failure, ...options });
  notify(notice.kind, notice.text);
  if (outcome !== "error") {
    void app
      .updateModelContext({ content: [{ type: "text", text: `From the rest timer (${outcome}): ${done}` }] })
      .catch(() => {});
    await refresh();
  }
  return outcome;
}

// The effort chosen for the next set; undefined until the person picks one.
let rpe: number | undefined;

function setRPE(value: number | undefined): void {
  rpe = value;
  for (const input of el.rpe.querySelectorAll<HTMLInputElement>("input")) {
    input.checked = input.value === (value === undefined ? "" : String(value));
  }
}

for (const value of [...RPE_CHOICES, undefined]) {
  const label = document.createElement("label");
  const input = document.createElement("input");
  input.type = "radio";
  input.name = "rpe";
  input.value = value === undefined ? "" : String(value);
  input.checked = value === undefined;
  input.addEventListener("change", () => setRPE(value));
  const text = document.createElement("span");
  text.textContent = value === undefined ? "No RPE" : String(value);
  label.append(input, text);
  el.rpe.append(label);
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
  void write(el.stop, "stop_rest", { set_id: last.id, rest_seconds: rested }, `Stopped the rest after ${last.exercise} at ${clock(rested)}.`, {
    retry: "Press Stop rest again.",
    idempotent: false,
  });
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

el.done.addEventListener("click", () => void logNext());

async function logNext(): Promise<void> {
  const next = data?.next_set;
  if (!next || logging) return;
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
  const set = setPayload({ weightKg: kg, reps, warmup: el.warmup.checked, rpe });
  attempt = attemptFor(attempt, JSON.stringify([next.exercise, set]), () => newKey());
  if (!canSend(attempt)) {
    notify("pending", "This set is already waiting for your approval in vibeTraining. Approve it there, or change the values to log another.");
    return;
  }
  const sent = attempt;
  const label = `${el.warmup.checked ? "warm-up " : ""}${next.exercise} ${weight} ${unit()} × ${reps}${rpe === undefined ? "" : ` @ RPE ${rpe}`}`;
  logging = true;
  el.done.disabled = true;
  try {
    // A set logged elsewhere (the PWA) since this card was filled changes what
    // comes next: read the server first, and let the person look again.
    const baseline = baselineLastId;
    const fresh = await refresh();
    if (!fresh) {
      notify("error", "Could not check the session before saving, so nothing was saved. Press Done again.");
      return;
    }
    if (isStale(baseline, fresh)) {
      notify("pending", "A set was logged elsewhere since this card was filled, so it was updated. Check it and press Done again.");
      return;
    }
    const outcome = await write(el.done, "log_sets", { idempotency_key: sent.key, exercise: next.exercise, sets: [set] }, `Logged ${label}.`, {
      retry: "Press Done again.",
      idempotent: true,
    });
    attempt = afterWrite(sent, outcome);
  } finally {
    logging = false;
    el.done.disabled = false;
  }
}

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
  void write(save, "update_set", args, `Corrected ${last.exercise} to ${weight} ${unit()} × ${reps}.`, {
    retry: "Press Save again.",
    idempotent: false,
  }).then(
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
