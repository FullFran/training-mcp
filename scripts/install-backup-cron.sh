#!/usr/bin/env bash
# Installs a daily cron entry that runs the backup and logs the result.
#
# A backup you must remember to take is not a backup, which is the whole point
# of this script existing.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
log="$HOME/.local/state/training-mcp/backup.log"
mkdir -p "$(dirname "$log")"

entry="17 7 * * * $root/scripts/backup.sh >>$log 2>&1"

# Replace any previous entry for this script rather than stacking duplicates.
current="$(crontab -l 2>/dev/null | grep -v "training-mcp/scripts/backup.sh" || true)"
printf '%s\n%s\n' "$current" "$entry" | grep -v '^$' | crontab -

echo "installed:"
crontab -l | grep backup.sh
echo "log: $log"
