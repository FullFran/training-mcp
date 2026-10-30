# Sourced by the backup scripts. Reads .credentials/r2.env as data rather than
# sourcing it: a value with a space would otherwise run its second word as a
# command, and the shell's error would print part of the secret into the log.
# Values are taken literally, so they need no quotes.
load_r2_env() {
  local line key
  while IFS= read -r line || [ -n "$line" ]; do
    key="${line%%=*}"
    case "$key" in
      R2_ENDPOINT | R2_BUCKET | R2_ACCESS_KEY_ID | R2_SECRET_ACCESS_KEY | BACKUP_PASSPHRASE)
        printf -v "$key" '%s' "${line#*=}"
        ;;
    esac
  done <"$1"
}

require_r2_env() {
  : "${R2_ENDPOINT:?missing in r2.env}" "${R2_BUCKET:?missing in r2.env}"
  : "${R2_ACCESS_KEY_ID:?missing in r2.env}" "${R2_SECRET_ACCESS_KEY:?missing in r2.env}"
}
