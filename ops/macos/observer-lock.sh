#!/usr/bin/env bash

# The observer runs from launchd once per wall-clock minute. A directory lock is
# used instead of flock because macOS does not provide flock by default. The
# owner identity contains both PID and the process start timestamp, preventing a
# recycled PID from keeping a dead lock alive.

QIU_OBSERVER_LOCK_OWNED=false
QIU_OBSERVER_LOCK_TOKEN=""
QIU_OBSERVER_LOCK_DIR=""
QIU_OBSERVER_LOCK_ARCHIVE_DIR=""
QIU_OBSERVER_LOCK_SCRIPT=""
QIU_OBSERVER_LOCK_BUSY_REASON=""

qiu_observer_process_start() {
  ps -p "$1" -o lstart= 2>/dev/null |
    awk '{$1=$1; print; exit}'
}

qiu_observer_process_command() {
  ps -p "$1" -o command= 2>/dev/null |
    awk 'NR == 1 { print; exit }'
}

qiu_observer_lock_mtime() {
  local target="$1"
  stat -f '%m' "$target" 2>/dev/null || stat -c '%Y' "$target" 2>/dev/null || true
}

qiu_observer_lock_read() {
  local target="$1"
  if [ ! -f "$target" ]; then
    return 0
  fi
  awk 'NR == 1 { print; exit }' "$target"
}

qiu_observer_lock_slug() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '_'
}

qiu_observer_write_evidence() {
  local target="$1"
  local event="$2"
  local reason="$3"
  local owner_pid="$4"
  local owner_token="$5"
  local temp_file="${target}.tmp.$$.$RANDOM"
  {
    printf 'schema_version=1\n'
    printf 'event=%s\n' "$event"
    printf 'reason=%s\n' "$reason"
    printf 'observed_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'observer_pid=%s\n' "$$"
    printf 'owner_pid=%s\n' "$owner_pid"
    printf 'owner_token=%s\n' "$owner_token"
  } > "$temp_file"
  chmod 600 "$temp_file"
  mv "$temp_file" "$target"
}

qiu_observer_lock_is_active() {
  local lock_dir="$1"
  local now_epoch
  local lock_mtime
  local lock_age
  local owner_pid
  local owner_token
  local owner_start
  local owner_script
  local live_start
  local live_command

  if [ ! -f "$lock_dir/owner.ready" ]; then
    now_epoch="$(date -u '+%s')"
    lock_mtime="$(qiu_observer_lock_mtime "$lock_dir")"
    if [[ "$lock_mtime" =~ ^[0-9]+$ ]]; then
      lock_age=$((now_epoch - lock_mtime))
    else
      lock_age=0
    fi
    if [ "$lock_age" -lt 30 ]; then
      QIU_OBSERVER_LOCK_BUSY_REASON="owner-initializing"
      return 0
    fi
    QIU_OBSERVER_LOCK_BUSY_REASON="incomplete-owner-metadata"
    return 1
  fi

  owner_pid="$(qiu_observer_lock_read "$lock_dir/owner.pid")"
  owner_token="$(qiu_observer_lock_read "$lock_dir/owner.token")"
  owner_start="$(qiu_observer_lock_read "$lock_dir/owner.process-start")"
  owner_script="$(qiu_observer_lock_read "$lock_dir/owner.script")"
  if [[ ! "$owner_pid" =~ ^[0-9]+$ ]] ||
    [ -z "$owner_token" ] ||
    [ -z "$owner_start" ] ||
    [ -z "$owner_script" ]; then
    QIU_OBSERVER_LOCK_BUSY_REASON="invalid-owner-metadata"
    return 1
  fi
  if ! kill -0 "$owner_pid" 2>/dev/null; then
    QIU_OBSERVER_LOCK_BUSY_REASON="owner-not-running"
    return 1
  fi

  live_start="$(qiu_observer_process_start "$owner_pid")"
  live_command="$(qiu_observer_process_command "$owner_pid")"
  if [ -z "$live_start" ] || [ -z "$live_command" ]; then
    # A process exists but its identity cannot be inspected. Fail closed rather
    # than reclaiming a lock that may still be live.
    QIU_OBSERVER_LOCK_BUSY_REASON="owner-live-identity-unobservable"
    return 0
  fi
  if [ "$live_start" = "$owner_start" ] &&
    [[ "$live_command" == *"$owner_script"* ]]; then
    QIU_OBSERVER_LOCK_BUSY_REASON="owner-live"
    return 0
  fi

  QIU_OBSERVER_LOCK_BUSY_REASON="owner-pid-reused-or-command-changed"
  return 1
}

qiu_observer_archive_stale_lock() {
  local lock_dir="$1"
  local archive_dir="$2"
  local reason="$3"
  local owner_pid
  local owner_token
  local timestamp
  local token_slug
  local destination

  owner_pid="$(qiu_observer_lock_read "$lock_dir/owner.pid")"
  owner_token="$(qiu_observer_lock_read "$lock_dir/owner.token")"
  timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
  token_slug="$(qiu_observer_lock_slug "${owner_token:-unknown}")"
  destination="$archive_dir/${timestamp}-${token_slug}-$$-$RANDOM"
  mkdir -p "$archive_dir"
  chmod 700 "$archive_dir"
  if ! mv "$lock_dir" "$destination" 2>/dev/null; then
    return 1
  fi
  qiu_observer_write_evidence \
    "$destination/recovery.evidence" \
    "stale-lock-recovered" \
    "$reason" \
    "${owner_pid:-unknown}" \
    "${owner_token:-unknown}"
  return 0
}

qiu_observer_initialize_lock() {
  local lock_dir="$1"
  local script_path="$2"
  local owner_start

  QIU_OBSERVER_LOCK_TOKEN="$$-$(date -u '+%s')-$RANDOM"
  owner_start="$(qiu_observer_process_start "$$")"
  if [ -z "$owner_start" ]; then
    return 1
  fi
  chmod 700 "$lock_dir"
  printf '%s\n' "$$" > "$lock_dir/owner.pid"
  printf '%s\n' "$QIU_OBSERVER_LOCK_TOKEN" > "$lock_dir/owner.token"
  printf '%s\n' "$owner_start" > "$lock_dir/owner.process-start"
  printf '%s\n' "$script_path" > "$lock_dir/owner.script"
  printf '%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" > "$lock_dir/owner.started-at"
  chmod 600 "$lock_dir"/owner.*
  : > "$lock_dir/owner.ready"
  chmod 600 "$lock_dir/owner.ready"
}

qiu_observer_acquire_lock() {
  local lock_dir="$1"
  local archive_dir="$2"
  local script_path="$3"
  local attempt

  QIU_OBSERVER_LOCK_DIR="$lock_dir"
  QIU_OBSERVER_LOCK_ARCHIVE_DIR="$archive_dir"
  QIU_OBSERVER_LOCK_SCRIPT="$script_path"
  QIU_OBSERVER_LOCK_BUSY_REASON=""
  mkdir -p "$(dirname "$lock_dir")"

  for attempt in 1 2 3 4 5; do
    if mkdir "$lock_dir" 2>/dev/null; then
      if ! qiu_observer_initialize_lock "$lock_dir" "$script_path"; then
        unlink "$lock_dir/owner.pid" 2>/dev/null || true
        unlink "$lock_dir/owner.token" 2>/dev/null || true
        unlink "$lock_dir/owner.process-start" 2>/dev/null || true
        unlink "$lock_dir/owner.script" 2>/dev/null || true
        unlink "$lock_dir/owner.started-at" 2>/dev/null || true
        unlink "$lock_dir/owner.ready" 2>/dev/null || true
        rmdir "$lock_dir" 2>/dev/null || true
        QIU_OBSERVER_LOCK_BUSY_REASON="owner-initialization-failed"
        return 1
      fi
      QIU_OBSERVER_LOCK_OWNED=true
      return 0
    fi
    if qiu_observer_lock_is_active "$lock_dir"; then
      return 1
    fi
    if ! qiu_observer_archive_stale_lock \
      "$lock_dir" \
      "$archive_dir" \
      "$QIU_OBSERVER_LOCK_BUSY_REASON"; then
      sleep 0.05
    fi
  done
  QIU_OBSERVER_LOCK_BUSY_REASON="lock-contention"
  return 1
}

qiu_observer_record_lock_event() {
  local event="$1"
  local reason="$2"
  local event_dir
  local timestamp
  local token_slug
  local target

  if [ "$QIU_OBSERVER_LOCK_OWNED" != true ]; then
    return 0
  fi
  event_dir="$QIU_OBSERVER_LOCK_ARCHIVE_DIR/events"
  timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
  token_slug="$(qiu_observer_lock_slug "$QIU_OBSERVER_LOCK_TOKEN")"
  mkdir -p "$event_dir"
  chmod 700 "$QIU_OBSERVER_LOCK_ARCHIVE_DIR" "$event_dir"
  target="$event_dir/${timestamp}-${token_slug}-$(qiu_observer_lock_slug "$event").evidence"
  qiu_observer_write_evidence \
    "$target" \
    "$event" \
    "$reason" \
    "$$" \
    "$QIU_OBSERVER_LOCK_TOKEN"
}

qiu_observer_release_lock() {
  local observed_token

  if [ "$QIU_OBSERVER_LOCK_OWNED" != true ] ||
    [ ! -d "$QIU_OBSERVER_LOCK_DIR" ]; then
    return 0
  fi
  observed_token="$(qiu_observer_lock_read "$QIU_OBSERVER_LOCK_DIR/owner.token")"
  if [ "$observed_token" != "$QIU_OBSERVER_LOCK_TOKEN" ]; then
    return 0
  fi
  unlink "$QIU_OBSERVER_LOCK_DIR/owner.pid" 2>/dev/null || true
  unlink "$QIU_OBSERVER_LOCK_DIR/owner.token" 2>/dev/null || true
  unlink "$QIU_OBSERVER_LOCK_DIR/owner.process-start" 2>/dev/null || true
  unlink "$QIU_OBSERVER_LOCK_DIR/owner.script" 2>/dev/null || true
  unlink "$QIU_OBSERVER_LOCK_DIR/owner.started-at" 2>/dev/null || true
  unlink "$QIU_OBSERVER_LOCK_DIR/owner.ready" 2>/dev/null || true
  rmdir "$QIU_OBSERVER_LOCK_DIR" 2>/dev/null || true
  QIU_OBSERVER_LOCK_OWNED=false
}
