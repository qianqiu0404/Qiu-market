#!/bin/bash
set -euo pipefail
umask 077

qiu_release_runtime="${QIU_MARKET_LIVE_RUNTIME:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
qiu_active_release_file="$qiu_release_runtime/config/active-release.json"

release_selector_fail() { printf 'Qiu Market live release selector failed: %s\n' "$1" >&2; return 1; }
release_private_file() { [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -f '%u:%Lp' "$1" 2>/dev/null || true)" = "$(id -u):600" ]; }
release_private_exec() { [ -f "$1" ] && [ ! -L "$1" ] && [ -x "$1" ] && [ "$(stat -f '%u:%Lp' "$1" 2>/dev/null || true)" = "$(id -u):700" ]; }

validate_release_selector() {
  local selector="$1" runtime_prefix="$qiu_release_runtime/"
  local commit deployment origin source_path config_path binary_path binary_sha gate_path gate_sha
  local attestation_path attestation_sha frontdoor_path frontdoor_sha redis_pidfile
  release_private_file "$selector" || release_selector_fail 'selector ACL or type is invalid'
  jq -e '
    (keys|sort)==[
      "attestation_path","attestation_sha256","binary_path","binary_sha256","commit",
      "config_path","contract_schema","data_mode","deployment_id","edge_schema",
      "frontdoor_binary_path","frontdoor_port","frontdoor_sha256","gate_path","gate_sha256",
      "generation_id","generation_owner_token","origin","provider_policy","redis_generation",
      "redis_owner_token","redis_pidfile","schema_version","selected_at","snapshot_schema",
      "source_path","tunnel_target"
    ] and
    .schema_version=="qiu.d1.active-release.v2" and
    (.commit|type=="string" and test("^[0-9a-f]{40}$")) and
    .data_mode=="live" and .provider_policy=="restricted-no-bypass.v1" and
    .contract_schema=="qiu.market-read-contract.v1" and
    .snapshot_schema=="qiu.market-snapshot.v1" and .edge_schema=="qiu.market-edge-contract.v1" and
    .frontdoor_port==18084 and .tunnel_target=="http://127.0.0.1:18084" and
    (.generation_id|type=="string" and test("^[A-Za-z0-9._:-]{1,128}$")) and
    (.generation_owner_token|type=="string" and test("^[0-9a-f]{32,64}$")) and
    (.redis_generation|type=="string" and test("^[A-Za-z0-9._:-]{1,128}$")) and
    (.redis_owner_token|type=="string" and test("^[0-9a-f]{32,64}$")) and
    (.deployment_id|type=="string" and test("^dpl_[A-Za-z0-9]{20,}$")) and
    (.origin|type=="string" and test("^https://[a-z0-9][a-z0-9.-]*[.]vercel[.]app$")) and
    (.source_path|type=="string" and length>0) and (.config_path|type=="string" and length>0) and
    (.binary_path|type=="string" and length>0) and (.frontdoor_binary_path|type=="string" and length>0) and
    (.gate_path|type=="string" and length>0) and (.attestation_path|type=="string" and length>0) and
    (.redis_pidfile|type=="string" and length>0) and
    (.binary_sha256|test("^[0-9a-f]{64}$")) and (.frontdoor_sha256|test("^[0-9a-f]{64}$")) and
    (.gate_sha256|test("^[0-9a-f]{64}$")) and (.attestation_sha256|test("^[0-9a-f]{64}$")) and
    (.selected_at|type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
  ' "$selector" >/dev/null || release_selector_fail 'selector schema is invalid'
  commit="$(jq -r '.commit' "$selector")"; deployment="$(jq -r '.deployment_id' "$selector")"; origin="$(jq -r '.origin' "$selector")"
  source_path="$(jq -r '.source_path' "$selector")"; config_path="$(jq -r '.config_path' "$selector")"
  binary_path="$(jq -r '.binary_path' "$selector")"; binary_sha="$(jq -r '.binary_sha256' "$selector")"
  frontdoor_path="$(jq -r '.frontdoor_binary_path' "$selector")"; frontdoor_sha="$(jq -r '.frontdoor_sha256' "$selector")"
  gate_path="$(jq -r '.gate_path' "$selector")"; gate_sha="$(jq -r '.gate_sha256' "$selector")"
  attestation_path="$(jq -r '.attestation_path' "$selector")"; attestation_sha="$(jq -r '.attestation_sha256' "$selector")"
  redis_pidfile="$(jq -r '.redis_pidfile' "$selector")"
  case "$config_path/" in "$runtime_prefix"releases/"$commit"/config/) ;; *) release_selector_fail 'config path is outside exact release' ;; esac
  case "$binary_path" in "$runtime_prefix"bin/market-services.*) ;; *) release_selector_fail 'market binary is outside runtime' ;; esac
  case "$frontdoor_path" in "$runtime_prefix"bin/market-frontdoor.*) ;; *) release_selector_fail 'frontdoor binary is outside runtime' ;; esac
  case "$gate_path" in "$runtime_prefix"ops/r1/preview-edge-gate.*) ;; *) release_selector_fail 'gate path is outside runtime' ;; esac
  [ "$attestation_path" = "$config_path/preview-deployment-attestation.json" ] || release_selector_fail 'attestation path is outside release config'
  [ "$redis_pidfile" = "$qiu_release_runtime/run/redis.pid" ] || release_selector_fail 'redis pidfile is outside runtime'
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    case "$source_path/" in "$qiu_release_runtime"/fixture-source/*/) ;; *) release_selector_fail 'fixture source path is outside fixture runtime' ;; esac
  else
    case "$source_path" in /Users/*/Documents/Codex/*/work/*) ;; *) release_selector_fail 'source path is outside prepared Codex worktrees' ;; esac
  fi
  [ -d "$config_path" ] && [ ! -L "$config_path" ] && [ "$(stat -f '%u:%Lp' "$config_path")" = "$(id -u):700" ] || release_selector_fail 'release config ACL is invalid'
  release_private_exec "$binary_path" || release_selector_fail 'market binary ACL or type is invalid'
  release_private_exec "$frontdoor_path" || release_selector_fail 'frontdoor binary ACL or type is invalid'
  release_private_exec "$gate_path" || release_selector_fail 'gate ACL or type is invalid'
  release_private_file "$attestation_path" || release_selector_fail 'attestation ACL or type is invalid'
  [ "$(shasum -a 256 "$binary_path" | awk '{print $1}')" = "$binary_sha" ] || release_selector_fail 'market binary digest mismatch'
  [ "$(shasum -a 256 "$frontdoor_path" | awk '{print $1}')" = "$frontdoor_sha" ] || release_selector_fail 'frontdoor binary digest mismatch'
  [ "$(shasum -a 256 "$gate_path" | awk '{print $1}')" = "$gate_sha" ] || release_selector_fail 'gate digest mismatch'
  [ "$(shasum -a 256 "$attestation_path" | awk '{print $1}')" = "$attestation_sha" ] || release_selector_fail 'attestation digest mismatch'
  for pair in source-commit:"$commit" preview-deployment-id:"$deployment" allowed-origin:"$origin"; do
    local expected="${pair#*:}" item="$config_path/${pair%%:*}"
    release_private_file "$item" || release_selector_fail 'release config file ACL is invalid'
    [ "$(<"$item")" = "$expected" ] || release_selector_fail 'release config value mismatch'
  done
  jq -e --arg deployment "$deployment" --arg origin "$origin" --arg commit "$commit" '
    .schema_version=="qiu.preview-edge.deployment-attestation.v1" and
    .deployment_id==$deployment and .immutable_url==$origin and .state=="READY" and .target==null and
    .git_commit==$commit and .source_commit==$commit and .release_commit==$commit
  ' "$attestation_path" >/dev/null || release_selector_fail 'attestation identity mismatch'
}

load_active_release() {
  validate_release_selector "$qiu_active_release_file"
  active_release_commit="$(jq -r '.commit' "$qiu_active_release_file")"
  active_release_deployment_id="$(jq -r '.deployment_id' "$qiu_active_release_file")"
  active_release_origin="$(jq -r '.origin' "$qiu_active_release_file")"
  active_release_source_path="$(jq -r '.source_path' "$qiu_active_release_file")"
  active_release_config_path="$(jq -r '.config_path' "$qiu_active_release_file")"
  active_release_binary_path="$(jq -r '.binary_path' "$qiu_active_release_file")"
  active_release_binary_sha256="$(jq -r '.binary_sha256' "$qiu_active_release_file")"
  active_release_frontdoor_binary_path="$(jq -r '.frontdoor_binary_path' "$qiu_active_release_file")"
  active_release_frontdoor_sha256="$(jq -r '.frontdoor_sha256' "$qiu_active_release_file")"
  active_release_gate_path="$(jq -r '.gate_path' "$qiu_active_release_file")"
  active_release_gate_sha256="$(jq -r '.gate_sha256' "$qiu_active_release_file")"
  active_release_attestation_path="$(jq -r '.attestation_path' "$qiu_active_release_file")"
  active_release_attestation_sha256="$(jq -r '.attestation_sha256' "$qiu_active_release_file")"
  active_release_generation_id="$(jq -r '.generation_id' "$qiu_active_release_file")"
  active_release_generation_owner_token="$(jq -r '.generation_owner_token' "$qiu_active_release_file")"
  active_release_data_mode="$(jq -r '.data_mode' "$qiu_active_release_file")"
}

atomic_select_release() {
  local candidate="$1" temporary="$qiu_release_runtime/config/active-release.$$.tmp"
  validate_release_selector "$candidate"
  install -m 600 "$candidate" "$temporary"
  mv "$temporary" "$qiu_active_release_file"
  load_active_release
  [ "$(jq -r '.commit' "$qiu_active_release_file")" = "$(jq -r '.commit' "$candidate")" ] || release_selector_fail 'atomic selector verification failed'
}
