#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
harness="$repo_root/ops/macos/fixtures/observer-lock-harness.sh"
fixture_root="$(mktemp -d)"
lock_dir="$fixture_root/observations/.observer.lock"
archive_dir="$fixture_root/observations/archive/observer-locks"

cleanup() {
  rm -rf -- "$fixture_root"
}
trap cleanup EXIT

wait_for_file() {
  local target="$1"
  local attempt
  for attempt in $(seq 1 100); do
    if [ -f "$target" ]; then
      return 0
    fi
    sleep 0.02
  done
  echo "Timed out waiting for fixture file: $target" >&2
  return 1
}

# A normal run owns the lock and removes only its own token on exit.
bash "$harness" "$lock_dir" "$archive_dir" once > "$fixture_root/once.out"
grep -q '^acquired:' "$fixture_root/once.out"
[ ! -e "$lock_dir" ]

# A live owner is never killed or replaced; the contender reports busy.
bash "$harness" "$lock_dir" "$archive_dir" hold 5 > "$fixture_root/live.out" &
live_pid=$!
wait_for_file "$lock_dir/owner.ready"
if bash "$harness" "$lock_dir" "$archive_dir" once > "$fixture_root/busy.out"; then
  echo "A concurrent observer unexpectedly acquired a live lock." >&2
  exit 1
fi
grep -q '^busy:owner-live$' "$fixture_root/busy.out"
kill -0 "$live_pid"
kill -TERM "$live_pid"
if wait "$live_pid"; then
  echo "TERM fixture unexpectedly exited successfully." >&2
  exit 1
fi
[ ! -e "$lock_dir" ]
find "$archive_dir/events" -type f -name '*signal-term.evidence' -print -quit |
  grep -q .

# SIGKILL cannot run cleanup. The next observer validates the dead PID, moves
# the entire lock to the evidence archive, and acquires a fresh lock.
bash "$harness" "$lock_dir" "$archive_dir" hold 5 > "$fixture_root/killed.out" &
killed_pid=$!
wait_for_file "$lock_dir/owner.ready"
kill -KILL "$killed_pid"
if wait "$killed_pid"; then
  echo "KILL fixture unexpectedly exited successfully." >&2
  exit 1
fi
[ -d "$lock_dir" ]
bash "$harness" "$lock_dir" "$archive_dir" once > "$fixture_root/recovered.out"
grep -q '^acquired:' "$fixture_root/recovered.out"
[ ! -e "$lock_dir" ]
recovery_evidence="$(find "$archive_dir" -type f -name recovery.evidence -print -quit)"
[ -n "$recovery_evidence" ]
grep -q '^event=stale-lock-recovered$' "$recovery_evidence"
grep -q '^reason=owner-not-running$' "$recovery_evidence"

# A recycled PID must not be accepted as the original owner. The contender
# compares process start identity and command, archives the lock, and never
# signals the unrelated live PID.
mkdir -p "$lock_dir"
printf '%s\n' "$$" > "$lock_dir/owner.pid"
printf '%s\n' 'recycled-pid-fixture' > "$lock_dir/owner.token"
printf '%s\n' 'not-the-current-process-start' > "$lock_dir/owner.process-start"
printf '%s\n' "$harness" > "$lock_dir/owner.script"
printf '%s\n' '2026-01-01T00:00:00Z' > "$lock_dir/owner.started-at"
: > "$lock_dir/owner.ready"
bash "$harness" "$lock_dir" "$archive_dir" once > "$fixture_root/reused.out"
grep -q '^acquired:' "$fixture_root/reused.out"
kill -0 "$$"
find "$archive_dir" -type f -name recovery.evidence \
  -exec grep -l '^reason=owner-pid-reused-or-command-changed$' {} + |
  grep -q .

# On an empty lock, simultaneous contenders still produce exactly one owner.
mkdir -p "$fixture_root/race"
race_lock="$fixture_root/race/.observer.lock"
race_archive="$fixture_root/race/archive"
race_pids=()
for index in 1 2 3 4; do
  bash "$harness" "$race_lock" "$race_archive" hold 1 \
    > "$fixture_root/race/$index.out" &
  race_pids+=("$!")
done
for race_pid in "${race_pids[@]}"; do
  wait "$race_pid" || true
done
acquired_count="$(grep -l '^acquired:' "$fixture_root"/race/*.out | wc -l | tr -d ' ')"
busy_count="$(grep -l '^busy:' "$fixture_root"/race/*.out | wc -l | tr -d ' ')"
[ "$acquired_count" = 1 ]
[ "$busy_count" = 3 ]
[ ! -e "$race_lock" ]

echo "Observer lock fixtures passed."
