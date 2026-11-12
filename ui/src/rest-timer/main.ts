// The rest-timer view, rendered by an MCP Apps host for render_rest_timer
// (ui://vibetraining/rest-timer/v1). It talks to the host only through the
// official ext-apps App bridge: the tool result arrives as a notification, and
// a refresh calls render_rest_timer again through the host, which runs it as
// the signed-in person. The view holds no credentials and calls no URL.

import {
  App,
  applyDocumentTheme,
  applyHostFonts,
  applyHostStyleVariables,
  type McpUiHostContext,
} from "@modelcontextprotocol/ext-apps";
import { clock, headline, parseTimerData, project, type RestTimerData, type TimerView } from "./model.ts";

const TOOL = "render_rest_timer";

const el = {
  status: document.getElementById("status") as HTMLElement,
  clock: document.getElementById("clock") as HTMLElement,
  bar: document.getElementById("bar") as HTMLProgressElement,
  detail: document.getElementById("detail") as HTMLElement,
  refresh: document.getElementById("refresh") as HTMLButtonElement,
};

let data: RestTimerData | undefined;
let receivedAt = 0;
let tick: ReturnType<typeof setInterval> | undefined;

function draw(): void {
  if (!data) return;
  const view = project(data, performance.now() - receivedAt);
  const text = headline(view, data);
  // The status region is aria-live; headline is constant within a state, so
  // writing it only when it differs announces state changes, not seconds.
  if (el.status.textContent !== text) el.status.textContent = text;
  document.body.dataset.state = view.state;
  el.clock.textContent = clockText(view);
  el.bar.hidden = view.state !== "resting" && view.state !== "ready";
  el.bar.max = view.targetSeconds;
  el.bar.value = Math.min(view.elapsedSeconds, view.targetSeconds);
  el.detail.textContent = detailText(data);
  const running = view.state === "resting" || view.state === "ready";
  if (running && !tick) tick = setInterval(draw, 1000);
  if (!running && tick) {
    clearInterval(tick);
    tick = undefined;
  }
}

function clockText(view: TimerView): string {
  switch (view.state) {
    case "resting":
      return clock(view.remainingSeconds);
    case "ready":
      return `+${clock(view.overBySeconds)}`;
    default:
      return "";
  }
}

function detailText(d: RestTimerData): string {
  const parts: string[] = [`Target ${clock(d.rest.target_seconds)}`];
  if (d.last_set) parts.push(`Last: ${d.last_set.exercise}, ${d.last_set.reps} reps`);
  if (d.next_exercise && !d.next_superset) parts.push(`Next: ${d.next_exercise}`);
  return parts.join(" · ");
}

function accept(structured: unknown): void {
  const parsed = parseTimerData(structured);
  if (!parsed) return;
  data = parsed;
  receivedAt = performance.now();
  draw();
}

function applyContext(ctx: McpUiHostContext | undefined): void {
  if (!ctx) return;
  if (ctx.theme) applyDocumentTheme(ctx.theme);
  if (ctx.styles?.variables) applyHostStyleVariables(ctx.styles.variables);
  if (ctx.styles?.css?.fonts) applyHostFonts(ctx.styles.css.fonts);
}

const app = new App({ name: "vibeTraining rest timer", version: "1.0.0" }, {});

app.ontoolresult = (params) => accept(params.structuredContent);
app.onhostcontextchanged = (params) => applyContext(params as McpUiHostContext);

async function refresh(): Promise<void> {
  el.refresh.disabled = true;
  try {
    const result = await app.callServerTool({ name: TOOL, arguments: {} });
    if (!result.isError) accept(result.structuredContent);
  } catch {
    // The host may refuse tool calls from views; keep what is shown.
  } finally {
    el.refresh.disabled = false;
  }
}

el.refresh.addEventListener("click", () => void refresh());
// Coming back to the tab re-reads the server truth: a set logged elsewhere, a
// changed target. While hidden nothing is polled.
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && data) void refresh();
});

await app.connect();
applyContext(app.getHostContext());
