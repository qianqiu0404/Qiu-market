#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_dir="$(mktemp -d /tmp/qiu-market-transport-smoke.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

support_dir="$fixture_dir/support"
observation_dir="$support_dir/observations"
guardian_dir="$support_dir/guardian"
runtime_target="$support_dir/runtime-releases/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
runtime_link="$support_dir/runtime-current"
binary_release="$support_dir/releases/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
managed_binary="$support_dir/bin/market-services"
mkdir -p "$observation_dir" "$guardian_dir" "$runtime_target/ops/macos" \
  "$runtime_target/migrations" "$binary_release" "$(dirname "$managed_binary")" \
  "$support_dir/vercel-release"
printf 'runtime fixture\n' > "$runtime_target/ops/macos/fixture.sh"
printf 'migration fixture\n' > "$runtime_target/migrations/fixture.sql"
printf '#!/usr/bin/env bash\nexit 0\n' > "$binary_release/market-services"
chmod 700 "$binary_release/market-services"
runtime_bundle_sha256="$(
  cd "$runtime_target"
  find ops migrations -type f -print | LC_ALL=C sort |
    while IFS= read -r file; do shasum -a 256 "$file"; done |
    shasum -a 256 | awk '{print $1}'
)"
binary_sha256="$(shasum -a 256 "$binary_release/market-services" | awk '{print $1}')"
printf 'git_commit=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\nbundle_sha256=%s\n' \
  "$runtime_bundle_sha256" > "$runtime_target/runtime-manifest.env"
printf 'git_commit=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\nbinary_sha256=%s\n' \
  "$binary_sha256" > "$binary_release/manifest.env"
ln -s "$runtime_target" "$runtime_link"
ln -s "$binary_release/market-services" "$managed_binary"

deployment_id="dpl_TransportSmokeFixture"
deployment_url="https://qiu-market-transport-fixture.vercel.app"
deployment_commit="19928325f9a1104d1dd3505a004dffb9fe52a714"
production_origin="https://qiu-market.vercel.app"
gate_file="$support_dir/vercel-release/last.json"
latest_file="$observation_dir/latest.json"
history_file="$observation_dir/production-soak.jsonl"
manager="$repo_root/ops/macos/manage-transport-smoke.sh"
base_now=1785120000

jq -n \
  --arg id "$deployment_id" \
  --arg url "$deployment_url" \
  --arg commit "$deployment_commit" '
  {
    status:"production-gate-passed",
    candidate:{commit:$commit},
    promoted:{id:$id,url:$url}
  }
' > "$gate_file"

jq -n \
  --arg id "$deployment_id" \
  --arg url "$deployment_url" \
  --arg commit "$deployment_commit" \
  --arg runtime "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" \
  --arg binary_sha256 "$binary_sha256" \
  --arg runtime_bundle_sha256 "$runtime_bundle_sha256" \
  --arg production_origin "$production_origin" \
  --arg scheduled_at "$(jq -nr --argjson epoch "$base_now" '$epoch | todateiso8601')" '
  {
    schema_version:7,
    deployment_id:$id,
    deployment_url:$url,
    deployment_commit:$commit,
    runtime_release_commit:$runtime,
    binary_sha256:$binary_sha256,
    runtime_bundle_sha256:$runtime_bundle_sha256,
    production_origin:$production_origin,
    release_provenance:{recovery:{
      full_match:true,
      body:{
        production_origin:$production_origin,
        deployment_id:$id,
        deployment_url:$url,
        release_commit:$commit,
        source_digest:$binary_sha256
      }
    }},
    scheduled_at:$scheduled_at,
    current_checks_status:"passed",
    failure_scope:"none",
    checks:{
      tailscale_backend_state:"Running",
      tailscale_health_ok:true,
      tailscale_health:[],
      runtime_candidate_match:true,
      recovery_provenance_match:true,
      runtime_bundle_commit:$runtime,
      managed_binary_commit:$runtime,
      runtime_bundle_manifest_sha256:$runtime_bundle_sha256,
      runtime_bundle_sha256:$runtime_bundle_sha256,
      managed_binary_manifest_sha256:$binary_sha256,
      managed_binary_sha256:$binary_sha256,
      guardian_last_automatic_restart_at:0,
      network_interface:"en1",
      network_gateway:"192.0.2.1"
    }
  }
' > "$latest_file"

export QIU_MARKET_SUPPORT_DIR="$support_dir"
export QIU_MARKET_OBSERVATION_DIR="$observation_dir"
export QIU_MARKET_GUARDIAN_DIR="$guardian_dir"
export QIU_MARKET_RUNTIME_LINK="$runtime_link"
export QIU_MARKET_MANAGED_BINARY="$managed_binary"
export QIU_MARKET_PRODUCTION_GATE_FILE="$gate_file"
export QIU_MARKET_OBSERVER_LATEST="$latest_file"
export QIU_MARKET_SOAK_HISTORY="$history_file"
export QIU_MARKET_TRANSPORT_SMOKE_FILE="$observation_dir/transport-smoke.json"
export QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$observation_dir/acceptance-epoch.json"
export QIU_MARKET_SMOKE_NOW_EPOCH="$base_now"

jq '.release_provenance.recovery.body.source_digest =
  "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"' \
  "$latest_file" > "$latest_file.next"
mv "$latest_file.next" "$latest_file"
if "$manager" start >/dev/null 2>&1; then
  echo "Transport smoke incorrectly allowed recovery source digest drift." >&2
  exit 1
fi
jq --arg digest "$binary_sha256" \
  '.release_provenance.recovery.body.source_digest = $digest' \
  "$latest_file" > "$latest_file.next"
mv "$latest_file.next" "$latest_file"

printf 'tampered runtime\n' >> "$runtime_target/ops/macos/fixture.sh"
if "$manager" start >/dev/null 2>&1; then
  echo "Transport smoke incorrectly allowed a modified runtime bundle." >&2
  exit 1
fi
printf 'runtime fixture\n' > "$runtime_target/ops/macos/fixture.sh"

"$manager" start >/dev/null
started_epoch="$(jq -r '.started_at | fromdateiso8601' "$QIU_MARKET_TRANSPORT_SMOKE_FILE")"
last_epoch="$(jq -r '.last_scheduled_at | fromdateiso8601' "$QIU_MARKET_TRANSPORT_SMOKE_FILE")"

for offset in $(seq 0 29); do
  scheduled_epoch=$((started_epoch + offset * 60))
  jq -nc \
    --arg id "$deployment_id" \
    --arg url "$deployment_url" \
    --arg commit "$deployment_commit" \
    --arg runtime "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" \
    --arg binary_sha256 "$binary_sha256" \
    --arg runtime_bundle_sha256 "$runtime_bundle_sha256" \
    --arg production_origin "$production_origin" \
    --arg scheduled_at "$(jq -nr --argjson epoch "$scheduled_epoch" '$epoch | todateiso8601')" '
    {
      schema_version:7,
      deployment_id:$id,
      deployment_url:$url,
      deployment_commit:$commit,
      runtime_release_commit:$runtime,
      binary_sha256:$binary_sha256,
      runtime_bundle_sha256:$runtime_bundle_sha256,
      production_origin:$production_origin,
      release_provenance:{recovery:{
        full_match:true,
        body:{
          production_origin:$production_origin,
          deployment_id:$id,
          deployment_url:$url,
          release_commit:$commit,
          source_digest:$binary_sha256
        }
      }},
      scheduled_at:$scheduled_at,
      finished_at:$scheduled_at,
      current_checks_status:"passed",
      failure_scope:"none",
      checks:{
        tailscale_backend_state:"Running",
        tailscale_health_ok:true,
        tailscale_health:[],
        runtime_candidate_match:true,
        recovery_provenance_match:true,
        runtime_bundle_commit:$runtime,
        managed_binary_commit:$runtime,
        runtime_bundle_manifest_sha256:$runtime_bundle_sha256,
        runtime_bundle_sha256:$runtime_bundle_sha256,
        managed_binary_manifest_sha256:$binary_sha256,
        managed_binary_sha256:$binary_sha256,
        guardian_last_automatic_restart_at:0,
        network_interface:"en1",
        network_gateway:"192.0.2.1",
        trading_bff_http:200,
        recovery_bff_http:200,
        system_bff_http:200,
        uniswap_bff_http:200,
        pancakeswap_bff_http:200
      },
      latency_ms:{
        trading_bff:100,
        recovery_bff:100,
        system_bff:100,
        uniswap_bff:100,
        pancakeswap_bff:100
      }
    }
  ' >> "$history_file"
done

export QIU_MARKET_SMOKE_NOW_EPOCH="$last_epoch"
"$manager" finish >/dev/null
jq -e '
  .status == "passed" and
  .result.expected_minutes == 30 and
  .result.observed_minutes == 30 and
  .result.passed_minutes == 30 and
  ([.result.acceptance[]] | all)
' "$QIU_MARKET_TRANSPORT_SMOKE_FILE" >/dev/null
"$manager" status | jq -e '
  .status == "passed" and
  .terminal_state == "passed" and
  .completed_at != null and
  ([.acceptance[]] | all)
' >/dev/null

echo "Qiu Market 30-minute transport smoke fixtures passed."
