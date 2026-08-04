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
mkdir -p "$observation_dir" "$guardian_dir" "$runtime_target" "$support_dir/vercel-release"
printf 'git_commit=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' > "$runtime_target/runtime-manifest.env"
ln -s "$runtime_target" "$runtime_link"

deployment_id="dpl_TransportSmokeFixture"
deployment_url="https://qiu-market-transport-fixture.vercel.app"
deployment_commit="19928325f9a1104d1dd3505a004dffb9fe52a714"
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
  --arg scheduled_at "$(jq -nr --argjson epoch "$base_now" '$epoch | todateiso8601')" '
  {
    schema_version:4,
    deployment_id:$id,
    deployment_url:$url,
    deployment_commit:$commit,
    runtime_release_commit:$runtime,
    scheduled_at:$scheduled_at,
    current_checks_status:"passed",
    failure_scope:"none",
    checks:{
      tailscale_backend_state:"Running",
      tailscale_health_ok:true,
      tailscale_health:[],
      guardian_last_automatic_restart_at:0
    }
  }
' > "$latest_file"

export QIU_MARKET_SUPPORT_DIR="$support_dir"
export QIU_MARKET_OBSERVATION_DIR="$observation_dir"
export QIU_MARKET_GUARDIAN_DIR="$guardian_dir"
export QIU_MARKET_RUNTIME_LINK="$runtime_link"
export QIU_MARKET_PRODUCTION_GATE_FILE="$gate_file"
export QIU_MARKET_OBSERVER_LATEST="$latest_file"
export QIU_MARKET_SOAK_HISTORY="$history_file"
export QIU_MARKET_TRANSPORT_SMOKE_FILE="$observation_dir/transport-smoke.json"
export QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$observation_dir/acceptance-epoch.json"
export QIU_MARKET_SMOKE_NOW_EPOCH="$base_now"

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
    --arg scheduled_at "$(jq -nr --argjson epoch "$scheduled_epoch" '$epoch | todateiso8601')" '
    {
      schema_version:4,
      deployment_id:$id,
      deployment_url:$url,
      deployment_commit:$commit,
      runtime_release_commit:$runtime,
      scheduled_at:$scheduled_at,
      finished_at:$scheduled_at,
      current_checks_status:"passed",
      failure_scope:"none",
      checks:{
        tailscale_backend_state:"Running",
        tailscale_health_ok:true,
        tailscale_health:[],
        guardian_last_automatic_restart_at:0,
        trading_bff_http:200,
        system_bff_http:200,
        uniswap_bff_http:200,
        pancakeswap_bff_http:200
      },
      latency_ms:{
        trading_bff:100,
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

echo "Qiu Market 30-minute transport smoke fixtures passed."
