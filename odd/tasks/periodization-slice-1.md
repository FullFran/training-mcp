# Periodization, slice 1

Objective: optional, non-opinionated planning above the routine: free-text goals, blocks of microcycles with an intent and a rotation of routines, advanced by sequence or by calendar, with sessions placed on their own and never rejected.
Branch: `feat/periodization-slice-1` from `main` (293e4abc). Delivery: one PR. Test runner: `go test ./internal/... ./cmd/...`. Refs #41.

## Specs

- S1 (owner, verbatim, 2026-11-22): "vamos a ver que podemos sacar para hacerlo lo menos opinado y general posible la planificación para que cada uno se pueda montar lo que quiera y a futuro meter los paquetes de conocimiento"
- S2 (owner, verbatim, 2026-11-22): "por secuencia! pero lo ideal es que sea configurable :v que haya libertad para la gente no que te obligue 100% y que sea mutable habras visto en los videos que la planificación tiene que ser por aproximaciones iterativas sucesivas y no puede estar escrita en piedra"
- S3 (owner, verbatim, 2026-11-22): "es que al final tiene que haber algo de flexibilidad"
- S4 (owner, verbatim, 2026-11-25): "Quiero avanzar lo máximo posible en este proyecto. [...] sigue la filosofía de HCS y esta tiene que ser la mejor app para planificar el entrenamiento de forma no opinada que existe. el objetivo es \"vibe entrenar\" auto regularse y planificarse el entrenamiento de la misma forma que usamos ia para programar."
- S5 (approved design, #41 comments "Design proposal" and "Open questions resolved"): goal, block (`sequence` default or `calendar`, `microcycle_days` 7 by default, status `sketch|planned|active|done`), microcycle (label, optional sets and effort deltas, optional own rotation); sessions auto-linked while a block is active, off-plan when they fit nowhere, link editable; edits forward only; at most 7 tools; single-routine use unchanged.

Assumptions: a block closes only by hand, in both modes; a sequence microcycle closes as soon as its rotation is done (and, if found full, on the next placement); a microcycle with an empty rotation never closes by itself; calendar placement follows dates whatever a microcycle's status; switching calendar to sequence marks elapsed pending microcycles done, and sequence to calendar starts the first pending one today; shrinking a block is a deleting action (`block.shorten`); modifiers are stored and shown, never applied (out of scope).

## Tasks

- [x] T1 (S1-S3, S5) inline: migration 030 (`goals`, `blocks`, `block_microcycles`, `block_rotations`, `sessions.block_id`, `sessions.block_microcycle_id`, `sessions.block_off_plan`); domain, `PeriodizationStore` port, service, SQLite adapter, undo tables; auto-placement on start, log and add set. Risk: item 1 (migration, additive only). Commit: `6e6feed5`.
- [x] T2 (S4, S5) inline: MCP tools `training_status`, `list_blocks`, `get_block`, `set_goal`, `save_block`, `advance_block`, `assign_session_block`; annotations, isolation cases, action types, coach step labels, review labels, `docs/tools.md`. Risk: none (new tools; existing tools unchanged). Commit: `ed53873b`.

Route evidence: sequential units (T2 depends on T1's service); bounded writer in its own worktree, parallel to four other writers; migration number 030 reserved by the orchestrator.

## Log

- L1 (2026-11-25): T1 test-first in `internal/sqlitestore/periodization_test.go`: migration from a 029 database with data keeps every row and leaves sessions unplaced; sketch needs only intent and length; sequence advance by done or skipped; calendar by date; mode change mid-block; forward-only edits; link editing; goals; status; training without an active block identical to a store without the port; every port method isolates accounts.
- L2 (2026-11-25): T2 test-first: tool list, annotations and count 59 failed before wiring, then passed; undo of every write tool restores the exact prior rows; a start inside an active block is placed and its undo reopens the microcycle; a shrink waits under `accept_except_delete`, applies on accept and undoes. `gofmt -l .` empty, `go vet ./...` clean, `go test ./internal/... ./cmd/...` 1279 passed.
- L3 (2026-11-25): the PR conflicted with main after the skills (#138) and exercise library (#139) merges; merged main (`06484184`) as the union of the appended lines, tool count 65, migration 031 applies after 030. `go test ./internal/... ./cmd/...` 1358 passed.
- L4 (2026-11-25): scope notes: README tool count ("35 tools") was already stale and lists no tools, left untouched; no PWA view, modifier application, microcycle review or progression rule (later slices).
