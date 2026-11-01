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

- [~] T1 (S7) inline: alert when the newest off-site copy is stale, from GitHub Actions. Risk: item 5. Pending: repository secrets, and a real run once the workflow is on main.
- [ ] T2 (S1, S5, S6) inline: migration adding owners, user-bound store, identity from token to tools, two-user tests. Risk: item 1, item 2.
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
