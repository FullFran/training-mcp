# Coach as a ChatGPT-style GUI, with sets and timer

Objective: the coach home looks and works like ChatGPT in training-mcp's colours (conversations, model and thinking pickers, streaming, stop), with set logging and the rest timer always at hand.
Branches: one per slice from `main`, Orca worktree, coordinator merges. Delivery: one PR per slice, rebased on `main`. Test runner: `go test ./...` plus headless Chromium flows at 390x844.

## Specs

- S1: ChatGPT GUI. Owner (2026-11-11, verbatim): «hay que dejarlo que sea como chat gpt + añadir series + cronometro lo que es la gui. que haya historial de chats y esas cosas y que se pueda cambiar modelo y thinking y demas igual que en chat gpt! con la misma interfaz pero con nuestro color!!!»
- S2: Engine protocol. Owner decision (2026-11-11): ACP later; extend the current protocol.
- S3: Model and thinking picker; the model must stay within the person's own OpenAI login.
- S4: Persisted conversations with list, new, rename, delete, per-chat delete and «borrar todo»; photos never stored. Retention, owner (2026-11-11, verbatim): «yo diria 90 dias... porque en la practica deja de importar y lo que importa es la memoria de entrenamiento». Conversations are auto-deleted 90 days after their last message (deterministic server-side purge, tested with a fake clock); training data (sessions, sets, plans) is never touched by the purge.
- S5: Fix the UI bugs seen in phase 1: the one-tap button offered «20 kg × 7 @8» after sets of 80 kg × 8 logged through MCP; the «Récord 1RM est.» bar is unreadable in the light scheme.

Design and screenshots (phase 1): `~/.cache/d1-coach-acp-design/design.md`, `shots/current/`, `shots/proposed/`.

## Tasks

- [x] T1 (S1) inline: ChatGPT shell, client only. Top bar and drawer (panels, account, settings), pill composer with send/stop, workout bar above the composer with the entry form as a bottom sheet, one steps row before each answer in plain words, live line, error and stop cards with retry, scroll-follow, empty state with deterministic suggestions, set card before the reaction it causes. Risk: none.
- [x] T2 (S2, S3) inline: protocol `models`, `set`, `thinking`; routes and per-person preference; picker sheet. Risk: item 2. Independent verifier.
- [ ] T3 (S1, S4) inline: conversation store, migration, routes, engine history rebuild, drawer list, 90-day purge. Risk: items 1 and 2. Independent verifier.
- [ ] T4 (S5) inline: the two UI bugs. Risk: none.

Route evidence: one unit at a time, each depends on the previous one's markup; no writer trigger.

## Log

- L1 (2026-11-11, owner, verbatim): «oye, podemos conectar por acp todo paa que se vea muy bien igual que [a private harness] no? ahora esta rarete... el diseño es poco intuitivo y usable inspirate en como alvaro hace las cosas»
- L2 (2026-11-11, owner, verbatim): «le falta mucho esto xD hay que dejarlo que sea como chat gpt + añadir series + cronometro lo que es la gui. que haya historial de chats y esas cosas y que se pueda cambiar modelo y thinking y demas igual que en chat gpt! con la misma interfaz pero con nuestro color!!!»
- L3 (2026-11-11, coordinator relaying the owner): ACP later; chat history auto-deleted after N days; S1 → S2 → S3 → S4, one PR each; verifier on S2 and S3.
- L4 (2026-11-11, owner, verbatim): «yo diria 90 dias... porque en la practica deja de importar y lo que importa es la memoria de entrenamiento». A separate coach memory is a later feature.
- L5: Phase 1 findings: tool steps render below the answer; the reaction lands above the set card that caused it; header plus workout card take about 37% of the screen; settings crowd the composer; panel pages show a second header.
- L6: T1 evidence. RED: `TestHomeIsAChatGPTStyleShell` failed on the base template («top bar has no menu button»), passes after. `go test ./...`: 657 passed. Headless Chromium flow at 390x844 against a scratch server with a Node fake engine: 12 of 12 checks pass (empty state with server suggestions, send turns into stop, steps row above the answer in words, 4 steps folded into one row, failure card with retry, stopped card, entry sheet closes after logging, set card before the reaction it caused, drawer closes when a panel opens, jump button after scrolling up), no page errors; dark and light screenshots. Not verified: a real phone; real model streaming. Seen again through the UI: after an 80 kg × 8 set the one-tap button offers «20 kg × 7 @8» (T4).
- L7: T2 evidence. RED: `internal/engine/models_test.go` did not build without `Models`/`SetPreferences`; `TestCoachModelsAndSettings`, the isolation and header tests failed on the new routes; `TestCoachPreferencesAreUnsetUntilChosenAndPerUser` did not build; the engine's `pickModel`/`thinkingLevels` tests failed to import; `TestHomeHasTheModelPicker` failed on the S1 template. GREEN: `go test ./...` all 9 packages, `node --test engine/protocol.test.ts` 7 passed; the real engine starts with Pi 1.5.2 (`ready`). Pi 1.5.2's built-in openai-codex catalog (read offline) has models without `off` (gpt-6-astra, gpt-6.1-sol), so levels are per model. Headless at 390x844: 8 of 8 (no model request on page load, title shows the model, levels follow the model, chip shows the level, the turn carries the choice, reasoning closes to «Pensó N s», choice kept after reload), no page errors. Not verified: a real account's model list and a real reasoning stream.
- L8: T2 independent verifier: S2 pass; risks 2-6 pass (isolation, fixed codes, input validation, concurrency under -race with 20 goroutines, unchanged prompts). One blocker: the in-memory model list survived Disconnect, so a later login to another account could choose from the old list. Fixed: the list is dropped on Disconnect and on a new login, and `Models` refuses a person with no login before serving it; pinned by `TestDisconnectForgetsTheModelList` (RED before, GREEN after). Recheck: the verifier's four probes pass on the fix; `go test -race ./internal/engine/` ok; `go test ./...` 9 packages ok. Advisories kept: a model request can evict another person's pending login at capacity, as a turn already could.
