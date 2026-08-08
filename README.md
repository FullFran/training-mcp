# Training MCP

Training MCP is a small, single-user Go service that replaces a workout spreadsheet with six conversational MCP tools over Streamable HTTP. It stores sessions and sets in SQLite and preserves the descriptive SI formula `max(0, 0.2 * RPE - 0.6)`.

## Quick path

1. Install Go 1.26 or newer.
2. Set `MCP_BEARER_TOKEN` and run `go run ./cmd/training-mcp` (default `:8080`).
3. Expose `/mcp` through HTTPS for a public ChatGPT connector; local tunnel-only development is documented in `docs/connection-paths.md`.
4. Verify with `go test ./...` and `go vet ./...`.

## Configuration

| Variable/flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` | HTTP listen address |
| `TRAINING_DB_PATH` | `~/.local/share/training-mcp/training.db` | SQLite file |
| `MCP_BEARER_TOKEN` | required | Static bearer credential |
| `MCP_AUTH_DISABLED=1` | disabled | Unsafe tunnel-only development bypass; never use in production |
| `WEB_BASE_PATH` | empty (web UI disabled) | Secret prefix the PWA is served from, e.g. `/a1b2c3…` |

The unauthenticated `GET /health` endpoint returns `{"status":"ok"}`. MCP is only available at `/mcp` for GET, POST, and DELETE. Public ChatGPT access requires HTTPS at the TLS termination boundary.

## Tools

A set can carry an optional **intensity technique** (`drop set`, `rest-pause`, `myo-reps`, `sin parar`…). It is a property of the set, not a separate exercise, so the volume still counts toward the right muscle group while progression on the base movement stays comparable — a set with a technique never becomes the exercise's record.

`log_set` is the simplest way to record training: it takes an exercise, weight, reps and RPE, finds or creates today's session, and can record several identical sets at once. It needs no session id, so conversational logging is one call. `start_session`, `add_set`, `update_set`, `delete_set`, `get_session` and `list_sessions` remain for explicit control. Exercises are trimmed/lowercased, positions remain dense, and totals are recalculated from stored SI values.

`create_plan`, `list_plans`, `get_plan` and `delete_plan` manage reusable workout plans: an ordered list of exercises with a target set count and an optional rep range and RPE. Load is deliberately not planned — the prescription is effort and reps, and the weight that meets it is discovered at the gym. `start_session` takes an optional `plan_id`, which **copies** the plan into the session. From then on the session owns its prescription: `add_session_exercise`, `adjust_session_exercise` (sets, rep range, RPE, skip), `swap_session_exercise` and `remove_session_exercise` change today only and never edit the template, while editing a template never rewrites a past session. `session_progress` reports planned versus completed sets per exercise, listing anything done off-plan with `target_sets` 0 so it stays visible. `save_session_as_plan` promotes an adjusted session into a new reusable plan.

`delete_session` removes a session and every set in it, reporting how many were destroyed. `exercise_history` returns one exercise's sets newest first with each set's estimated 1RM plus its all-time best, so progression can be judged in one call instead of reading every session. `weekly_volume` buckets SI per muscle group by training week.

`record_feedback` and `volume_recommendation` close the loop the source spreadsheet automated: rate a trained muscle group 0-3 on fatigue, pump and recovery, and get next week's set-count change. The mapping from the 0-9 magnitude is in `training.RecommendSets`; its two anchors come from the sheet, the thresholds between them are an interpolation.

`set_exercise_group`, `list_exercise_groups`, and `volume_by_muscle` add per-muscle-group volume. Each exercise maps to exactly one muscle group, so group SI is a true partition of session SI — every set counts once and the group totals add up to the session total. Sets whose exercise has no mapping are reported under an empty group rather than dropped, so gaps in the catalogue stay visible.

## Web UI (PWA)

Setting `WEB_BASE_PATH` mounts an installable PWA for logging sets from a phone
without going through ChatGPT. It is a driving adapter over the same
`training.Service`, so validation, exercise normalization and SI calculation are
shared with the MCP tools and cannot drift apart.

- Served only under the configured secret prefix; unset means not served at all.
- A plan can be started for the day; its exercises become a checklist showing
  done/target sets, and tapping one loads its prescription into the form.
- The day's plan is fully adjustable mid-session: add or drop an exercise,
  change its set count, skip it, or substitute it when a machine is taken.
  Sets already logged are never touched by any of this — an exercise removed
  from the plan simply reappears as off-plan work.
- Sets are grouped by exercise, the way a workout is performed and read back.
- The entry form shows what was done last time for that exercise and the
  standing estimated-1RM record; a set matching the record is badged `PR`.
- The exercise field autocompletes from every exercise already known, which is
  what stops the catalogue fragmenting into near-duplicate names.
- Quick-pick chips restore the last weight, reps and RPE per exercise.
- Session feedback asks only about the muscle groups actually trained that day.
- A rest timer starts on every logged set and remembers the adjusted duration.
- Sets can be edited in place instead of deleted and re-entered.
- Today's session is created lazily on the first set, so opening the app never
  leaves an empty session behind.
- The service worker caches the shell and the last viewed pages for offline
  *reading*. Writes always require the network — a set only counts once stored.

**Authentication is currently the secret URL itself.** The reverse proxy injects
the bearer token, so anyone holding the URL has full access. This is tracked
debt, not a design: see the open issues.

## Scope boundary

This MVP excludes recommendations, multi-user identity/RBAC, REST/GraphQL,
OAuth/JWT/token rotation, audit history, cloud sync/backups, concurrent-writer
controls, and formula changes.

Three exclusions were deliberately lifted as the tool proved useful:
frontend/mobile clients (the PWA), analytics (per-muscle volume and exercise
progression), and planned workouts (plans). Everything else still stands.

Plans record prescriptions you decide. The volume-landmark feedback loop is
implemented as an advisory: it reports a set-count change per muscle group, but
never edits a plan on its own.

## Documentation

- [ChatGPT setup](docs/chatgpt-setup.md)
- [Connection paths and tunnel safety](docs/connection-paths.md)
- [SI formula](docs/si-formula.md)

The external Secure MCP Tunnel smoke flow is **unverified in this repository**. Use the official provider documentation rather than invented commands.
