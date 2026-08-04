#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck disable=SC1091
source "$repo_root/ops/macos/observer-lock.sh"

lock_dir="$1"
archive_dir="$2"
mode="${3:-once}"
hold_seconds="${4:-2}"

if ! qiu_observer_acquire_lock "$lock_dir" "$archive_dir" "$0"; then
  printf 'busy:%s\n' "$QIU_OBSERVER_LOCK_BUSY_REASON"
  exit 75
fi

cleanup() {
  qiu_observer_release_lock
}
on_signal() {
  local signal_name="$1"
  local exit_code="$2"
  qiu_observer_record_lock_event "signal-${signal_name}" "fixture-signal"
  exit "$exit_code"
}
trap cleanup EXIT
trap 'on_signal hup 129' HUP
trap 'on_signal int 130' INT
trap 'on_signal term 143' TERM

printf 'acquired:%s\n' "$$"
if [ "$mode" = hold ]; then
  sleep "$hold_seconds"
fi
