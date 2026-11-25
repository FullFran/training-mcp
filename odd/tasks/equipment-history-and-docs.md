# Equipment history and docs

Objective: `exercise_history` and `suggest_load` stop ranking every equipment together, and the documents that drifted while the tool surface grew are brought in line with the code, with a test that keeps the tool reference in step with the registered tools.
Branch: `feat/equipment-history-docs` from `main` (55d6fe91). Delivery: one PR. Test runner: `go test ./internal/... ./cmd/...`. Refs #21, #24.

## Specs

- S1 (owner, verbatim, 2026-11-25): "Quiero avanzar lo máximo posible en este proyecto. [...] implementa de forma autonoma todo l oque puedas".
- S2 (issues #21 and #24, owner comment of 2026-11-22, verbatim): "Still open: `exercise_history`'s best set and `suggest_load` rank all equipment together."
- S3 (orchestrator brief, 2026-11-25): `exercise_history` reports the best set per equipment value (the "not stated" group included) and keeps its overall best unchanged; `suggest_load` takes an optional `equipment`, says so when it is used, lists the equipment values seen when none is given and there are several, and says so explicitly when the given equipment has no sets; no existing output changes when `equipment` is omitted.
- S4 (orchestrator brief, 2026-11-25): documents stale against the code (tool counts, migration count, package table, README product summary, `ToolNames()`) are corrected, and a test fails when the tool reference drifts from the registered tools.

Assumptions: a set with no equipment is comparable only to sets with no equipment (the drop-off rule); "comparable" for best and suggestion keeps today's filter (working sets with no technique); the per-equipment best spans the whole history, not the returned page; an empty `equipment` input means omitted, so the "not stated" group cannot be asked for by name; the web app keeps calling the suggestion without equipment, so what it shows is unchanged.

## Tasks

- [x] T1 (S2, S3) inline: `best_by_equipment` in `exercise_history`; `equipment` input and `equipment`, `no_sets_on_equipment`, `equipment_seen` outputs in `suggest_load`; golden test against main's output; `docs/tools.md` and `docs/performance-engine.md`. Risk: none (additive optional input and fields; golden test shows no existing field changes).
- [ ] T2 (S4) inline: tool reference drift test; `ToolNames()`; README, `docs/tools.md` and `docs/architecture.md` counts, package table and product summary. Risk: none (docs and a test).

Route evidence: two independent small units in one PR, as the brief asks; no parallel writer inside this worktree; parent works inline.

## Log

- L1 (2026-11-25, owner, verbatim): "Quiero avanzar lo máximo posible en este proyecto. [...] implementa de forma autonoma todo l oque puedas".
- L2 (2026-11-25): code read: the overall best is one SQL query over working straight sets of every equipment; `suggest_load` advises from the newest working straight set in the last 100, any equipment. The web app's entry form shows the overall record and calls the suggestion without equipment; its exercise screen lists the top sets of every equipment. Neither is changed.
- L3 (2026-11-25): T1 test-first: the new tests failed on the stubbed fields (no groups, no equipment filter, no length check) and the golden test captured main's output on the same fixture before any change.
