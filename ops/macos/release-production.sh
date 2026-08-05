#!/usr/bin/env bash
set -euo pipefail
umask 077

action="${1:-status}"
requested_revision="${2:-HEAD}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "$repo_root/ops/macos/production-lib.sh"
support_dir="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
release_root="$support_dir/releases"
binary_dir="$support_dir/bin"
managed_binary="$binary_dir/market-services"
state_dir="$support_dir/release-state"
lock_dir="$state_dir/deploy.lock"

temporary_release=""
temporary_source=""
owns_lock=false
deploy_switched=false
previous_target=""
production_environment_loaded=false
prepare_lock=""
owns_prepare_lock=false

load_production_environment() {
  if [ "$production_environment_loaded" = true ]; then
    return
  fi
  QIU_MARKET_SUPPORT_DIR="$support_dir"
  export QIU_MARKET_SUPPORT_DIR
  qiu_load_production_environment "$repo_root"
  production_environment_loaded=true
}

atomic_state_write() {
  local target="$1"
  local value="$2"
  local temporary="$target.next.$$"
  printf '%s\n' "$value" > "$temporary"
  chmod 600 "$temporary"
  mv -f "$temporary" "$target"
}

switch_managed_binary() {
  local target="$1"
  local temporary="$binary_dir/.market-services.next.$$"
  if [ ! -x "$target" ]; then
    echo "Release binary is not executable: $target" >&2
    return 1
  fi
  install -d -m 700 "$binary_dir"
  ln -s "$target" "$temporary"
  mv -f "$temporary" "$managed_binary"
}

record_rollback_result() {
  local result="$1"
  local target="$2"
  local result_file="$state_dir/rollback-$result.env"
  {
    printf 'result=%s\n' "$result"
    printf 'target=%s\n' "$target"
    printf 'recorded_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  } > "$result_file"
  chmod 600 "$result_file"
}

perform_automatic_rollback() {
  local target="$1"
  if verify_rollback_target "$target" &&
    switch_managed_binary "$target" &&
    qiu_restart_role trading &&
    qiu_restart_role api &&
    qiu_wait_for_api 60 >/dev/null &&
    qiu_wait_for_trading_status false 60 >/dev/null &&
    atomic_state_write "$state_dir/current-release" "$target"; then
    record_rollback_result "passed" "$target"
    echo "Automatic binary rollback passed health verification: $target" >&2
    return 0
  fi
  record_rollback_result "failed" "$target" || true
  echo "CRITICAL: automatic rollback could not restore a verified healthy runtime; inspect release-state and service logs." >&2
  return 1
}

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM
  if [ "$exit_code" -ne 0 ] &&
    [ "$deploy_switched" = "true" ] &&
    [ -n "$previous_target" ] &&
    [ -x "$previous_target" ]; then
    echo "Deployment failed after activation; restoring the previous binary." >&2
    perform_automatic_rollback "$previous_target" || true
  fi
  if [ -n "$temporary_release" ] && [ -d "$temporary_release" ]; then
    find "$temporary_release" -depth -delete 2>/dev/null || true
  fi
  if [ -n "$temporary_source" ] && [ -d "$temporary_source" ]; then
    find "$temporary_source" -depth -delete 2>/dev/null || true
  fi
  if [ "$owns_lock" = "true" ] && [ -d "$lock_dir" ]; then
    rmdir "$lock_dir" 2>/dev/null || true
  fi
  if [ "$owns_prepare_lock" = true ] && [ -n "$prepare_lock" ] && [ -d "$prepare_lock" ]; then
    rmdir "$prepare_lock" 2>/dev/null || true
  fi
  exit "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_clean_revision() {
  local revision="$1"
  local commit
  commit="$(git -C "$repo_root" rev-parse --verify "$revision^{commit}")"
  if [ -n "$(git -C "$repo_root" status --porcelain)" ]; then
    echo "Release requires a clean Git worktree." >&2
    return 1
  fi
  if [ "$(git -C "$repo_root" rev-parse HEAD)" != "$commit" ]; then
    echo "Requested release must be the checked-out HEAD: $commit" >&2
    return 1
  fi
  printf '%s\n' "$commit"
}

release_dir_for() {
  printf '%s/%s\n' "$release_root" "$1"
}

manifest_value() {
  local manifest="$1"
  local key="$2"
  sed -n "s/^${key}=//p" "$manifest" | head -1
}

cursor_source_for_binary() {
  local binary="$1"
  local manifest
  manifest="$(dirname "$binary")/manifest.env"
  if [ ! -f "$manifest" ]; then
    printf '%s\n' "unknown"
    return
  fi
  manifest_value "$manifest" cursor_source
}

verify_rollback_target() {
  local binary="$1"
  local manifest
  local expected_sha
  local cursor_source
  manifest="$(dirname "$binary")/manifest.env"
  if [ ! -x "$binary" ] || [ ! -f "$manifest" ]; then
    echo "Rollback target or manifest is unavailable: $binary" >&2
    return 1
  fi
  expected_sha="$(manifest_value "$manifest" binary_sha256)"
  if [ -z "$expected_sha" ] || [ "$(qiu_sha256 "$binary")" != "$expected_sha" ]; then
    echo "Rollback target checksum does not match its manifest: $binary" >&2
    return 1
  fi
  cursor_source="$(manifest_value "$manifest" cursor_source)"
  if [ "$cursor_source" != "outbox" ] && [ "$cursor_source" != "event-feed" ]; then
    echo "Rollback target has no recognized cursor capability: $binary" >&2
    return 1
  fi
}

migration_set_sha256() {
  local migration_dir="${1:-$repo_root/migrations}"
  while IFS= read -r migration_file; do
    printf '%s  %s\n' \
      "$(qiu_sha256 "$migration_file")" \
      "$(basename "$migration_file")"
  done < <(find "$migration_dir" -maxdepth 1 -type f -name '*.sql' -print | sort) |
    shasum -a 256 |
    awk '{print $1}'
}

verify_release() {
  local commit="$1"
  local release_dir
  local manifest
  local binary
  local migration_dir
  local expected_binary_sha
  local expected_migration_set_sha
  local expected_migration_count
  local expected_migration_last
  local actual_migration_count
  local actual_migration_last
  release_dir="$(release_dir_for "$commit")"
  manifest="$release_dir/manifest.env"
  binary="$release_dir/market-services"
  migration_dir="$release_dir/migrations"
  if [ ! -x "$binary" ] || [ ! -f "$manifest" ] || [ ! -d "$migration_dir" ]; then
    echo "Prepared release is incomplete: $release_dir" >&2
    return 1
  fi
  if [ "$(manifest_value "$manifest" git_commit)" != "$commit" ]; then
    echo "Release manifest commit does not match $commit." >&2
    return 1
  fi
  if [ "$(manifest_value "$manifest" schema_version)" != "2" ]; then
    echo "Release manifest schema is unsupported." >&2
    return 1
  fi
  expected_binary_sha="$(manifest_value "$manifest" binary_sha256)"
  if [ -z "$expected_binary_sha" ] ||
    [ "$(qiu_sha256 "$binary")" != "$expected_binary_sha" ]; then
    echo "Release binary checksum does not match its manifest." >&2
    return 1
  fi
  expected_migration_set_sha="$(manifest_value "$manifest" migration_set_sha256)"
  if [ -z "$expected_migration_set_sha" ] ||
    [ "$(migration_set_sha256 "$migration_dir")" != "$expected_migration_set_sha" ]; then
    echo "Release migration-set checksum does not match its manifest." >&2
    return 1
  fi
  expected_migration_count="$(manifest_value "$manifest" migration_count)"
  expected_migration_last="$(manifest_value "$manifest" migration_last)"
  actual_migration_count="$(find "$migration_dir" -maxdepth 1 -type f -name '*.sql' | wc -l | tr -d ' ')"
  actual_migration_last="$(find "$migration_dir" -maxdepth 1 -type f -name '*.sql' -print | sort | tail -1 | xargs basename)"
  if find "$migration_dir" -mindepth 1 -type d -print -quit | grep -q . ||
    find "$migration_dir" -maxdepth 1 -type f ! -name '*.sql' -print -quit | grep -q .; then
    echo "Release migrations must be a flat directory containing SQL files only." >&2
    return 1
  fi
  if [ -z "$expected_migration_count" ] ||
    [ "$expected_migration_count" -le 0 ] ||
    [ "$expected_migration_count" != "$actual_migration_count" ] ||
    [ -z "$expected_migration_last" ] ||
    [ "$expected_migration_last" != "$actual_migration_last" ]; then
    echo "Release migration inventory does not match its manifest." >&2
    return 1
  fi
  if [ "$(manifest_value "$manifest" cursor_source)" != "event-feed" ]; then
    echo "Release does not declare the event-feed cursor capability." >&2
    return 1
  fi
}

prepare_release() {
  local commit="$1"
  local release_dir
  local git_timestamp
  local binary_sha
  local migration_set_sha
  release_dir="$(release_dir_for "$commit")"
  install -d -m 700 "$release_root" "$state_dir"
  if [ -d "$release_dir" ]; then
    verify_release "$commit"
    echo "Release already prepared and verified: $release_dir"
    return
  fi
  prepare_lock="$release_root/.prepare.${commit}.lock"
  if ! mkdir "$prepare_lock" 2>/dev/null; then
    echo "Another process is preparing release $commit." >&2
    return 1
  fi
  owns_prepare_lock=true

  temporary_release="$(mktemp -d "$release_root/.prepare.${commit}.XXXXXX")"
  temporary_source="$(mktemp -d "$release_root/.source.${commit}.XXXXXX")"
  git -C "$repo_root" archive "$commit" | tar -xf - -C "$temporary_source"
  git_timestamp="$(git -C "$repo_root" show -s --format='%ct' "$commit")"
  (
    cd "$temporary_source"
    go build -trimpath \
      -ldflags "-X main.GitCommit=$commit -X main.GitData=$git_timestamp" \
      -o "$temporary_release/market-services" \
      ./cmd/market-services
  )
  chmod 700 "$temporary_release/market-services"
  cp -R "$temporary_source/migrations" "$temporary_release/migrations"
  find "$temporary_release/migrations" -type d -exec chmod 700 {} +
  find "$temporary_release/migrations" -type f -exec chmod 600 {} +
  binary_sha="$(qiu_sha256 "$temporary_release/market-services")"
  migration_set_sha="$(migration_set_sha256 "$temporary_release/migrations")"
  {
    printf 'schema_version=2\n'
    printf 'git_commit=%s\n' "$commit"
    printf 'git_timestamp=%s\n' "$git_timestamp"
    printf 'binary_sha256=%s\n' "$binary_sha"
    printf 'migration_set_sha256=%s\n' "$migration_set_sha"
    printf 'migration_count=%s\n' "$(find "$temporary_release/migrations" -maxdepth 1 -type f -name '*.sql' | wc -l | tr -d ' ')"
    printf 'migration_last=%s\n' "$(find "$temporary_release/migrations" -maxdepth 1 -type f -name '*.sql' -print | sort | tail -1 | xargs basename)"
    printf 'cursor_source=event-feed\n'
    printf 'built_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  } > "$temporary_release/manifest.env"
  chmod 600 "$temporary_release/manifest.env"
  mv "$temporary_release" "$release_dir"
  temporary_release=""
  find "$temporary_source" -depth -delete
  temporary_source=""
  verify_release "$commit"
  rmdir "$prepare_lock"
  owns_prepare_lock=false
  echo "Prepared immutable release: $release_dir"
}

verify_source_and_release() {
  local commit="$1"
  local release_dir
  local binary
  local migration_dir
  release_dir="$(release_dir_for "$commit")"
  binary="$release_dir/market-services"
  migration_dir="$release_dir/migrations"

  (
    cd "$repo_root"
    go test ./...
    go test -race ./trading/...
    go vet ./...
    bash ops/macos/test-production-slo.sh
    for script in ops/macos/*.sh; do
      bash -n "$script"
    done
    git diff --check
  )
  (
    cd "$repo_root/frontend"
    npm test -- --run
    npm run build
  )
  MARKET_MIGRATIONS_DIR="$migration_dir" \
    QIU_MARKET_BINARY="$binary" "$repo_root/ops/macos/restore-drill.sh"
  {
    printf 'schema_version=1\n'
    printf 'git_commit=%s\n' "$commit"
    printf 'binary_sha256=%s\n' "$(qiu_sha256 "$binary")"
    printf 'migration_set_sha256=%s\n' "$(migration_set_sha256 "$migration_dir")"
    printf 'verified_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'checks=go-test,go-race,go-vet,frontend-test,frontend-build,slo-fixtures,shell-syntax,diff-check,restore-drill\n'
  } > "$release_dir/verification.env"
  chmod 600 "$release_dir/verification.env"
  echo "Release verification passed: $release_dir"
}

require_verified_release() {
  local commit="$1"
  local release_dir
  local verification
  local binary
  release_dir="$(release_dir_for "$commit")"
  verification="$release_dir/verification.env"
  binary="$release_dir/market-services"
  verify_release "$commit"
  if [ ! -f "$verification" ] ||
    [ "$(manifest_value "$verification" git_commit)" != "$commit" ] ||
    [ "$(manifest_value "$verification" binary_sha256)" != "$(qiu_sha256 "$binary")" ] ||
    [ "$(manifest_value "$verification" migration_set_sha256)" != \
      "$(migration_set_sha256 "$release_dir/migrations")" ]; then
    echo "Release has not passed the exact-artifact verification gate: $release_dir" >&2
    return 1
  fi
}

preflight_production() {
  local available_bytes
  local minimum_deploy_bytes="${QIU_MARKET_MINIMUM_DEPLOY_BYTES:-35000000000}"
  for command_name in curl git go jq launchctl nc npm pg_dump pg_isready pg_restore psql shasum; do
    qiu_require_command "$command_name"
  done
  qiu_require_private_environment
  if [ -z "${MARKET_PUBLIC_PROXY_HMAC_SECRET:-}" ] ||
    [[ "${MARKET_PUBLIC_PROXY_HMAC_SECRET:-}" == replace-with-* ]]; then
    echo "Production HMAC secret is missing or still a placeholder." >&2
    return 1
  fi
  if ! pg_isready -q \
    --host="$QIU_MARKET_DB_HOST" \
    --port="$QIU_MARKET_DB_PORT" \
    --dbname="$QIU_MARKET_DB_NAME"; then
    echo "Production PostgreSQL is not ready." >&2
    return 1
  fi
  available_bytes="$(
    df -k /System/Volumes/Data |
      awk 'NR == 2 { printf "%.0f", $4 * 1024 }'
  )"
  if [ -z "$available_bytes" ] || [ "$available_bytes" -lt "$minimum_deploy_bytes" ]; then
    echo "Production deploy requires at least $minimum_deploy_bytes free bytes; available=${available_bytes:-unknown}." >&2
    return 1
  fi
  echo "Production preflight passed: free_bytes=$available_bytes minimum=$minimum_deploy_bytes"
}

preflight_rollback() {
  for command_name in curl jq launchctl pg_dump pg_isready shasum; do
    qiu_require_command "$command_name"
  done
  qiu_require_private_environment
  if ! pg_isready -q \
    --host="$QIU_MARKET_DB_HOST" \
    --port="$QIU_MARKET_DB_PORT" \
    --dbname="$QIU_MARKET_DB_NAME"; then
    echo "Production PostgreSQL is not ready; refusing a blind binary rollback." >&2
    return 1
  fi
}

acquire_deploy_lock() {
  install -d -m 700 "$state_dir"
  if ! mkdir "$lock_dir" 2>/dev/null; then
    echo "Another Qiu Market deployment owns $lock_dir." >&2
    return 1
  fi
  owns_lock=true
}

capture_current_target() {
  local binary_sha
  local legacy_dir
  if [ -L "$managed_binary" ]; then
    previous_target="$(readlink "$managed_binary")"
  elif [ -x "$managed_binary" ]; then
    binary_sha="$(qiu_sha256 "$managed_binary")"
    legacy_dir="$release_root/legacy-$binary_sha"
    if [ ! -d "$legacy_dir" ]; then
      install -d -m 700 "$legacy_dir"
      cp "$managed_binary" "$legacy_dir/market-services"
      chmod 700 "$legacy_dir/market-services"
      {
        printf 'schema_version=1\n'
        printf 'git_commit=unknown-legacy\n'
        printf 'binary_sha256=%s\n' "$binary_sha"
        printf 'cursor_source=outbox\n'
        printf 'captured_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
      } > "$legacy_dir/manifest.env"
      chmod 600 "$legacy_dir/manifest.env"
    fi
    previous_target="$legacy_dir/market-services"
  else
    previous_target=""
  fi
  if [ -n "$previous_target" ] && [ ! -x "$previous_target" ]; then
    echo "Current managed binary points to an invalid target: $previous_target" >&2
    return 1
  fi
  if [ -n "$previous_target" ]; then
    verify_rollback_target "$previous_target"
  fi
}

verify_migration_ledger_contract() {
  local migration_dir="${1:-$repo_root/migrations}"
  local filename
  local recorded_checksum
  local local_path
  local local_checksum
  local pending_count=0
  local pending_names=()
  local seen_pending=false
  local ledger_exists

  ledger_exists="$(qiu_psql -c "
SELECT to_regclass('public.s78_schema_migrations') IS NOT NULL
")"
  if [ "$ledger_exists" != "t" ]; then
    echo "Production migration ledger is absent; refusing an unbounded migration run." >&2
    return 1
  fi

  while IFS='|' read -r filename recorded_checksum; do
    local_path="$migration_dir/$filename"
    if [[ ! "$filename" =~ ^[0-9]+\.sql$ ]] || [ ! -f "$local_path" ]; then
      echo "Production migration ledger contains an unknown file: $filename" >&2
      return 1
    fi
    local_checksum="$(qiu_sha256 "$local_path")"
    if [ "$recorded_checksum" != "$local_checksum" ]; then
      echo "Applied migration checksum diverged: $filename" >&2
      return 1
    fi
  done < <(qiu_psql -F '|' -c "
SELECT filename, checksum_sha256
FROM s78_schema_migrations
ORDER BY filename
")

  while IFS= read -r local_path; do
    filename="$(basename "$local_path")"
    if [[ ! "$filename" =~ ^[0-9]+\.sql$ ]]; then
      echo "Candidate migration filename is unsafe: $filename" >&2
      return 1
    fi
    recorded_checksum="$(qiu_psql -c "
SELECT checksum_sha256
FROM s78_schema_migrations
WHERE filename='$filename'
")"
    if [ -z "$recorded_checksum" ]; then
      pending_count=$((pending_count + 1))
      pending_names+=("$filename")
      seen_pending=true
    elif [ "$seen_pending" = true ]; then
      echo "Migration ledger is not a contiguous prefix; applied migration follows pending $filename." >&2
      return 1
    fi
  done < <(find "$migration_dir" -maxdepth 1 -type f -name '*.sql' -print | sort)

  echo "Migration ledger contract passed: pending_count=$pending_count pending=${pending_names[*]:-none}"
}

verify_migration() {
  local migration_dir="$1"
  local filename
  local migration_file
  local expected_sha
  local recorded_sha
  local table_state
  verify_migration_ledger_contract "$migration_dir"
  while IFS= read -r migration_file; do
    filename="$(basename "$migration_file")"
    expected_sha="$(qiu_sha256 "$migration_file")"
    recorded_sha="$(qiu_psql -c \
      "SELECT checksum_sha256 FROM s78_schema_migrations WHERE filename='$filename'")"
    if [ "$recorded_sha" != "$expected_sha" ]; then
      echo "Production migration ledger checksum mismatch for $filename." >&2
      return 1
    fi
  done < <(find "$migration_dir" -maxdepth 1 -type f -name '*.sql' -print | sort)
  table_state="$(qiu_psql -F '|' -c "
SELECT
  to_regclass('public.trading_event_feed') IS NOT NULL,
  to_regclass('public.trading_outbox_checkpoint') IS NOT NULL
")"
  if [ "$table_state" != "t|t" ]; then
    echo "Production event feed or outbox checkpoint table is absent." >&2
    return 1
  fi
}

wait_for_outbox_drain() {
  local attempt
  local stats
  local unpublished
  local oldest_unpublished_seconds
  local published_mismatches
  local cursor_mismatches
  local feed_rows
  local source_rows
  for attempt in $(seq 1 240); do
    stats="$(qiu_outbox_integrity_stats)"
    IFS='|' read -r \
      unpublished \
      oldest_unpublished_seconds \
      published_mismatches \
      cursor_mismatches \
      feed_rows \
      source_rows \
      <<<"$stats"
    if [ "$unpublished" -eq 0 ] &&
      [ "$oldest_unpublished_seconds" -eq 0 ] &&
      [ "$published_mismatches" -eq 0 ] &&
      [ "$cursor_mismatches" -eq 0 ] &&
      [ "$feed_rows" -gt 0 ]; then
      echo "Outbox caught up: unpublished=0 published_mismatches=0 cursor_mismatches=0 feed_rows=$feed_rows source_rows=$source_rows"
      return 0
    fi
    sleep 1
  done
  echo "Outbox did not catch up within 240 seconds: ${stats:-unavailable}" >&2
  return 1
}

deploy_release() {
  local commit="$1"
  local release_dir
  local release_binary
  local migration_dir
  local full_backup
  release_dir="$(release_dir_for "$commit")"
  release_binary="$release_dir/market-services"
  migration_dir="$release_dir/migrations"

  require_verified_release "$commit"
  preflight_production
  verify_migration_ledger_contract "$migration_dir"
  acquire_deploy_lock
  capture_current_target

  full_backup="$(
    QIU_MARKET_DEFER_BACKUP_RETENTION=true \
      "$repo_root/ops/macos/backup-production.sh" full
  )"
  QIU_MARKET_DEFER_BACKUP_RETENTION=true \
    "$repo_root/ops/macos/backup-production.sh" trading >/dev/null
  MARKET_MIGRATIONS_DIR="$migration_dir" \
    QIU_MARKET_BINARY="$release_binary" \
    QIU_MARKET_BACKUP="$full_backup" \
    "$repo_root/ops/macos/restore-drill.sh"

  (
    cd "$release_dir"
    export MARKET_MIGRATIONS_DIR="$migration_dir"
    "$release_binary" migrate
  )
  verify_migration "$migration_dir"

  switch_managed_binary "$release_binary"
  deploy_switched=true
  if [ -n "$previous_target" ]; then
    atomic_state_write "$state_dir/previous-release" "$previous_target"
  fi
  atomic_state_write "$state_dir/current-release" "$release_binary"

  qiu_restart_role trading
  sleep 1
  qiu_restart_role api
  qiu_wait_for_api 90
  qiu_wait_for_trading_status true 120 >/dev/null
  wait_for_outbox_drain
  qiu_wait_for_trading_status true 30 >/dev/null
  if [ -n "$previous_target" ] &&
    [ "$(cursor_source_for_binary "$previous_target")" = "outbox" ]; then
    atomic_state_write \
      "$state_dir/legacy-rollback-deadline-epoch" \
      "$(($(date '+%s') + 86400))"
  fi
  if ! "$repo_root/ops/macos/observe-production.sh"; then
    echo "Local release passed, but the external observer smoke check failed; Gate 2 remains blocked." >&2
  fi
  echo "Production release accepted locally: commit=$commit binary_sha256=$(qiu_sha256 "$release_binary")"
}

rollback_release() {
  local target="${1:-}"
  local current_target
  local rollback_deadline
  local missing_legacy_rows
  if [ -z "$target" ] && [ -f "$state_dir/previous-release" ]; then
    target="$(cat "$state_dir/previous-release")"
  fi
  if [ -z "$target" ] || [ ! -x "$target" ]; then
    echo "No valid previous release is available for rollback." >&2
    return 1
  fi
  case "$target" in
    "$release_root"/*/market-services) ;;
    *)
      echo "Rollback target must be a captured Qiu Market release: $target" >&2
      return 1
      ;;
  esac
  verify_rollback_target "$target"
  preflight_rollback
  if [ "$(cursor_source_for_binary "$target")" = "outbox" ]; then
    rollback_deadline="$(cat "$state_dir/legacy-rollback-deadline-epoch" 2>/dev/null || echo 0)"
    if [ "$(date '+%s')" -gt "$rollback_deadline" ]; then
      echo "Legacy outbox-cursor rollback compatibility window has expired." >&2
      return 1
    fi
    missing_legacy_rows="$(qiu_psql -c "
SELECT count(*)
FROM trading_event_feed feed
LEFT JOIN trading_outbox source
  USING (market_id, sequence, event_index)
WHERE source.market_id IS NULL
")"
    if [ "$missing_legacy_rows" -ne 0 ]; then
      echo "Legacy rollback is unsafe because $missing_legacy_rows feed rows no longer exist in outbox." >&2
      return 1
    fi
  fi
  acquire_deploy_lock
  capture_current_target
  current_target="$previous_target"
  "$repo_root/ops/macos/backup-production.sh" trading
  switch_managed_binary "$target"
  deploy_switched=true
  previous_target="$current_target"
  qiu_restart_role trading
  sleep 1
  qiu_restart_role api
  qiu_wait_for_api 90
  qiu_wait_for_trading_status false 120 >/dev/null
  atomic_state_write "$state_dir/current-release" "$target"
  atomic_state_write "$state_dir/previous-release" "$current_target"
  record_rollback_result "passed" "$target"
  deploy_switched=false
  echo "Production binary rolled back to: $target"
}

show_status() {
  local current_target="unmanaged"
  local current_sha="unavailable"
  local migration_state="unavailable"
  local feed_state="absent"
  local outbox_stats="unavailable"
  if [ -L "$managed_binary" ]; then
    current_target="$(readlink "$managed_binary")"
  elif [ -x "$managed_binary" ]; then
    current_target="$managed_binary (legacy regular file)"
  fi
  if [ -x "$managed_binary" ]; then
    current_sha="$(qiu_sha256 "$managed_binary")"
  fi
  if pg_isready -q \
    --host="$QIU_MARKET_DB_HOST" \
    --port="$QIU_MARKET_DB_PORT" \
    --dbname="$QIU_MARKET_DB_NAME"; then
    migration_state="$(qiu_psql -F ',' -c "
SELECT count(*), COALESCE(max(filename),'none') FROM s78_schema_migrations
")"
    feed_state="$(qiu_psql -c "
SELECT CASE WHEN to_regclass('public.trading_event_feed') IS NOT NULL
  THEN 'present' ELSE 'absent' END
")"
    if [ "$feed_state" = "present" ]; then
      outbox_stats="$(qiu_psql -F ',' -c "
SELECT
  (SELECT count(*) FROM trading_outbox WHERE published_at IS NULL),
  (SELECT count(*) FROM trading_event_feed),
  (SELECT COALESCE(max(sequence),0) FROM trading_outbox_checkpoint)
")"
    fi
  fi
  echo "managed_target=$current_target"
  echo "managed_binary_sha256=$current_sha"
  echo "migration_count,latest=$migration_state"
  echo "event_feed=$feed_state"
  echo "outbox_unpublished,feed_rows,checkpoint_sequence=$outbox_stats"
  if payload="$(qiu_signed_trading_status 2>/dev/null)"; then
    jq '{state,sequence,last_error,outbox_state,outbox_checkpoint_sequence,outbox_last_error}' \
      <<<"$payload"
  else
    echo "trading_status=unavailable"
  fi
}

case "$action" in
  preflight)
    require_clean_revision "$requested_revision" >/dev/null
    load_production_environment
    preflight_production
    verify_migration_ledger_contract "$repo_root/migrations"
    ;;
  prepare)
    commit="$(require_clean_revision "$requested_revision")"
    prepare_release "$commit"
    ;;
  verify)
    commit="$(require_clean_revision "$requested_revision")"
    prepare_release "$commit"
    load_production_environment
    verify_source_and_release "$commit"
    ;;
  artifact)
    commit="$(require_clean_revision "$requested_revision")"
    verify_release "$commit"
    echo "Immutable release artifact verified: $(release_dir_for "$commit")"
    ;;
  deploy)
    if [ "${3:-}" != "--execute" ]; then
      echo "Deploy is side-effecting; rerun with: $0 deploy $requested_revision --execute" >&2
      exit 2
    fi
    commit="$(require_clean_revision "$requested_revision")"
    QIU_MARKET_SUPPORT_DIR="$support_dir" \
      qiu_require_release_coordination release-deploy "$commit"
    load_production_environment
    deploy_release "$commit"
    ;;
  rollback)
    if [ "${3:-}" != "--execute" ]; then
      echo "Rollback is side-effecting; rerun with: $0 rollback ${2:-<release>} --execute" >&2
      exit 2
    fi
    QIU_MARKET_SUPPORT_DIR="$support_dir" \
      qiu_require_release_coordination release-rollback "${2:-}"
    load_production_environment
    rollback_release "${2:-}"
    ;;
  status)
    load_production_environment
    show_status
    ;;
  *)
    echo "Usage: $0 preflight|prepare|artifact|verify|status [revision] | deploy <revision> --execute | rollback <release> --execute" >&2
    exit 2
    ;;
esac
