# Vibe training MVP sprint

Objective: turn training-mcp into a vibe training harness: a personal-trainer companion in the mobile PWA whose agent sees the live workout, talks with the person, and handles everything through proposals the person can accept, remember, or auto-accept.
Branches: one per unit from `main`, each in its own Orca worktree; coordinator merges. Delivery: `auto-chain`, chain strategy `stacked-to-main`. Test runner: `go test ./...`.

## Specs

- S1: Live companion. User (2026-11-11): "ahora lo que quiero es eso que sea entrenador personal que es tu compañero cuando entrenas: que puedas subir las series y ver los descansos facil mientras entrenas y ese contexto le llegue al agente y puedas hablar con el, te de feedback... y que el agente pueda manejar todo".
- S2: Mobile PWA parity. User: "al principio le damos paridad con training mcp pero en la pwa adaptada para movil (ya sin enlace secreto y con login con clerk el mismo que training mcp ahora". Issue #39.
- S3: Proposals and permissions. User: "para el de training debemos poder elegir el nivel de permisos xD para dejarle todos los permisos si queremos que no pregunte :v igual que el haga-harness"; "no hace falta type name que sea solo confirmar"; "y que se pueda poner para que lo aceptes siempre :v vomo el --dangerously-skip-permissions de claude". Agent actions to propose: "todas!". Issues #34, #35, #36, #38.
- S4: In-app coach. User: "ahora estoy haciendolo con A pero la app es justo para pasar a B lo ideal seria usar el sdk de pi!"; "que cada uno traiga su modelo! con el login de dispositivo de chat gpt como hace re-write!". Issue #48.
- S5: Scope rule. User: "pero vamos haciendo solo lo que necesitamos vale? priorizando el producto". Deterministic work is done by tools: "todo lo determinista que se haga determinista sin gastar tokens! pero que el gaente lo pueda usar y sea el centro de la experiencia" (#47, after the MVP).
- S6: Multi-user stays intact: no user can read, change or infer another user's data (S5 of `odd/tasks/multiuser-clerk.md`).

Assumptions (stated, not objected to): actions a person takes directly in the PWA apply at once without a proposal; one confirmation tier; approval memory keys on action type, never a whole tool.

## Tasks

- [ ] U1 (S3, S6) delegated, Orca worker: proposals core, per-action once/session/always, accept-all switch, /review page, MCP write tools report pending/accepted. Risk: item 1, item 2, item 3.
- [ ] U2 (S1, S2, S6) delegated, Orca worker: live workout context for the agent (current session, last set, rest timing) through MCP, workout screen polish on mobile, in-session parity. Risk: item 3 if existing tool outputs change.
- [ ] U3 (S4, S6) delegated, Orca worker: Pi engine with ChatGPT device login, chat panel in the PWA, live workout events to the engine. Risk: item 2, item 5.
- [ ] U4 (S6) delegated, Orca worker: fix flaky stale-id test (#49). Risk: none.
- [ ] Held for the owner: T3 (Clerk production instance) and the Clerk OAuth application for the web login; T6 secret removal waits for the web login to be live. #37 undo, #41, #42 after the MVP.

Route evidence: U1-U3 are independent units of behavior, each several files and well over 100 lines, launched together (Writer trigger a). Shared files (`internal/mcpsrv/server.go`, migrations) are resolved by merge order U1 first, others rebase.

## Log

- L1 (2026-11-11, user, verbatim): "Se me ha ocurrido convertir training mcp en un harness de vibe entrenamiento, siguiendo hsc como re;write pero aprobechando el sdk de hsc si nos ayuda a hacerlo más rapido".
- L2 (2026-11-11, user, verbatim): "hazlo! y ve mergeando todo, manda workers con opus en orca y vamos a hacer sprint del mvp de la app tu coordina todo".
- L3: Verified before dispatch: the PWA already has a client-side rest timer (`internal/websrv/static/app.js:224`, default 120 s in localStorage), so the agent cannot see rests today. Latest migration is `013_owners.sql`. T4 web login is PR #50; it stays off until `WebClientID` is set. Merging to `main` deploys within about 2 minutes.
