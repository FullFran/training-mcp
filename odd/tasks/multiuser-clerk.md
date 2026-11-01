# Multi-user training with Clerk

Objective: invited users each get private training data, through ChatGPT and a web app at a normal public URL, with one identity provider.
Branch: `feat/multiuser-clerk`, on top of `docs/modernization-oauth-evidence`. Delivery: `ask-on-risk`, chain strategy `stacked-to-main`. Test runner: `go test ./...`; scripts through `scripts/*_test.go` with fake tools.

## Specs

- S1: Multi-user with Clerk. User: "vamos con clerk! unifica todo y ya que estas, la hacemos multiusuario". Clerk is the one identity for ChatGPT and the web.
- S2: Access. User: "venga, solo entra gente a la que yo invite". Sign-up is invite-only.
- S3: Public web. User: "ponemos la web del dashboard desde una url publica normal?". The web is served at `https://training.fullfran.com` behind a login, not under a secret prefix.
- S4: Retire secrets. User: "7 esto lo hacemos ya?", where 7 is removing the secret route and rotating the URL and `MCP_BEARER_TOKEN`.
- S5: Isolation. No user can read, change, or infer another user's data through any tool or page. Proposed and not objected to: a store bound to a user, no framework; user: "entiendo, entonces pocketbase no?".
- S6: The owner's existing data ends up in the owner's account, with no loss.
- S7: Backups keep working through all of this, and a failed off-site backup reaches the owner. Assumption: proposed as a prerequisite of S4, and covered by "unifica todo".
- S8: Clerk production instance. Assumption: proposed, not objected to. Any cost is announced before it is incurred.

Assumptions from the proposal, not stated by the user: all personal data is per user, including exercise groups and notes; `export.db` leaves the web and becomes a backup-only route.

## Tasks

- [x] T1 (S7) inline: alert when the newest off-site copy is stale, from GitHub Actions. Risk: item 5. PR #14. The failure email itself is unobserved.
- [~] T2 (S1, S5, S6) inline: migration adding owners (this slice), then user-bound store, then identity from token to tools, two-user tests. Risk: item 1, item 2.
- [ ] T3 (S2, S8) inline: Clerk production instance, invite-only, reconnect ChatGPT. Risk: item 2, item 5.
- [ ] T4 (S1, S3, S5) inline: web login, cookie session, CSRF, per-user offline cache, root mount. Risk: item 2.
- [ ] T5 (S7) inline: backup credential for the snapshot, independent of the web path. Risk: item 2, item 5.
- [ ] T6 (S4) inline: remove proxy injection and secret routes, rotate secrets. Risk: item 2, item 5.

Route: inline; the units depend on each other, so no writer trigger holds.

## Log

- L1 (2026-11-01, user, verbatim): "5 funciona bien hoy tambien sin pedirme nada. 6, ha funcionado registrar todo. 7 esto lo hacemos ya? y ponemos la web del dashboard desde una url publica normal?"
- L2 (2026-11-01, user, verbatim): "vamos con clerk! unifica todo y ya que estas, la hacemos multiusuario"
- L3 (2026-11-01, user, verbatim): "no se puede hacer con un framework sql? como migrar a poketbase", then "entiendo, entonces pocketbase no?"
- L4 (2026-11-01, user, verbatim): "venga, solo entra gente a la que yo invite"
- L5: Code map at 5c3f8e8: 11 tables with no owner; four primary keys assume one user (`exercise_groups`, `exercise_notes`, `volume_landmarks`, `idempotency_keys`); 77 data SQL sites; 20 store methods keyed by client id; 19 of 35 tools take an id. The service worker caches pages by URL alone. No cookies or CSRF exist. The proxy config is outside the repo.
- L6: The signed listing of the bucket from the laptop returned HTTP 200, so the laptop reaches R2. That makes a laptop watchdog possible and disproves the second-hand claim in the other feature's L20.
- L7: T1 evidence: RED on a stub (6 failures), then `go test ./scripts` 33 passed and the full suite exit 0. Real run under an empty environment against the real bucket: exit 0, silent. Forced failure against a bucket the token cannot read: HTTP 403, exit 22, "could not read the bucket"; whether the desktop popup appeared is unconfirmed. Laptop crontab diff: one added line. `.credentials/r2.env.pending` renamed to `r2-watch.env`. Verifier for item 5 is deferred to the feature's end.
- L8 (2026-11-01, user, verbatim): "no me dejes automatizaciones en le ordenador mio vale? y si, stacked to main"
- L9: Removed both training entries from the laptop crontab, the backup and the watchdog; it now has 0 lines, and the existing local copies stay. So L7's install is undone and the laptop no longer makes copies; the VPS and R2 hold the running backups. The watchdog moved to a scheduled GitHub workflow using `scripts/check-offsite.sh` with a read-only token; its failure email is the alert. Writing repository secrets was refused by the permission system, so the owner runs the setup script. `go test ./scripts`: 32 passed. The workflow cannot run until it is on main.
- L10 (2026-11-01): Setup script run at the owner's request. Token `training-mcp-backups-watch`: signed list 200, signed write 403. The four repository secrets are set, per `gh secret list`.
- L11 (2026-11-01, user, verbatim): "y si! sube rama y pr abierta y mergeada sigue asi con todas"
- L12: PR #14 merged by rebase. The workflow ran on main against the real bucket: success, `newest copy: training-2026-11-01.db.gpg`. Discovery: Dokploy has `autoDeploy` on `main`, and that merge started a production deployment, so merging a migration applies it to real data at once.
- L13: Migration 013 rehearsed on a fresh production snapshot in scratch: sessions 104, sets 2142, SI 2348.4, session items 3, plans 4, plan items 20, notes 1, groups 72, landmarks 1, keys 10, identical before and after; server booted and served list_sessions; 1 user, 0 rows owned by anyone else; integrity ok, foreign key check empty. Rollback note: an older binary fails on the rebuilt keys, since its `ON CONFLICT` targets no longer match, so rolling back means restoring a pre-migration backup or fixing forward.
- L14: Independent verifier on PR #15, no blockers: all 10 data tables hash-identical after migrating a real backup; 4 older backups also migrate; failures at 4 points leave the file byte-identical; a kill mid-migration recovers; 384 read calls and all write tools but `swap_session_exercise` (unverified, does not touch a rebuilt table) match main. Corrects L13's rollback note: a pre-013 binary boots and reports healthy, while `log_sets`, `set_exercise_note`, `set_exercise_group` and `set_volume_landmarks` fail with an internal tool error. Next slice must scope `WHERE key=?`, `DELETE ... WHERE exercise=?` and the `exercise_groups` joins.
- L15 (2026-11-01, user, verbatim, answering whether to merge #15 knowing it deploys): "hazlo!"
- L16: Fresh backup on the VPS before merging, restore-checked. PR #15 merged at 19:11 UTC and deployed. Production afterwards: version 13, sessions 104, sets 2142, SI 2348.4, plans 4, plan items 20, notes 1, groups 72, landmarks 1, keys 10, all as before; 1 user, 0 sessions owned by anyone else, integrity ok, foreign key check empty, health 200, a real `list_sessions` over MCP works. A production write through the rebuilt keys is unobserved.
- L17: Slice "user-bound store" written by a delegated writer (context backstop). `training.Store.ForUser` returns a `UserStore` with 36 data methods; `training.ServiceFor` is the one seam, answering user 1 from `main.go`. RED: 36 store subtests after the split, then GREEN. A mutation forcing `ForUser` to user 1 failed 51 tool and web subtests. Differential against main on a real backup: 394 reads, 31 writes through 22 tools, 199 web requests, tool schemas and persisted rows, 0 differences. Parent reran `go test ./...`, `go vet`, `gofmt`. Deliberate change: `POST /plan` with a nonexistent plan keeps the day's empty session. Size: +487/-371 plus 1246 lines of new tests, over the 400 budget; one behavior that does not split cohesively. Open: `export.db` is global and must leave the web before a second web user (T4, T5); `ForUser` does not check that the user exists, so the next slice's resolver must return real users only; a new user has no exercise groups, which is an open product question.
