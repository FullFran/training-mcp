#!/usr/bin/env bash
# Installs a daily cron entry that runs the backup and logs the result.
#
# A backup you must remember to take is not a backup, which is the whole point
# of this script existing.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
log="$HOME/.local/state/training-mcp/backup.log"
mkdir -p "$(dirname "$log")"

# Hourly, because the machine is not always on at any one time. --daily makes
# every run after the day's first success a no-op.
entry="17 * * * * $root/scripts/backup.sh --daily >>$log 2>&1"
# Silent unless the newest off-site copy is stale.
watchdog="47 * * * * $root/scripts/check-offsite.sh >>$log 2>&1"

# Replace any previous entry for these scripts, from this or another clone, rather
# than stacking duplicates. Other projects may have scripts with the same names.
current="$(crontab -l 2>/dev/null | grep -vF \
  -e "training-mcp/scripts/backup.sh" -e "$root/scripts/backup.sh" \
  -e "training-mcp/scripts/check-offsite.sh" -e "$root/scripts/check-offsite.sh" || true)"
printf '%s\n%s\n%s\n' "$current" "$entry" "$watchdog" | grep -v '^$' | crontab -

echo "installed:"
crontab -l | grep -F -e "$root/scripts/backup.sh" -e "$root/scripts/check-offsite.sh"
echo "log: $log"
