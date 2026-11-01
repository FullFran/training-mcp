#!/usr/bin/env bash
# Fails when the newest copy in R2 is older than yesterday, or when the bucket
# cannot be read. The off-site backup runs on a VPS where a failure only reaches a
# log nobody reads, and a dead VPS would report nothing at all, so a scheduled
# GitHub workflow runs this from elsewhere and its failure email is the alert.
#
# Reads .credentials/r2-watch.env, not r2.env, as the workflow's token is read-only.
set -euo pipefail

reason="the check itself failed"
on_exit() {
  local status=$?
  [ "$status" -eq 0 ] || echo "off-site check failed (exit $status): $reason" >&2
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
# Yesterday still counts: the day's copy only appears after the day's first run.
if [[ "$date" < "$(date -d yesterday +%F)" ]]; then
  reason="newest copy is from $date"
  exit 1
fi
echo "newest copy: $newest"
