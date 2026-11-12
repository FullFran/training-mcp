# Live workout app and approval modes

Objective: make training from ChatGPT comfortable: a rest timer that can be stopped (saving the real rest) and extended by 30 s, with the next set's weight and reps editable and logged from the view; and three agent approval modes, the new default accepting everything except deletes.
Branch: `feat/live-workout-app` from `main` (980e5ae6). Delivery: `single-pr` proposed, confirmed before the PR. Test runner: `go test ./...`; `node --test` for ui/.

## Specs

- S1 (Fran, verbatim): "estaria bien que te permita, parar el descanso (por ejemplo si descansas menos en la de calentamiento) o añadir mas descanso (rollo boton de 30s mas) y que guarde cuanto has descansado si le das a parar!"
- S2 (Fran, verbatim): "si! y que se pueda cambiar peso y repeticiones en la interfaz! eso seria muy god vamos a ello!"
- S3 (Fran, verbatim): "quiero ambas cosas tanto yo como el mcp". Stopping the rest and logging/editing sets are available to the person in the view and to the agent through MCP, through the same tools.
- S4 (Fran, verbatim): "hay que poner mas modos... que por defecto se acepte todo salvo borrar y que haya la por defecto de ahora que revisas todo ;-: y la de acept all (que es la que tendré yo xD)". Modes: `review_all` (today's default), `accept_except_delete` (new default), `accept_all`.
- S5 (Fran, verbatim): "pero haz que los diseños estén to guapos vale?"
- S6 (Fran, verbatim): "na entonces por ahora que sea desde la app pero dejalo como issue". The mode is changed only in the PWA; the in-chat button is #125.

Assumptions:
- Existing accounts keep exactly their current behavior: accept-all on stays `accept_all`, everything else becomes an explicit `review_all`. Only accounts created later start in `accept_except_delete`. No permission is loosened without the person choosing it.
- "Borrar" means the action types that delete: `set.delete`, `session.delete`, `plan.delete`, `memory.forget`, `plan.exercise.remove`, `session.exercise.remove`, and `exercise.note.remove` (added after the T1 verifier found it deletes a row; L6).
- The view's buttons call the same agent tools (`stop_rest`, `log_sets`, `update_set`) through the proposal path, so the mode decides; in `review_all` the view shows the write as awaiting approval. No app-only write tool (see #125 for why a host-enforced visibility is not trusted for writes).
- The real rest is stored only when the timer is stopped (#24: never inferred from timestamps), on the set the rest followed, as observed seconds. +30 s extends only the current countdown in the view; it never changes the person's default target.

## Tasks

- [x] T1 (S4, S6) delegated writer (parallel unit, isolated worktree): approval modes: migration 027, service decision, PWA `/review` selector, docs, tests. Risk: item 2 (permission guards), item 1 (migration). Commit 681c6c5e.
- [x] T2 (S1, S3) inline: real rest: migration 028 `sets.rest_after_seconds`, `Service.RunningRest`/`RecordRest`, live state `stopped`, MCP `stop_rest` (proposal `rest.stop`), PWA skip stores the rest (`POST /rest/stop`). Risk: item 1 (migration). Commit a8645c78.
- [x] T3 (S1, S2, S3, S5) inline: rest-timer view v2: stop, +30 s, next set with editable weight and reps logged by "Done", last set editable; pending state; polished design. Commit e38e4963.
- [ ] T4 seam check and PR: full CI, independent verifier on the high-risk items. Local seam check green; T2/T3 verifier running; push/PR need Fran.

Route evidence: T1 and T2+T3 are independent units with disjoint edit surfaces, each several hundred lines, launched together (writer trigger a). T1 owns migration 027 and the proposal/review files; T2 owns 028 and the rest files.

## Log

- L1 (2026-11-12, Fran, verbatim): "maravilloso! pero vamos a editar algunas cosas, estaria bien que te permita, parar el descanso (por ejemplo si descansas menos en la de calentamiento) o añadir mas descanso (rollo boton de 30s mas) y que guarde cuanto has descansado si le das a parar! tenemos que seguir desarollando lo de las apps me encanta!!!!!! que se te ocurre? para que sea muy comodo usarlo desde chat gpt"
- L2 (Fran, verbatim): "si! y que se pueda cambiar peso y repeticiones en la interfaz! eso seria muy god vamos a ello! quiero ambas cosas tanto yo como el mcp y hay que poner mas modos... que por defecto se acepte todo salvo borrar y que haya la por defecto de ahora que revisas todo ;-: y la de acept all (que es la que tendré yo xD)"; "pero haz que los diseños estén to guapos vale?"; "que la app tambien de por mcp para cambiar el modo pulsando botón"; then "na entonces por ahora que sea desde la app pero dejalo como issue" → #125.
- L3: Decision. Widget writes go through the existing agent tools and the mode instead of app-only direct tools: the same behavior for person and agent, no duplicate tools, no trust in host-enforced visibility for writes.
- L4: T1 writer returned partial: all in-surface work green; outside its surfaces `cmd/training-mcp/main_test.go` still posted to the removed `/review/accept-all`, README described the old switch, and `SetApprovalMode` sat behind a type-asserted side interface. Parent fixed all three (`SetApprovalMode` moved into `UserStore` with a two-user isolation case). T1-only index tested in a scratch copy: training, sqlitestore, mcpsrv, websrv, cmd green.
- L5: T2. RED observed for the store (`GetSession` did not read the column) and the web route (404); the training tests were written with the implementation in one step (RED not observed there). Design: `stop_rest` with no arguments resolves set and elapsed seconds at call time (a `resolve` hook in `addWriteBy`), so a proposal accepted later stores what the timer showed; `set_id` + `rest_seconds` record a reported rest; stored once per set (`ErrRestRecorded`, 409 on the web). Undo is covered by the existing every-write-tool undo test. Annotated idempotent and non-destructive.
- L6: T1 independent verifier: S4, S6 met, no blockers; migration 027 checked on a baseline database (accept_all 1/0, no row, later user). Advisory taken: `exercise.note.remove` deletes a row, so it now waits in accept_except_delete (commit 7e4588e0).
- L7: T3. RED: model tests (5 failing) and Go next-set tests (undefined RestTimerNext). View rendered in a fake host built on the SDK's AppBridge and screenshot with Playwright Chromium in a scratch dir: resting, ready (dark), stopped, none (dark), pending, logged, lb edit. Calls checked: Stop sends `stop_rest {set_id, rest_seconds}`, Done sends `log_sets` with the key, warm-up category, Edit sends only changed fields. Fixes found that way: an untouched lb weight would have logged 80.0137 kg for 80 (now keeps the stored kg); headline overflowed the ring in the none state. URI bumped to v2. View copy stays English as in v1.
- L8: Seam check: `gofmt`, `go vet ./...`, `go test ./...` 1262 passed in 12 packages; HCS verify ok; ui check, 13 node tests, bundle fresh.
- L9: T2/T3 independent verifier: S1, S2, S3 met, no blockers; S5 (design) not verifiable by it. Migration 028 on a baseline DB leaves existing sets NULL; pending stop_rest stores the call-time seconds; undo restores NULL; cross-user set refused; differential on 6 tools shows only intended changes. Advisory taken: the view reused the log_sets key after an error even when the values changed, so the key is now tied to the payload. Other advisories (stopped overrides superset/idle for that set; bodyweight sets need weight > 0 as on the server) left as is.
