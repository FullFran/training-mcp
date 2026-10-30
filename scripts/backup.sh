#!/usr/bin/env bash
# Downloads a consistent snapshot of the training database and verifies it.
#
# Usage: backup.sh [--daily] [destination]
# --daily does nothing once today's copy exists, so cron can run it hourly and
# still catch a day on which the machine was off at any single fixed time.
#
# Restoring: copy the .db back into the container's /data volume as
# training.db while the app is stopped, then start it again.
set -euo pipefail

# Cron's PATH has neither of the locations curl and sqlite3 are installed in.
PATH="$HOME/.nix-profile/bin:/home/linuxbrew/.linuxbrew/bin:$PATH"

tmp=""
on_exit() {
  local status=$?
  [ -z "$tmp" ] || rm -f "$tmp"
  if [ "$status" -ne 0 ]; then
    echo "$(date '+%F %T') backup failed (exit $status)" >&2
    # Best effort: nobody reads a log, so a failure has to interrupt someone.
    DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/run/user/$(id -u)/bus}" \
      notify-send -u critical "Training backup failed" "See ~/.local/state/training-mcp/backup.log" 2>/dev/null || true
  fi
}
trap on_exit EXIT

daily=false
if [ "${1:-}" = "--daily" ]; then
  daily=true
  shift
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "$root/.credentials/dokploy.env"

dest="${1:-$HOME/backups/training-mcp}"
mkdir -p "$dest"
out="$dest/training-$(date +%Y-%m-%d).db"

if [ "$daily" = true ] && [ -e "$out" ]; then
  exit 0
fi

# Verify before replacing, so a bad download never costs the copy already here.
tmp="$(mktemp "$dest/.incoming.XXXXXX")"
curl -fsS --max-time 300 -o "$tmp" "${WEB_URL%/}/export.db"

# A download that is not a readable database is not a backup, so check.
head -c 15 "$tmp" | grep -q 'SQLite format 3' || { echo "not a SQLite file" >&2; exit 1; }
sqlite3 "$tmp" 'PRAGMA integrity_check;' | grep -qx ok || { echo "integrity check failed" >&2; exit 1; }

# mktemp already made it private, which suits a copy of personal training data.
mv "$tmp" "$out"
tmp=""

printf '%s  %s sesiones, %s series\n' "$out" \
  "$(sqlite3 "$out" 'SELECT COUNT(*) FROM sessions;')" \
  "$(sqlite3 "$out" 'SELECT COUNT(*) FROM sets;')"

# Keep the last 30 daily copies.
ls -1t "$dest"/training-*.db | tail -n +31 | xargs -r rm --
