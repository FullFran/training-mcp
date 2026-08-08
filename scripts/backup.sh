#!/usr/bin/env bash
# Downloads a consistent snapshot of the training database and verifies it.
#
# Restoring: copy the .db back into the container's /data volume as
# training.db while the app is stopped, then start it again.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "$root/.credentials/dokploy.env"

dest="${1:-$HOME/backups/training-mcp}"
mkdir -p "$dest"
out="$dest/training-$(date +%Y-%m-%d).db"

curl -fsS -o "$out" "${WEB_URL%/}/export.db"

# A download that is not a readable database is not a backup, so check.
head -c 15 "$out" | grep -q 'SQLite format 3' || { echo "not a SQLite file" >&2; exit 1; }
sqlite3 "$out" 'PRAGMA integrity_check;' | grep -qx ok || { echo "integrity check failed" >&2; exit 1; }

printf '%s  %s sesiones, %s series\n' "$out" \
  "$(sqlite3 "$out" 'SELECT COUNT(*) FROM sessions;')" \
  "$(sqlite3 "$out" 'SELECT COUNT(*) FROM sets;')"

# Keep the last 30 daily copies.
ls -1t "$dest"/training-*.db | tail -n +31 | xargs -r rm --
