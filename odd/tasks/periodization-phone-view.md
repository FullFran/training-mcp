# Periodization, phone view (slice 5)

Objective: the PWA shows and edits by hand the active goal, the active block with its microcycles, the current rotation, what is due next, off-plan sessions and the goal's other blocks, through the same domain calls as the MCP tools, with an empty state that never blocks single-routine use.
Branch: `feat/periodization-phone-view` from `main` (55d6fe91). Delivery: one PR. Test runner: `go test ./internal/... ./cmd/...`. Refs #41.

## Specs

- S1 (owner, verbatim, 2026-11-22): "vamos a ver que podemos sacar para hacerlo lo menos opinado y general posible la planificación para que cada uno se pueda montar lo que quiera y a futuro meter los paquetes de conocimiento"
- S2 (owner, verbatim, 2026-11-22): "por secuencia! pero lo ideal es que sea configurable :v que haya libertad para la gente no que te obligue 100% y que sea mutable habras visto en los videos que la planificación tiene que ser por aproximaciones iterativas sucesivas y no puede estar escrita en piedra"
- S3 (owner, verbatim, 2026-11-25): "esta tiene que ser la mejor app para planificar el entrenamiento de forma no opinada que existe. el objetivo es \"vibe entrenar\" auto regularse y planificarse el entrenamiento de la misma forma que usamos ia para programar."
- S4 (coordinator): goal, block and microcycles on the phone; empty state saying blocks are optional; hand edits (goal, block create and edit, microcycle label, deltas and rotation, activate, close or skip the current microcycle, close the block, move a session) through the domain operations, forward only with the refusal shown; the due routine starts through the existing start flow; unopinionated; peninsular Spanish; graceful offline; accessibility basics; no change to `internal/training`, MCP tools, `ui/` or migrations.

Assumptions: the screen's forms are the person acting, so they write directly, like Rutinas, Memoria and the bodyweight log; proposals stay for agent writes (`docs/architecture.md`, "Agent writes are proposals"). The overview shows the active block in full and the other blocks as a list; `?bloque=ID` opens any block in full. Off-plan sessions are the block's whole off-plan list, not only those inside the current microcycle's dates. A new block is always a sketch with no length filled in; sketch or planned is chosen when editing.

## Tasks

- [x] T1 (S1-S4) inline: `GET /blocks` and `POST /blocks/{goal,save,microcycle,advance,assign}` in `internal/websrv/blocks.go`, template `templates/blocks.html`, chip **Bloques** in Planificar, appended `pz-` styles, README line; handler tests (empty state, sketch, active block with a done and a current microcycle, due start through `POST /plan`, forward-only refusal shown, advance and move, block edit, CSRF on every form) and isolation cases for every new route. Risk: none (new routes and screen; no domain, schema or MCP change). Commit: see PR.

Known (not fixed in this slice):
- K1: the domain refuses with a bare `ErrValidation`, so each form's refusal text is written per action in the web layer and names the possible reasons rather than the exact one.
- K2: the PWA is only served behind the provider's web login, so the local run used a scratch preview that serves the same `websrv` handler with a fixed user; `go run ./cmd/training-mcp` was booted only for `/health`.

Route evidence: bounded writer in its own worktree; new code in new files, shared files touched only by appended lines (`server.go` routes and one `pageData` field, `index.html` chip, `app.css`, README).

## Log

- L1 (2026-11-25): test-first. `blocks_test.go` and `blocks_isolation_test.go` failed (empty state, create, active block, edits; isolation panicked on the missing routes) before `blocks.go` and the template existed, then passed. Isolation: another person's goal, block, microcycle, session or routine answers byte for byte like a missing id, with `goals`, `blocks`, `block_microcycles`, `block_rotations` and `sessions` unchanged, while the owner's own post succeeds and changes rows.
- L2 (2026-11-25): `gofmt -l .` empty, `go vet ./...` clean, `go test ./internal/... ./cmd/...` all packages ok. Screenshots at 390x844 with headless Chromium from the preview: `docs/screenshots/app/58-bloques-vacio.png` (empty state) and `59-bloque-activo.png` (active block, due routine, current rotation), both looked at.
