# Periodization, slices 2 and 3

Objective: today's prescription (a microcycle's `sets_delta` and `effort_delta` applied to the session's copy of its routine at start) and a read-only microcycle review (planned against done, as numbers, with no verdict); plus the two slice 1 leftovers K1 and K2.
Branch: `feat/periodization-slices-2-3` from `main` (55d6fe91). Delivery: one PR. Test runner: `go test ./internal/... ./cmd/...`. Refs #41.

## Specs

- S1 (owner, verbatim, 2026-11-22): "vamos a ver que podemos sacar para hacerlo lo menos opinado y general posible la planificación para que cada uno se pueda montar lo que quiera y a futuro meter los paquetes de conocimiento"
- S2 (owner, verbatim, 2026-11-22): "por secuencia! pero lo ideal es que sea configurable :v que haya libertad para la gente no que te obligue 100% y que sea mutable habras visto en los videos que la planificación tiene que ser por aproximaciones iterativas sucesivas y no puede estar escrita en piedra"
- S3 (owner, verbatim, 2026-11-22): "es que al final tiene que haber algo de flexibilidad"
- S4 (owner, verbatim, 2026-11-25): "esta tiene que ser la mejor app para planificar el entrenamiento de forma no opinada que existe. el objetivo es \"vibe entrenar\" auto regularse y planificarse el entrenamiento de la misma forma que usamos ia para programar."
- S5 (orchestrator brief, 2026-11-25): slice 2 applies the modifiers to `session_items` only (sets within 1-20, RPE only where a target is set, within 1-10), never to the routine or past sessions, shows base, delta and result, best effort, identical without a block; slice 3 is one read-only tool `review_microcycle` with planned against done, single-group and fractional muscle counts, effort with imputed RPE never treated as observed, drop-off, off-plan and extra work apart, feedback, bodyweight and the previous microcycle; no verdict, no recommendation, bounded output.

Assumptions: the applied prescription and how a microcycle closed cannot be derived later, so they are stored (migration 034); a microcycle reopens on delete only when it was closed by placement in the active sequence block and no later microcycle moved on; undoing an activation is refused once any session is linked to the block or a microcycle closed (sessions linked by hand before the activation also hold it); the review's sequence window runs from the day after the previous microcycle closed (or the block's start) to its own close (or today), widened to its sessions.

## Tasks

- [x] T1 (S1-S3, S5) inline: migration 034 (`sessions.block_modifiers`, `block_microcycles.closed_by`); `PrescriptionStore` port and adapter; `StartPrescribedSession`; `start_session` and `session_progress` return `block_modifiers`; `training_status` returns `next_prescription`. Risk: item 1 (migration, additive only); `start_session` output and session copies change while a block with modifiers is active. Commit: `3fdf8d33`.
- [x] T2 (S2, S3) inline: K1, deleting the session that closed a microcycle reopens it; K2, undoing an activation once it was used is refused with an undo conflict. Risk: none (an undo that left inconsistent rows is now refused). Commit: `bdf07651`.
- [x] T3 (S1, S4, S5) inline: `review_microcycle`, annotations, isolation case, coach step label, tool count 72, `docs/tools.md`. Risk: none (new read-only tool). Commit: `71fbf2e6`.
- [x] T4 inline: `docs/architecture.md` describes migration 034. Commit: `bee5758b`.

Known (not done in these slices): no progression rule, no PWA view of blocks or reviews, no block templates or knowledge packs.

Route evidence: sequential units in one bounded writer worktree; migration number 034 and the shared-file rules set by the orchestrator.

## Log

- L1 (2026-11-25): T1 test-first in `internal/sqlitestore/periodization_prescription_test.go` and `internal/mcpsrv/periodization_prescription_test.go`: modifiers applied, clamped and stored, routine unchanged, a later edit leaves the past copy alone; identical start without modifiers; a failing prescription never fails the start; `next_prescription`; two-user isolation of the port; migration 034 keeps rows; `closed_by` for every closing path; undo of `start_session` restores the copy.
- L2 (2026-11-25): T2 test-first in `internal/sqlitestore/periodization_leftovers_test.go` and `internal/mcpsrv/periodization_leftovers_test.go`: reopen and the four stays-closed cases; a reopen failure never fails the delete; delete then undo restores every row; an activation undo after a placed start is refused and changes nothing, and succeeds once the start is undone.
- L3 (2026-11-25): T3: the domain code was drafted before the fixture test; the hand-computed fixture (`internal/sqlitestore/periodization_review_test.go`: two routines, one microcycle with +1 set and +0.5 RPE closed by hand with a routine skipped, an imputed RPE, a warm-up, extra rows, an off-plan session, feedback and bodyweight) then passed, and comparing imputed RPE as observed made two of its three tests fail, so they guard it. The tool list, annotation, count 72 and isolation case failed before the tool was registered, then passed. `gofmt -l .` empty, `go vet ./...` clean, `go test ./internal/... ./cmd/...` 1479 passed.
- L4 (2026-11-25): main moved (#146 phone view of blocks, equipment history); merged `origin/main` (`2d95c46e`), one conflict in the `session_progress` row of `docs/tools.md` resolved as the union, tool count 72. `gofmt -l .` empty, `go vet ./...` clean, `go test ./internal/... ./cmd/...` 1507 passed.
