#!/usr/bin/env bash
# Proves an off-site backup can be restored, without touching production: fetches
# the newest object from R2, decrypts it, verifies it, boots the server against
# the copy, and reads it back through MCP. Everything happens in a temp dir that
# is removed afterwards.
#
# Usage: restore-check.sh [--no-boot] [encrypted-file]
# With a file it skips R2 and only needs BACKUP_PASSPHRASE. Exit 0 means restorable.
set -euo pipefail

PATH="$HOME/.nix-profile/bin:/home/linuxbrew/.linuxbrew/bin:$PATH"

boot=true
if [ "${1:-}" = "--no-boot" ]; then
  boot=false
  shift
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "$root/scripts/r2-env.sh"
load_r2_env "$root/.credentials/r2.env"
: "${BACKUP_PASSPHRASE:?missing in r2.env}"

work="$(mktemp -d)"
server=""
cleanup() {
  [ -z "$server" ] || kill "$server" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

r2() {
  printf 'user = "%s:%s"\n' "$R2_ACCESS_KEY_ID" "$R2_SECRET_ACCESS_KEY" |
    curl -fsS --max-time 300 --aws-sigv4 aws:amz:auto:s3 -K - "$@"
}

if [ $# -gt 0 ]; then
  cp "$1" "$work/copy.gpg"
else
  require_r2_env
  base="${R2_ENDPOINT%/}/$R2_BUCKET"
  listing="$(r2 "$base/?list-type=2&prefix=training-")"
  # Names carry the date, so the last in sort order is the newest. Finding none
  # is not an error yet: the check below says what it means.
  key="$(printf '%s' "$listing" | { grep -o '<Key>[^<]*</Key>' || true; } | sed 's/<[^>]*>//g' | sort | tail -n 1)"
  [ -n "$key" ] || { echo "no backups found in the bucket" >&2; exit 1; }
  r2 -o "$work/copy.gpg" "$base/$key"
  echo "fetched $key"
fi

gpg --batch --yes --pinentry-mode loopback --passphrase-fd 3 --decrypt \
  --output "$work/training.db" "$work/copy.gpg" 3<<<"$BACKUP_PASSPHRASE" 2>/dev/null ||
  { echo "could not decrypt: wrong BACKUP_PASSPHRASE or a damaged file" >&2; exit 1; }

head -c 15 "$work/training.db" | grep -q 'SQLite format 3' || { echo "not a SQLite file" >&2; exit 1; }
sqlite3 "$work/training.db" 'PRAGMA integrity_check;' | grep -qx ok || { echo "integrity check failed" >&2; exit 1; }

counts() {
  printf '%s sesiones, %s series' \
    "$(sqlite3 "$work/training.db" 'SELECT COUNT(*) FROM sessions;')" \
    "$(sqlite3 "$work/training.db" 'SELECT COUNT(*) FROM sets;')"
}
before="$(counts)"
echo "$before"

if [ "$boot" = true ]; then
  (cd "$root" && go build -o "$work/server" ./cmd/training-mcp)
  port=$((18000 + RANDOM % 1000))
  # A loopback-only server on a throwaway copy is the one place bypassing auth is safe.
  TRAINING_DB_PATH="$work/training.db" MCP_AUTH_DISABLED=1 \
    "$work/server" --addr "127.0.0.1:$port" >"$work/server.log" 2>&1 &
  server=$!
  for _ in $(seq 1 50); do
    curl -fsS --max-time 1 "http://127.0.0.1:$port/health" >/dev/null 2>&1 && break
    sleep 0.2
  done
  curl -fsS --max-time 10 -X POST "http://127.0.0.1:$port/mcp" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_sessions","arguments":{"limit":1}}}' |
    grep -q '"sessions"' || { echo "the server did not serve the restored copy" >&2; cat "$work/server.log" >&2; exit 1; }
  kill "$server"
  wait "$server" 2>/dev/null || true
  server=""
  after="$(counts)"
  [ "$after" = "$before" ] || { echo "booting changed the data: $after" >&2; exit 1; }
  echo "the server boots on the copy and serves it through MCP"
fi

echo "restorable"
