#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_dir="$(mktemp -d /tmp/qiu-market-slo-test.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

epoch_id="qiu-market-fixture-epoch"
deployment_id="dpl_FixtureRelease123"
deployment_url="https://qiu-market-fixture-release.vercel.app"
deployment_commit="19928325f9a1104d1dd3505a004dffb9fe52a714"
production_origin="https://qiu-market.vercel.app"
window_start=1785196800
window_last_slot=$((window_start + 7 * 24 * 60 * 60 - 60))
epoch_file="$fixture_dir/acceptance-epoch.json"
healthy="$fixture_dir/healthy.jsonl"
with_gap="$fixture_dir/with-gap.jsonl"
with_bad_duplicates="$fixture_dir/with-bad-duplicates.jsonl"
with_changed_canary="$fixture_dir/with-changed-canary.jsonl"

jq -n \
  --arg epoch_id "$epoch_id" \
  --arg production_origin "$production_origin" \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$deployment_commit" \
  --argjson start "$window_start" '{
    schema_version: 2,
    epoch_id: $epoch_id,
    status: "active",
    production_origin: $production_origin,
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    dex_canaries: {
      uniswap: {
        asset_guid: "11111111-1111-4111-8111-111111111111",
        route_key: "fixture-uniswap-route",
        quote_notional_usd: "10000",
        selected_at: ($start | todateiso8601)
      },
      pancakeswap: {
        asset_guid: "22222222-2222-4222-8222-222222222222",
        route_key: "fixture-pancakeswap-route",
        quote_notional_usd: "10000",
        selected_at: ($start | todateiso8601)
      }
    },
    created_at: (($start - 30) | todateiso8601),
    started_at: ($start | todateiso8601),
    stopped_at: null
  }' > "$epoch_file"

jq -nc \
  --arg epoch_id "$epoch_id" \
  --arg production_origin "$production_origin" \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$deployment_commit" \
  --argjson start "$window_start" \
  --argjson end "$window_last_slot" '
  def canary($provider):
    if $provider == "uniswap" then {
      asset_guid: "11111111-1111-4111-8111-111111111111",
      route_key: "fixture-uniswap-route",
      quote_notional_usd: "10000",
      selected_at: ($start | todateiso8601)
    } else {
      asset_guid: "22222222-2222-4222-8222-222222222222",
      route_key: "fixture-pancakeswap-route",
      quote_notional_usd: "10000",
      selected_at: ($start | todateiso8601)
    } end;
  range($start; $end + 1; 60) as $at |
  {
    schema_version: 4,
    acceptance_epoch_id: $epoch_id,
    acceptance_eligible: true,
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    production_origin: $production_origin,
    scheduled_at: ($at | todateiso8601),
    started_at: (($at + 1) | todateiso8601),
    finished_at: (($at + 10) | todateiso8601),
    observed_at: (($at + 10) | todateiso8601),
    duration_ms: 9000,
    current_checks_status: "passed",
    checks: {
      trading_bff_http: 200,
      system_bff_http: 200,
      uniswap_bff_http: 200,
      pancakeswap_bff_http: 200,
      disk_free_bytes: (30 * 1024 * 1024 * 1024)
    },
    latency_ms: {
      trading_bff: 100,
      system_bff: 100,
      uniswap_bff: 100,
      pancakeswap_bff: 100
    },
    historical_windows: [
      [24, 48, 72][] as $hours |
      ["pancakeswap", "uniswap"][] as $provider |
      {
        hours: $hours,
        provider: $provider,
        mode: "fixed_epoch_canary",
        status: "passed",
        canary: canary($provider)
      }
    ]
  }
' > "$healthy"

# Contamination must never count: an old schema, the same epoch with a wrong
# commit, and a different epoch all sit inside the same JSONL history.
jq -nc \
  --arg epoch_id "$epoch_id" \
  --arg production_origin "$production_origin" \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$deployment_commit" \
  --argjson start "$window_start" '
  {
    schema_version: 3,
    acceptance_epoch_id: $epoch_id,
    acceptance_eligible: true,
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    production_origin: $production_origin,
    scheduled_at: ($start | todateiso8601),
    current_checks_status: "passed"
  },
  {
    schema_version: 4,
    acceptance_epoch_id: $epoch_id,
    acceptance_eligible: true,
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: "0000000000000000000000000000000000000000",
    production_origin: $production_origin,
    scheduled_at: ($start | todateiso8601),
    current_checks_status: "passed"
  },
  {
    schema_version: 4,
    acceptance_epoch_id: "qiu-market-other-epoch",
    acceptance_eligible: true,
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    production_origin: $production_origin,
    scheduled_at: ($start | todateiso8601),
    current_checks_status: "passed"
  }
' >> "$healthy"

healthy_report="$(
  QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$epoch_file" \
  QIU_MARKET_SOAK_HISTORY="$healthy" \
  QIU_MARKET_SLO_NOW_EPOCH="$window_last_slot" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "production-recommendation" and
  .acceptance_epoch_id == "qiu-market-fixture-epoch" and
  .deployment_commit == "19928325f9a1104d1dd3505a004dffb9fe52a714" and
  .raw_eligible_samples == 10080 and
  .rejected_epoch_samples == 2 and
  .observed_minutes == 10080 and
  .missing_minutes == 0 and
  .availability_percent == 100 and
  .acceptance.dex_fixed_24_48_72_windows_passed == true and
  ([.acceptance[]] | all)
' <<<"$healthy_report" >/dev/null

partial_report="$(
  QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$epoch_file" \
  QIU_MARKET_SOAK_HISTORY="$healthy" \
  QIU_MARKET_SLO_NOW_EPOCH="$((window_start + 119 * 60))" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "environment-pending" and
  .expected_minutes == 120 and
  .observed_minutes == 120 and
  .acceptance.full_7d_observation_window == false
' <<<"$partial_report" >/dev/null

awk 'NR <= 300 || NR > 360' "$healthy" > "$with_gap"
gap_report="$(
  QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$epoch_file" \
  QIU_MARKET_SOAK_HISTORY="$with_gap" \
  QIU_MARKET_SLO_NOW_EPOCH="$window_last_slot" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "failed" and
  .missing_minutes == 60 and
  .availability_percent < 99.5 and
  .longest_observed_failure_seconds >= 3600
' <<<"$gap_report" >/dev/null

cp "$healthy" "$with_bad_duplicates"
jq -nc \
  --arg epoch_id "$epoch_id" \
  --arg production_origin "$production_origin" \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$deployment_commit" \
  --argjson start "$window_start" '
  range(300; 306) as $offset |
  ($start + ($offset * 60)) as $at |
  {
    schema_version: 4,
    acceptance_epoch_id: $epoch_id,
    acceptance_eligible: true,
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    production_origin: $production_origin,
    scheduled_at: ($at | todateiso8601),
    started_at: (($at + 20) | todateiso8601),
    finished_at: (($at + 30) | todateiso8601),
    observed_at: (($at + 30) | todateiso8601),
    duration_ms: 10000,
    current_checks_status: "failed",
    checks: {
      trading_bff_http: 504,
      system_bff_http: 200,
      uniswap_bff_http: 200,
      pancakeswap_bff_http: 200,
      disk_free_bytes: (30 * 1024 * 1024 * 1024)
    },
    latency_ms: {
      trading_bff: 8000,
      system_bff: 100,
      uniswap_bff: 100,
      pancakeswap_bff: 100
    }
  }
' >> "$with_bad_duplicates"
duplicate_report="$(
  QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$epoch_file" \
  QIU_MARKET_SOAK_HISTORY="$with_bad_duplicates" \
  QIU_MARKET_SLO_NOW_EPOCH="$window_last_slot" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "failed" and
  .duplicate_samples == 6 and
  .observed_minutes == 10080 and
  .availability_percent < 100 and
  .longest_observed_failure_seconds == 360 and
  .acceptance.no_interruption_over_5m == false
' <<<"$duplicate_report" >/dev/null

window_last_slot_iso="$(
  jq -nr --argjson epoch "$window_last_slot" '$epoch | todateiso8601'
)"
jq -c --arg scheduled_at "$window_last_slot_iso" '
  if .schema_version == 4 and
    .acceptance_epoch_id == "qiu-market-fixture-epoch" and
    .scheduled_at == $scheduled_at
  then .historical_windows[0].canary.route_key = "changed-route"
  else .
  end
' "$healthy" > "$with_changed_canary"
changed_canary_report="$(
  QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$epoch_file" \
  QIU_MARKET_SOAK_HISTORY="$with_changed_canary" \
  QIU_MARKET_SLO_NOW_EPOCH="$window_last_slot" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "failed" and
  .acceptance.dex_fixed_24_48_72_windows_passed == false
' <<<"$changed_canary_report" >/dev/null

missing_epoch_report="$(
  QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$fixture_dir/not-created.json" \
  QIU_MARKET_SOAK_HISTORY="$healthy" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "environment-pending" and
  .reason == "acceptance_epoch_not_started"
' <<<"$missing_epoch_report" >/dev/null

echo "Qiu Market acceptance-epoch SLO fixtures passed."
