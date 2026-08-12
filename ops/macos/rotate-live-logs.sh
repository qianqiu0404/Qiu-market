#!/usr/bin/env bash
set -euo pipefail
umask 077

runtime_root="${QIU_MARKET_LIVE_RUNTIME_ROOT:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
log_dir="$runtime_root/logs"
state_dir="$runtime_root/run/live-log-rotation"
maximum_bytes="${QIU_MARKET_LOG_MAX_BYTES:-52428800}"
keep_bytes="${QIU_MARKET_LOG_KEEP_BYTES:-10485760}"
archive_generations="${QIU_MARKET_LOG_ARCHIVE_GENERATIONS:-2}"

require_positive_integer() {
  local name="$1"
  local value="$2"
  case "$value" in
    ''|*[!0-9]*)
      echo "$name must be a positive integer." >&2
      return 2
      ;;
  esac
  if [ "$value" -le 0 ]; then
    echo "$name must be a positive integer." >&2
    return 2
  fi
}

require_positive_integer QIU_MARKET_LOG_MAX_BYTES "$maximum_bytes"
require_positive_integer QIU_MARKET_LOG_KEEP_BYTES "$keep_bytes"
require_positive_integer QIU_MARKET_LOG_ARCHIVE_GENERATIONS "$archive_generations"
if [ "$keep_bytes" -ge "$maximum_bytes" ]; then
  echo "QIU_MARKET_LOG_KEEP_BYTES must be smaller than QIU_MARKET_LOG_MAX_BYTES." >&2
  exit 2
fi

[ -d "$runtime_root" ] || {
  echo "Qiu Market d1-candidate runtime is unavailable: $runtime_root" >&2
  exit 1
}
install -d -m 700 "$log_dir" "$state_dir"
lock_dir="$state_dir/rotation.lock"
if ! mkdir "$lock_dir" 2>/dev/null; then
  exit 0
fi
cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM
  if [ -d "$lock_dir" ]; then
    rmdir "$lock_dir" 2>/dev/null || true
  fi
  exit "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

file_size() {
  local path="$1"
  if stat -f '%z' "$path" >/dev/null 2>&1; then
    stat -f '%z' "$path"
  else
    stat -c '%s' "$path"
  fi
}

rotate_one() {
  local log_file="$1"
  local size
  local generation
  local previous
  local destination
  local archive_tmp
  local retained_tmp

  [ -f "$log_file" ] || return 0
  [ ! -L "$log_file" ] || return 0
  chmod 600 "$log_file"
  size="$(file_size "$log_file")"
  if [ "$size" -le "$maximum_bytes" ]; then
    return 0
  fi

  generation="$archive_generations"
  while [ "$generation" -gt 1 ]; do
    previous="$log_file.$((generation - 1))"
    destination="$log_file.$generation"
    if [ -f "$previous" ] && [ ! -L "$previous" ]; then
      mv -f "$previous" "$destination"
      chmod 600 "$destination"
    fi
    generation=$((generation - 1))
  done

  archive_tmp="$(mktemp "$state_dir/.archive.XXXXXX")"
  retained_tmp="$(mktemp "$state_dir/.retained.XXXXXX")"
  tail -c "$maximum_bytes" "$log_file" > "$archive_tmp"
  tail -c "$keep_bytes" "$log_file" > "$retained_tmp"
  chmod 600 "$archive_tmp" "$retained_tmp"
  mv -f "$archive_tmp" "$log_file.1"

  # Keep the original inode because launchd-managed processes retain their
  # stdout/stderr file descriptors. This is a bounded copy-truncate rotation.
  : > "$log_file"
  cat "$retained_tmp" > "$log_file"
  chmod 600 "$log_file"
  find "$retained_tmp" -maxdepth 0 -type f -delete

  generation=$((archive_generations + 1))
  while [ -e "$log_file.$generation" ]; do
    if [ -f "$log_file.$generation" ] && [ ! -L "$log_file.$generation" ]; then
      find "$log_file.$generation" -maxdepth 0 -type f -delete
    else
      break
    fi
    generation=$((generation + 1))
  done
}

allowed_logs=(
  live-crawler.out.log live-crawler.err.log
  live-dex.out.log live-dex.err.log
  live-worker.out.log live-worker.err.log
  live-api-tunnel.out.log live-api-tunnel.err.log
  live-keepawake.out.log live-keepawake.err.log
  live-log-rotation.out.log live-log-rotation.err.log
  r1-frontdoor.out.log r1-frontdoor.err.log
  r1-stack.out.log r1-stack.err.log
)
for basename in "${allowed_logs[@]}"; do
  log_file="$log_dir/$basename"
  [ -e "$log_file" ] || continue
  rotate_one "$log_file"
done
