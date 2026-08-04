#!/usr/bin/env bash
set -euo pipefail
umask 077

action="${1:-status}"
revision="${2:-HEAD}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
support_dir="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
release_root="$support_dir/runtime-releases"
current_link="$support_dir/runtime-current"
launch_dir="$HOME/Library/LaunchAgents"
database_env="$support_dir/database.env"
production_env="$support_dir/production.env"
labels=(trading api crawler worker dex tailscaled guardian backup.full backup.trading restore-drill observer)
runtime_paths=(
  migrations
  ops/macos/backup-production.sh
  ops/macos/com.qiumarket.backup.plist.template
  ops/macos/com.qiumarket.guardian.plist.template
  ops/macos/com.qiumarket.observer.plist.template
  ops/macos/com.qiumarket.restore-drill.plist.template
  ops/macos/com.qiumarket.role.plist.template
  ops/macos/com.qiumarket.tailscaled.plist.template
  ops/macos/guardian.sh
  ops/macos/manage-funnel.sh
  ops/macos/manage-observer.sh
  ops/macos/manage-acceptance-epoch.sh
  ops/macos/manage-runtime-release.sh
  ops/macos/manage-services.sh
  ops/macos/manage-transport-smoke.sh
  ops/macos/manage-user-resilience.sh
  ops/macos/observe-production.sh
  ops/macos/production-lib.sh
  ops/macos/proxy-env.sh
  ops/macos/restore-drill.sh
  ops/macos/run-role.sh
  ops/macos/run-tailscaled.sh
  ops/macos/verify-runtime.sh
)

bundle_hash() {
  local root="$1"
  (
    cd "$root"
    find ops migrations -type f -print | LC_ALL=C sort |
      while IFS= read -r file; do
        shasum -a 256 "$file"
      done |
      shasum -a 256 |
      awk '{print $1}'
  )
}

manifest_value() {
  local manifest="$1"
  local key="$2"
  sed -n "s/^${key}=//p" "$manifest" | head -1
}

verify_bundle() {
  local root="$1"
  local manifest="$root/runtime-manifest.env"
  [ -d "$root/ops/macos" ] && [ -d "$root/migrations" ] && [ -f "$manifest" ] || return 1
  [ "$(manifest_value "$manifest" bundle_sha256)" = "$(bundle_hash "$root")" ] || return 1
  [ "$(manifest_value "$manifest" git_commit)" = "$(basename "$root")" ] || return 1
}

private_file_ok() {
  local file="$1"
  local mode
  [ -f "$file" ] || return 1
  mode="$(stat -f '%Lp' "$file")"
  [ "$mode" = 600 ] || [ "$mode" = 400 ]
}

prepare_bundle() {
  local commit
  local target
  local temporary
  local sha
  commit="$(git -C "$repo_root" rev-parse --verify "$revision^{commit}")"
  target="$release_root/$commit"
  install -d -m 700 "$release_root"
  if [ -d "$target" ]; then
    verify_bundle "$target"
    echo "Immutable runtime release already verified: $target"
    return
  fi
  temporary="$(mktemp -d "$release_root/.prepare.XXXXXX")"
  cleanup_prepare() {
    if [ -d "$temporary" ]; then
      find "$temporary" -depth -delete 2>/dev/null || true
    fi
  }
  trap cleanup_prepare RETURN
  git -C "$repo_root" archive "$commit" -- "${runtime_paths[@]}" | tar -xf - -C "$temporary"
  find "$temporary" -type d -exec chmod 700 {} +
  find "$temporary" -type f -exec chmod 600 {} +
  find "$temporary/ops/macos" -type f -name '*.sh' -exec chmod 700 {} +
  sha="$(bundle_hash "$temporary")"
  {
    printf 'git_commit=%s\n' "$commit"
    printf 'bundle_sha256=%s\n' "$sha"
    printf 'prepared_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  } > "$temporary/runtime-manifest.env"
  chmod 600 "$temporary/runtime-manifest.env"
  mv "$temporary" "$target"
  trap - RETURN
  verify_bundle "$target"
  echo "Prepared immutable runtime release: $target"
}

backup_plists() {
  local backup_dir="$1"
  local plist
  install -d -m 700 "$backup_dir"
  for plist in "$launch_dir"/com.qiumarket*.plist; do
    [ -f "$plist" ] || continue
    install -m 600 "$plist" "$backup_dir/$(basename "$plist")"
  done
}

restore_plists() {
  local backup_dir="$1"
  local plist
  local label
  for plist in "$backup_dir"/com.qiumarket*.plist; do
    [ -f "$plist" ] || continue
    label="$(basename "$plist" .plist)"
    install -m 600 "$plist" "$launch_dir/$label.plist"
    launchctl bootout "gui/$UID/$label" >/dev/null 2>&1 || true
    launchctl bootstrap "gui/$UID" "$launch_dir/$label.plist" >/dev/null 2>&1 || true
  done
}

activate_bundle() {
  local commit
  local target
  local plist_backup
  local activation_status
  commit="$(git -C "$repo_root" rev-parse --verify "$revision^{commit}")"
  target="$release_root/$commit"
  verify_bundle "$target" || {
    echo "Runtime bundle is missing or failed checksum verification: $target" >&2
    return 1
  }
  private_file_ok "$database_env" || {
    echo "Private database environment is missing or not mode 0600/0400: $database_env" >&2
    return 1
  }
  private_file_ok "$production_env" || {
    echo "Private production environment is missing or not mode 0600/0400: $production_env" >&2
    return 1
  }
  plist_backup="$release_root/plist-backup-$(date -u '+%Y%m%dT%H%M%SZ')-$$"
  backup_plists "$plist_backup"
  set +e
  (
    set -e
    QIU_MARKET_RUNTIME_ROOT="$target" "$target/ops/macos/manage-services.sh" reload
    QIU_MARKET_RUNTIME_ROOT="$target" "$target/ops/macos/manage-funnel.sh" install-daemon
    QIU_MARKET_RUNTIME_ROOT="$target" "$target/ops/macos/manage-funnel.sh" start
    QIU_MARKET_RUNTIME_ROOT="$target" "$target/ops/macos/manage-user-resilience.sh" install
    QIU_MARKET_RUNTIME_ROOT="$target" "$target/ops/macos/manage-observer.sh" install
  )
  activation_status=$?
  set -e
  if [ "$activation_status" -ne 0 ]; then
    echo "Runtime activation failed; restoring previous LaunchAgent definitions." >&2
    restore_plists "$plist_backup"
    return 1
  fi
  launchctl bootout "gui/$UID/com.qiumarket.dw" >/dev/null 2>&1 || true
  if [ -f "$launch_dir/com.qiumarket.dw.plist" ]; then
    install -m 600 "$launch_dir/com.qiumarket.dw.plist" "$plist_backup/com.qiumarket.dw.disabled.plist"
    find "$launch_dir/com.qiumarket.dw.plist" -maxdepth 0 -type f -delete
  fi
  for label in "${labels[@]}"; do
    launchctl print "gui/$UID/com.qiumarket.$label" >/dev/null 2>&1 || {
      echo "Managed runtime role is not loaded: $label" >&2
      restore_plists "$plist_backup"
      return 1
    }
    if ! launchctl print "gui/$UID/com.qiumarket.$label" 2>/dev/null |
      grep -F "$target" >/dev/null; then
      echo "Loaded runtime role does not reference the immutable bundle: $label" >&2
      restore_plists "$plist_backup"
      return 1
    fi
    if ! plutil -p "$launch_dir/com.qiumarket.$label.plist" | grep -F "$target" >/dev/null; then
      echo "Managed runtime role does not reference the immutable bundle: $label" >&2
      restore_plists "$plist_backup"
      return 1
    fi
  done
  ln -sfn "$target" "$current_link"
  echo "Activated immutable runtime release: $target"
  echo "Previous LaunchAgent definitions preserved at: $plist_backup"
}

case "$action" in
  prepare)
    prepare_bundle
    ;;
  activate)
    activate_bundle
    ;;
  status)
    if [ -L "$current_link" ]; then
      target="$(readlink "$current_link")"
      if verify_bundle "$target"; then
        echo "runtime_status=verified"
      else
        echo "runtime_status=invalid"
      fi
      echo "runtime_target=$target"
    else
      echo "runtime_status=unmanaged"
    fi
    ;;
  *)
    echo "Usage: $0 prepare|activate|status [revision]" >&2
    exit 2
    ;;
esac
