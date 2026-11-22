# Set equipment

Objective: each set can record, optionally, the machine or equipment it was done on, as free text, so the performance engine stops comparing sets from different machines that share an exercise name.
Branch: `feat/set-equipment` from `main` (80b67fab). Delivery: one PR. Test runner: `go test ./...`. Refs #21, #24.

## Specs

- S1 (owner, verbatim, 2026-11-22): "vamos con ello!" (after the proposal to build the performance engine; #20 and #22 were found already done, the remaining gap being stored equipment identity).
- S2 (owner's choice, verbatim option text, 2026-11-22): "Free text per set (Recommended)": "An optional `equipment` field on each set, e.g. 'technogym pec fly'. ChatGPT fills it from the conversation. Two sets are comparable only if they have the same equipment text. One nullable column and no new tools. Downside: typos split one machine into two (the history would show both values so you can correct them)."

Assumptions: equipment is normalized like exercise names (trimmed, inner spaces collapsed, lower case) and capped in length; an empty value means "not stated" and is stored as NULL; past sets stay NULL, never backfilled; a set with no equipment is comparable only to sets with no equipment.

## Tasks

- [x] T1 (S2) inline: migration 029 `sets.equipment` nullable TEXT; `Set.Equipment`; normalization and validation; store insert, update and every set read. Risk: item 1 (migration). Commit: `fc324379`.
- [x] T2 (S2) inline: MCP `log_set`, `log_sets`, `add_set`, `update_set` accept optional `equipment`; set outputs and `exercise_history` carry it; the PWA history shows it. Risk: none (additive optional field). Commit: `6f91beed`.
- [x] T3 (S2) inline: drop-off excludes a set whose equipment differs from the reference set (`excluded_reason` `different_equipment`); `docs/performance-engine.md` and `docs/tools.md` updated. Risk: none. Commit: `36c09d2f`.

Route evidence: sequential units sharing one field (T2 and T3 depend on T1); no parallel units, no writer trigger; parent works inline. Verification trigger: T1 names item 1, so an independent verifier checks the migration before the PR.

## Log

- L1 (2026-11-22, owner, verbatim): "vamos con ello!"
- L2 (2026-11-22): audit of #20-#24 against code: #20 and #22 done in #82 and closed; #21 and #24 lack stored equipment identity (the engine treats the exercise name as the machine). Owner chose S2.
- L3 (2026-11-22): T1-T3 done test-first; `go vet ./...` clean, `go test ./...` 1284 passed. Independent verifier on the migration (item 1): PASS on all points. Migration 029 applied through `Open()` to a temp DB built at 028 with 50 sets: every row NULL, every other table and column unchanged. The CHECK cannot reject a value the service writes, since Go caps bytes and SQLite counts characters. Every set read carries equipment, undo captures `sets` with `SELECT *`, and imports and lbfix are unaffected.
- L4 (2026-11-22): scope notes: `exercise_history` best set and `suggest_load` still rank all equipment together (not in S2); the phone form does not write equipment, only shows it.
