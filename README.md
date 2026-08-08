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

`start_session`, `add_set`, `update_set`, `delete_set`, `get_session`, and `list_sessions` cover logging. Exercises are trimmed/lowercased, positions remain dense, and totals are recalculated from stored SI values.

`delete_session` removes a session and every set in it, reporting how many were destroyed. `exercise_history` returns one exercise's sets newest first with each set's estimated 1RM plus its all-time best, so progression can be judged in one call instead of reading every session. `weekly_volume` buckets SI per muscle group by training week.

`set_exercise_group`, `list_exercise_groups`, and `volume_by_muscle` add per-muscle-group volume. Each exercise maps to exactly one muscle group, so group SI is a true partition of session SI — every set counts once and the group totals add up to the session total. Sets whose exercise has no mapping are reported under an empty group rather than dropped, so gaps in the catalogue stay visible.

## Web UI (PWA)

Setting `WEB_BASE_PATH` mounts an installable PWA for logging sets from a phone
without going through ChatGPT. It is a driving adapter over the same
`training.Service`, so validation, exercise normalization and SI calculation are
shared with the MCP tools and cannot drift apart.

- Served only under the configured secret prefix; unset means not served at all.
- Sets are grouped by exercise, the way a workout is performed and read back.
- The entry form shows what was done last time for that exercise and the
  standing estimated-1RM record; a set matching the record is badged `PR`.
- Quick-pick chips restore the last weight, reps and RPE per exercise.
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

This MVP excludes analytics, recommendations, templates, planned workouts,
multi-user identity/RBAC, REST/GraphQL, OAuth/JWT/token rotation, audit history,
cloud sync/backups, concurrent-writer controls, and formula changes.

The original boundary also excluded frontend/mobile clients. That exclusion was
deliberately lifted to add the PWA above; everything else still stands.

## Documentation

- [ChatGPT setup](docs/chatgpt-setup.md)
- [Connection paths and tunnel safety](docs/connection-paths.md)
- [SI formula](docs/si-formula.md)

The external Secure MCP Tunnel smoke flow is **unverified in this repository**. Use the official provider documentation rather than invented commands.
