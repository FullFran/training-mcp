#!/usr/bin/env bash
# Downloads a consistent snapshot of the training database, verifies it, and
# when .credentials/r2.env exists, uploads an encrypted copy to Cloudflare R2.
#
# Usage: backup.sh [--daily] [destination]
# --daily does nothing once today's copies exist, so cron can run it hourly and
# still catch a day on which the machine was off at any single fixed time. It
# also retries a failed upload without downloading again.
#
# Restoring: see "Backups" in the README. scripts/restore-check.sh proves a copy
# can be restored without touching production.
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

offsite=false
if [ -f "$root/.credentials/r2.env" ]; then
  # shellcheck disable=SC1091
  source "$root/.credentials/r2.env"
  : "${R2_ENDPOINT:?missing in r2.env}" "${R2_BUCKET:?missing in r2.env}"
  : "${R2_ACCESS_KEY_ID:?missing in r2.env}" "${R2_SECRET_ACCESS_KEY:?missing in r2.env}"
  : "${BACKUP_PASSPHRASE:?missing in r2.env}"
  offsite=true
fi

dest="${1:-$HOME/backups/training-mcp}"
mkdir -p "$dest"
name="training-$(date +%Y-%m-%d).db"
out="$dest/$name"
marker="$dest/.offsite-last"

download=true
if [ "$daily" = true ] && [ -e "$out" ]; then
  if [ "$offsite" = false ] || [ "$(cat "$marker" 2>/dev/null || true)" = "$name" ]; then
    exit 0
  fi
  download=false
fi

if [ "$download" = true ]; then
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
fi

if [ "$offsite" = false ]; then
  echo "warning: no off-site copy configured, this machine holds the only backup" >&2
  exit 0
fi

# Encrypted here, so the bucket only ever holds ciphertext. Secrets go through
# file descriptors because anything in argv is visible to every local process.
tmp="$(mktemp "$dest/.incoming.XXXXXX")"
gpg --batch --yes --pinentry-mode loopback --passphrase-fd 3 --symmetric \
  --cipher-algo AES256 --output "$tmp" "$out" 3<<<"$BACKUP_PASSPHRASE"
printf 'user = "%s:%s"\n' "$R2_ACCESS_KEY_ID" "$R2_SECRET_ACCESS_KEY" |
  curl -fsS --max-time 300 --aws-sigv4 aws:amz:auto:s3 -K - -T "$tmp" \
    "${R2_ENDPOINT%/}/$R2_BUCKET/$name.gpg"
printf '%s' "$name" >"$marker"
echo "off-site: $name.gpg"
