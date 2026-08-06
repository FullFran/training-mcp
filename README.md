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

The unauthenticated `GET /health` endpoint returns `{"status":"ok"}`. MCP is only available at `/mcp` for GET, POST, and DELETE. Public ChatGPT access requires HTTPS at the TLS termination boundary.

## Tools

`start_session`, `add_set`, `update_set`, `delete_set`, `get_session`, and `list_sessions` are the complete MVP surface. Exercises are trimmed/lowercased, positions remain dense, and totals are recalculated from stored SI values.

## Scope boundary

This MVP excludes analytics, recommendations, templates, planned workouts, multi-user identity/RBAC, frontend/mobile clients, REST/GraphQL, OAuth/JWT/token rotation, audit history, cloud sync/backups, concurrent-writer controls, and formula changes.

## Documentation

- [ChatGPT setup](docs/chatgpt-setup.md)
- [Connection paths and tunnel safety](docs/connection-paths.md)
- [SI formula](docs/si-formula.md)

The external Secure MCP Tunnel smoke flow is **unverified in this repository**. Use the official provider documentation rather than invented commands.
