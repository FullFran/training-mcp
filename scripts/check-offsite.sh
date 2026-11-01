#!/usr/bin/env bash
# Raises a desktop notification when the newest copy in R2 is older than
# yesterday, or when the bucket cannot be read. The off-site backup runs on a VPS
# where a failure only reaches a log nobody reads, and a dead VPS would not report
# anything at all, so this checks the result from another machine.
#
# Reads .credentials/r2-watch.env, not r2.env: on this machine r2.env would make
# backup.sh upload too.
set -euo pipefail

# Cron's PATH has neither of the locations curl is installed in.
PATH="$HOME/.nix-profile/bin:/home/linuxbrew/.linuxbrew/bin:$PATH"

reason="the check itself failed"
on_exit() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    echo "$(date '+%F %T') off-site check failed (exit $status): $reason" >&2
    # Best effort, as in backup.sh.
    DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/run/user/$(id -u)/bus}" \
      notify-send -u critical "Training off-site backup" "$reason" 2>/dev/null || true
  fi
}
trap on_exit EXIT

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "$root/scripts/r2-env.sh"

reason="missing $root/.credentials/r2-watch.env"
load_r2_env "$root/.credentials/r2-watch.env"
require_r2_env

reason="could not read the bucket"
listing="$(printf 'user = "%s:%s"\n' "$R2_ACCESS_KEY_ID" "$R2_SECRET_ACCESS_KEY" |
  curl -fsS --max-time 60 --aws-sigv4 aws:amz:auto:s3 -K - \
    "${R2_ENDPOINT%/}/$R2_BUCKET/?list-type=2&prefix=training-")"

# Names carry the date, so the last in sort order is the newest.
newest="$(printf '%s' "$listing" | { grep -o '<Key>[^<]*</Key>' || true; } | sed 's/<[^>]*>//g' | sort | tail -n 1)"
if [ -z "$newest" ]; then
  reason="no copies in the bucket"
  exit 1
fi

date="${newest#training-}"
date="${date%%.*}"
# Yesterday still counts, as the day's copy only appears after midnight's first run.
if [[ "$date" < "$(date -d yesterday +%F)" ]]; then
  reason="newest copy is from $date"
  exit 1
fi
