# MCP Apps

Some tools come with an interactive view that a chat can render inline. This
uses the MCP Apps extension (`io.modelcontextprotocol/ui`,
[specification 2026-01-26](https://github.com/modelcontextprotocol/ext-apps/blob/main/specification/2026-01-26/apps.mdx)),
which ChatGPT, Claude and other hosts implement. It is a view over the same
tools, not a second app: every view reads through a normal tool, and a chat
without MCP Apps gets that tool's text instead.

## How a view reaches the chat

1. The tool lists `_meta.ui.resourceUri`, for example
   `ui://vibetraining/rest-timer/v4`. It also sends the deprecated flat
   `_meta["ui/resourceUri"]`, as the official `registerAppTool` does.
2. A host that supports MCP Apps reads that resource (`resources/read`,
   MIME `text/html;profile=mcp-app`) and renders it in a sandboxed iframe.
3. The host passes the tool result to the view (`ui/notifications/tool-result`).
   The view can call tools again through the host (`tools/call` over the
   bridge). The host runs those calls as the signed-in person, so the view
   never holds a token or calls a URL.

The server is stateless, so `tools/list` cannot see whether the client
declared the extension. A tool always carries its `_meta.ui`, and every result
also carries a readable text. Hosts without MCP Apps ignore the metadata and
show the text. The specification's "servers SHOULD check client capabilities
before registering UI-enabled tools" is the one rule this does not meet.

## Views

| View | Tool | What it shows |
|---|---|---|
| `ui://vibetraining/rest-timer/v4` | `render_rest_timer` | The rest since the last logged set: a countdown while `resting`, time over the target when `ready`, the seconds rested once `stopped`, the next exercise in a `superset`, and nothing running when `none` or `idle`. Below it, the next set, with a picker to switch to any exercise of today's plan (shown with its progress, such as "Chest press · 2/3"; without a plan, the exercises done today) and a "2/3 sets done" line (weight and reps prefilled from the last set of the chosen exercise today, editable, with an optional RPE of 7 to 10 and a warm-up box) and the last set (editable). |

The view's buttons call the same tools the agent uses, so the person's
approval mode decides each one, and the view says when a write waits on the
review page: **Stop rest** calls `stop_rest` with the set and the seconds on
screen, **Done** calls `log_sets` (one idempotency key per set, reused on a
retry; the RPE is sent only when the person picks one, so "No RPE" stays an
unrated, `rpe_imputed` set), and editing the last set calls `update_set` with only the changed
fields. **+30 s** only extends the countdown on screen; it never changes the
person's target. After each write the view tells the model what happened
(`ui/update-model-context`) and re-reads the timer. There is no app-only
write tool: a tool hidden from the model is hidden by the host, not the
server, so it would not be a boundary (#125).

A refresh updates the progress counts but never overwrites the exercise or values the person is editing; a logged set resets the card to what the server offers next. Logging still goes through `log_sets` with the chosen exercise: there is no new write tool.

`render_rest_timer` takes one optional argument, `mode`: `compact` shows only the
countdown (a clock, a completion mark and an accessible label; no set details,
picker, progress or controls), `full` shows the whole workout view. Without it
the view is the full one, as before. Asking "ponme solo el crono" should be
answered with `mode: "compact"`; the tool description says so, but a host may
still choose its own call. **Show workout** and **Timer only** expand and
collapse the same view: they change presentation only, never restart the rest,
write an interval or call the server, and the mode the person picks is kept
through refreshes and logged sets (logging a set does not switch it). Both modes
count from the same `rest` state (`since`, `elapsed_seconds`, `target_seconds`)
plus the time measured on the device, so a refresh in either shows the real
remaining seconds.

Done is safe to press twice. Before it logs, the view re-reads the session: if a
set was logged elsewhere (the phone app, the chat) since the card was filled,
nothing is logged and the card updates so the person looks again. While a set
waits for approval the view does not send it again, and the server also points
a repeated `log_sets` (same `idempotency_key` and sets) at the proposal already
waiting instead of queueing a second one. A retry after a lost answer replays
the same key. Errors say what happened: "Not saved" with the server's reason and
how to retry, or "may or may not have been saved" when no answer came back.

The rest timer counts locally from the server's `rest.elapsed_seconds` plus
the time measured on the device since the result arrived. It never compares
the device clock with the server's, so a skewed phone clock does not move it.
It reads the server again when you press Refresh or come back to the tab,
never every second. A `log_sets` that waits for approval logs nothing yet, so
it never starts the timer. The phone app's own timer is unchanged.

## Building

Views are TypeScript in `ui/`, built on the official
`@modelcontextprotocol/ext-apps` SDK (`App`, no framework). `ui/build.mjs`
bundles each view with esbuild into a single self-contained HTML file in
`internal/mcpsrv/apps/`, which the Go server embeds with `go:embed`. The
built files are committed, so `go build` and `go test` never need Node.

```sh
cd ui
npm ci
npm run check         # tsc --noEmit
npm test              # node --test, the pure models
npm run build         # writes internal/mcpsrv/apps/*.html
npm run check:bundle  # fails when a committed file is stale (CI runs it)
```

The rest timer is 654 KiB (145 KiB gzipped). zod, which the SDK imports
whole, is 444 KiB of that. How often each host reads the resource again is
not measured yet.

## Adding a view

1. Add `ui/src/<view>/index.html`, with a `<!--SCRIPT-->` marker where the
   bundle goes, and `ui/src/<view>/main.ts`. Put the logic you can test without
   a DOM in a separate module with a `*.test.ts` next to it.
2. Add `{ src: "<view>", out: "<view>.html" }` to `views` in `ui/build.mjs`
   and run `npm run build`.
3. In `internal/mcpsrv/apps.go`, embed the file, register the resource with
   `appMIMEType` under a versioned `ui://vibetraining/<view>/v1`, and give the
   tool `Meta: appToolMeta(uri)`. The tool returns its own text content, which
   is the fallback, plus a typed output, which becomes the view's
   `structuredContent`.
4. A view reads through tools that already exist. Writes still go through the
   proposal path (`addWrite`). A view must not add a write of its own.
5. Give the tool an entry in `toolBehaviors` and an isolation case
   (`TestEveryToolHasAnIsolationCase` fails without one), and test the
   resource and the text as `apps_test.go` does.

Change a view's behavior in a way an open chat would misread, and the URI
moves to `/v2`. Hosts may cache a resource by URI.

## Testing in a host

Automated tests cover the protocol side: the tool's `_meta`, the resource's
MIME and contents, the text fallback, isolation between people, and the
timer model. Whether a host actually renders a view can only be checked in
that host, on a real authenticated connection:

1. Deploy, then connect the chat to `https://training.fullfran.com/mcp`.
2. Log a set from the chat and ask for the rest timer. The view should show
   the remaining time, not the full target, when asked some seconds later.
3. Change the target in the phone app, return to the chat, and press Refresh.

| Host | Version | Renders | Bridge `tools/call` | Notes |
|---|---|---|---|---|
| ChatGPT | not tested yet | | | |
| Claude | not tested yet | | | |
