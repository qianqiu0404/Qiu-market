#!/usr/bin/env bash
set -euo pipefail
umask 077

action="${1:-status}"
revision="${2:-HEAD}"
execute_flag="${3:-}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
support_dir="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
release_tool="$repo_root/ops/macos/release-production.sh"
runtime_tool="$repo_root/ops/macos/manage-runtime-release.sh"
managed_binary="$support_dir/bin/market-services"
runtime_current="$support_dir/runtime-current"
state_dir="$support_dir/release-state"
candidate_lock="$state_dir/candidate-activation.lock"
candidate_state="$state_dir/candidate-activation.env"
owns_lock=false

if [ "${QIU_MARKET_RELEASE_TEST_MODE:-false}" = true ]; then
  fixture_root="$(dirname "$support_dir")"
  case "$support_dir" in /tmp/qiu-market-release-candidate.*/*) ;;
    *) echo "Release test mode requires an isolated Qiu Market fixture." >&2; exit 2 ;;
  esac
  release_tool="${QIU_MARKET_RELEASE_TOOL:-$release_tool}"
  runtime_tool="${QIU_MARKET_RUNTIME_TOOL:-$runtime_tool}"
  case "$release_tool" in "$fixture_root"/*) ;;
    *) echo "Fixture release tool must stay inside the isolated fixture." >&2; exit 2 ;;
  esac
  case "$runtime_tool" in "$fixture_root"/*) ;;
    *) echo "Fixture runtime tool must stay inside the isolated fixture." >&2; exit 2 ;;
  esac
fi

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM
  if [ "$owns_lock" = true ] && [ -d "$candidate_lock" ]; then
    if [ -f "$candidate_lock/context.env" ]; then
      find "$candidate_lock/context.env" -maxdepth 0 -type f -delete 2>/dev/null || true
    fi
    rmdir "$candidate_lock" 2>/dev/null || true
  fi
  exit "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

resolve_commit() {
  git -C "$repo_root" rev-parse --verify "$1^{commit}"
}

manifest_value() {
  local manifest="$1"
  local key="$2"
  sed -n "s/^${key}=//p" "$manifest" | head -1
}

require_same_clean_head() {
  local commit="$1"
  if [ -n "$(git -C "$repo_root" status --porcelain)" ]; then
    echo "Candidate activation requires a clean Git worktree." >&2
    return 1
  fi
  if [ "$(git -C "$repo_root" rev-parse HEAD)" != "$commit" ]; then
    echo "Candidate must be the checked-out HEAD: $commit" >&2
    return 1
  fi
}

verify_candidate_pair() {
  local commit="$1"
  local binary_manifest="$support_dir/releases/$commit/manifest.env"
  local runtime_manifest="$support_dir/runtime-releases/$commit/runtime-manifest.env"
  "$release_tool" artifact "$commit" >/dev/null
  "$runtime_tool" verify "$commit" >/dev/null
  if [ "$(manifest_value "$binary_manifest" git_commit)" != "$commit" ] ||
    [ "$(manifest_value "$runtime_manifest" git_commit)" != "$commit" ]; then
    echo "Binary and runtime manifests do not bind the same candidate commit." >&2
    return 1
  fi
  printf 'candidate_commit=%s\n' "$commit"
  printf 'binary_sha256=%s\n' "$(manifest_value "$binary_manifest" binary_sha256)"
  printf 'migration_set_sha256=%s\n' "$(manifest_value "$binary_manifest" migration_set_sha256)"
  printf 'runtime_bundle_sha256=%s\n' "$(manifest_value "$runtime_manifest" bundle_sha256)"
}

require_rollback_baseline() {
  if [ ! -L "$managed_binary" ] || [ ! -x "$managed_binary" ]; then
    echo "Managed binary is not an immutable symlink; refusing a release without a rollback baseline." >&2
    return 1
  fi
  if [ ! -L "$runtime_current" ]; then
    echo "Managed runtime is not an immutable symlink; refusing a release without a rollback baseline." >&2
    return 1
  fi
  local runtime_target
  runtime_target="$(readlink "$runtime_current")"
  "$runtime_tool" active >/dev/null
}

preflight_candidate() {
  local commit="$1"
  command -v openssl >/dev/null 2>&1 || {
    echo "Required command is unavailable: openssl" >&2
    return 1
  }
  require_same_clean_head "$commit"
  verify_candidate_pair "$commit"
  require_rollback_baseline
  "$release_tool" preflight "$commit"
  echo "candidate_preflight=passed"
  echo "mutated=false"
}

acquire_candidate_lock() {
  install -d -m 700 "$state_dir"
  if ! mkdir "$candidate_lock" 2>/dev/null; then
    echo "Another Qiu Market candidate activation owns $candidate_lock." >&2
    return 1
  fi
  owns_lock=true
}

run_coordinated() {
  local operation="$1"
  local subject="$2"
  shift 2
  local context="$candidate_lock/context.env"
  local nonce
  local child_status=0
  case "$operation" in release-deploy|release-rollback|runtime-activate) ;;
    *) echo "Unsupported coordinated release operation: $operation" >&2; return 2 ;;
  esac
  if [ -z "$subject" ] || [[ "$subject" == *$'\n'* ]]; then
    echo "Coordinated release subject is unsafe." >&2
    return 2
  fi
  nonce="$(openssl rand -hex 32)"
  {
    printf 'schema_version=1\n'
    printf 'operation=%s\n' "$operation"
    printf 'subject=%s\n' "$subject"
    printf 'coordinator_pid=%s\n' "$$"
    printf 'expires_epoch=%s\n' "$(($(date '+%s') + 120))"
    printf 'nonce=%s\n' "$nonce"
  } > "$context"
  chmod 600 "$context"
  QIU_MARKET_COORDINATOR_CONTEXT="$context" \
    QIU_MARKET_COORDINATOR_TOKEN="$nonce" \
    "$@" || child_status=$?
  if [ -f "$context" ]; then
    echo "Coordinated child did not consume its one-time context." >&2
    return 1
  fi
  if [ "$child_status" -ne 0 ]; then
    return "$child_status"
  fi
}

write_candidate_state() {
  local phase="$1"
  local commit="$2"
  local previous_binary="$3"
  local previous_runtime="$4"
  local temporary="$candidate_state.next.$$"
  {
    printf 'schema_version=1\n'
    printf 'phase=%s\n' "$phase"
    printf 'candidate_commit=%s\n' "$commit"
    printf 'previous_binary=%s\n' "$previous_binary"
    printf 'previous_runtime=%s\n' "$previous_runtime"
    printf 'recorded_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  } > "$temporary"
  chmod 600 "$temporary"
  mv -f "$temporary" "$candidate_state"
}

activate_candidate() {
  local commit="$1"
  local previous_binary
  local previous_runtime
  local previous_runtime_commit
  preflight_candidate "$commit" >/dev/null
  acquire_candidate_lock
  previous_binary="$(readlink "$managed_binary")"
  previous_runtime="$(readlink "$runtime_current")"
  case "$previous_runtime" in
    "$support_dir"/runtime-releases/*) ;;
    *)
      echo "Previous runtime is outside the immutable runtime root." >&2
      return 1
      ;;
  esac
  previous_runtime_commit="$(basename "$previous_runtime")"
  "$runtime_tool" verify "$previous_runtime_commit" >/dev/null
  write_candidate_state activating "$commit" "$previous_binary" "$previous_runtime"

  run_coordinated release-deploy "$commit" \
    "$release_tool" deploy "$commit" --execute
  if ! run_coordinated runtime-activate "$commit" \
    "$runtime_tool" activate "$commit" --execute; then
    echo "Runtime activation failed; compensating with the verified previous binary and runtime." >&2
    if ! run_coordinated release-rollback "$previous_binary" \
      "$release_tool" rollback "$previous_binary" --execute; then
      write_candidate_state compensation-failed "$commit" "$previous_binary" "$previous_runtime"
      echo "CRITICAL: candidate activation failed and binary compensation failed; runtime compensation was not attempted." >&2
      return 1
    fi
    if ! run_coordinated runtime-activate "$previous_runtime_commit" \
      "$runtime_tool" activate "$previous_runtime_commit" --execute; then
      write_candidate_state compensation-failed "$commit" "$previous_binary" "$previous_runtime"
      echo "CRITICAL: candidate activation failed; binary compensation succeeded but previous runtime compensation failed." >&2
      return 1
    fi
    write_candidate_state rolled-back-after-runtime-failure "$commit" "$previous_binary" "$previous_runtime"
    return 1
  fi
  write_candidate_state active "$commit" "$previous_binary" "$previous_runtime"
  echo "Candidate activated with matching binary and runtime commit: $commit"
}

rollback_candidate() {
  local phase
  local commit
  local previous_binary
  local previous_runtime
  local previous_runtime_commit
  [ -f "$candidate_state" ] || {
    echo "No candidate activation state is available for rollback." >&2
    return 1
  }
  phase="$(manifest_value "$candidate_state" phase)"
  commit="$(manifest_value "$candidate_state" candidate_commit)"
  previous_binary="$(manifest_value "$candidate_state" previous_binary)"
  previous_runtime="$(manifest_value "$candidate_state" previous_runtime)"
  [ "$phase" = active ] || {
    echo "Candidate rollback requires phase=active; current=$phase" >&2
    return 1
  }
  case "$previous_binary" in
    "$support_dir"/releases/*/market-services) ;;
    *) echo "Recorded previous binary is outside the immutable release root." >&2; return 1 ;;
  esac
  case "$previous_runtime" in
    "$support_dir"/runtime-releases/*) ;;
    *) echo "Recorded previous runtime is outside the immutable runtime root." >&2; return 1 ;;
  esac
  previous_runtime_commit="$(basename "$previous_runtime")"
  acquire_candidate_lock
  "$runtime_tool" verify "$previous_runtime_commit" >/dev/null
  run_coordinated release-rollback "$previous_binary" \
    "$release_tool" rollback "$previous_binary" --execute
  if ! run_coordinated runtime-activate "$previous_runtime_commit" \
    "$runtime_tool" activate "$previous_runtime_commit" --execute; then
    write_candidate_state rollback-runtime-failed "$commit" "$previous_binary" "$previous_runtime"
    echo "CRITICAL: binary rollback succeeded but runtime rollback failed." >&2
    return 1
  fi
  write_candidate_state rolled-back "$commit" "$previous_binary" "$previous_runtime"
  echo "Candidate binary and runtime rolled back to their verified previous targets."
}

case "$action" in
  prepare)
    commit="$(resolve_commit "$revision")"
    require_same_clean_head "$commit"
    "$runtime_tool" prepare "$commit"
    "$release_tool" prepare "$commit"
    verify_candidate_pair "$commit"
    ;;
  verify)
    commit="$(resolve_commit "$revision")"
    require_same_clean_head "$commit"
    "$release_tool" verify "$commit"
    verify_candidate_pair "$commit"
    ;;
  preflight)
    commit="$(resolve_commit "$revision")"
    preflight_candidate "$commit"
    ;;
  activate)
    if [ "$execute_flag" != "--execute" ]; then
      echo "Activation is side-effecting; rerun with: $0 activate $revision --execute" >&2
      exit 2
    fi
    commit="$(resolve_commit "$revision")"
    activate_candidate "$commit"
    ;;
  rollback)
    if [ "$revision" != "--execute" ]; then
      echo "Rollback is side-effecting; rerun with: $0 rollback --execute" >&2
      exit 2
    fi
    rollback_candidate
    ;;
  status)
    if [ -f "$candidate_state" ]; then
      cat "$candidate_state"
    else
      echo "candidate_status=unmanaged"
    fi
    "$release_tool" status
    "$runtime_tool" status
    ;;
  *)
    echo "Usage: $0 prepare|verify|preflight <revision> | activate <revision> --execute | rollback --execute | status" >&2
    exit 2
    ;;
esac
