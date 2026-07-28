#!/usr/bin/env bash
set -euo pipefail

support_dir="$HOME/Library/Application Support/Qiu Market"
observation_dir="${QIU_MARKET_OBSERVATION_DIR:-$support_dir/observations}"
history_file="${QIU_MARKET_SOAK_HISTORY:-$observation_dir/production-soak.jsonl}"
epoch_file="${QIU_MARKET_ACCEPTANCE_EPOCH_FILE:-$observation_dir/acceptance-epoch.json}"

pending_report() {
  local reason="$1"
  jq -n --arg reason "$reason" '{
    window: "acceptance_epoch_7d",
    status: "environment-pending",
    reason: $reason,
    acceptance: {
      full_7d_observation_window: false
    }
  }'
}

if [ ! -s "$epoch_file" ]; then
  pending_report acceptance_epoch_not_started
  exit 0
fi
if ! jq -e '
  . as $root |
  .schema_version == 2 and
  (.status == "active" or .status == "stopped") and
  (.epoch_id | type == "string" and length >= 8) and
  (.production_origin | type == "string" and startswith("https://")) and
  (.deployment_id | type == "string" and startswith("dpl_")) and
  (.deployment_url | type == "string" and startswith("https://")) and
  (.deployment_commit | type == "string" and test("^[0-9a-f]{40}$")) and
  (.started_at | type == "string") and
  (.started_at | try fromdateiso8601 catch null | type == "number") and
  (.dex_canaries | keys | sort) == ["pancakeswap", "uniswap"] and
  all(.dex_canaries[];
    (.asset_guid | type == "string") and
    (.route_key | type == "string" and length > 0) and
    (.quote_notional_usd | type == "string") and
    (.selected_at == $root.started_at)
  )
' "$epoch_file" >/dev/null 2>&1; then
  echo "The Qiu Market acceptance epoch file is invalid." >&2
  exit 1
fi
if [ ! -s "$history_file" ]; then
  pending_report acceptance_samples_not_started
  exit 0
fi

epoch_id="$(jq -r '.epoch_id' "$epoch_file")"
epoch_status="$(jq -r '.status' "$epoch_file")"
production_origin="$(jq -r '.production_origin' "$epoch_file")"
deployment_id="$(jq -r '.deployment_id' "$epoch_file")"
deployment_url="$(jq -r '.deployment_url' "$epoch_file")"
deployment_commit="$(jq -r '.deployment_commit' "$epoch_file")"
dex_canaries="$(jq -c '.dex_canaries' "$epoch_file")"
window_start_epoch="$(jq -r '.started_at | fromdateiso8601' "$epoch_file")"
window_last_slot_epoch=$((window_start_epoch + 7 * 24 * 60 * 60 - 60))
now_epoch="${QIU_MARKET_SLO_NOW_EPOCH:-$(date -u '+%s')}"
if [[ ! "$now_epoch" =~ ^[0-9]+$ ]]; then
  echo "QIU_MARKET_SLO_NOW_EPOCH must be a Unix timestamp." >&2
  exit 1
fi
now_epoch=$((now_epoch - now_epoch % 60))
evaluation_end_epoch="$now_epoch"
if [ "$evaluation_end_epoch" -gt "$window_last_slot_epoch" ]; then
  evaluation_end_epoch="$window_last_slot_epoch"
fi
expected_minutes=0
if [ "$evaluation_end_epoch" -ge "$window_start_epoch" ]; then
  expected_minutes=$(((evaluation_end_epoch - window_start_epoch) / 60 + 1))
fi

jq -s \
  --arg epoch_id "$epoch_id" \
  --arg epoch_status "$epoch_status" \
  --arg production_origin "$production_origin" \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$deployment_commit" \
  --argjson dex_canaries "$dex_canaries" \
  --argjson window_start "$window_start_epoch" \
  --argjson evaluation_end "$evaluation_end_epoch" \
  --argjson window_last_slot "$window_last_slot_epoch" \
  --argjson now_epoch "$now_epoch" \
  --argjson expected "$expected_minutes" '
  def p95($values):
    ($values | sort) as $sorted |
    if ($sorted | length) == 0 then null
    else $sorted[((($sorted | length) - 1) * 0.95 | floor)]
    end;
  def rest_probes($sample):
    [
      {
        http: ($sample.checks.trading_bff_http // 0),
        latency: ($sample.latency_ms.trading_bff // null)
      },
      {
        http: ($sample.checks.system_bff_http // 0),
        latency: ($sample.latency_ms.system_bff // null)
      },
      {
        http: ($sample.checks.uniswap_bff_http // 0),
        latency: ($sample.latency_ms.uniswap_bff // null)
      },
      {
        http: ($sample.checks.pancakeswap_bff_http // 0),
        latency: ($sample.latency_ms.pancakeswap_bff // null)
      }
    ] | map(select(.http != 0 or .latency != null));
  def same_release:
    .schema_version == 4 and
    .acceptance_eligible == true and
    .acceptance_epoch_id == $epoch_id and
    .production_origin == $production_origin and
    .deployment_id == $deployment_id and
    .deployment_url == $deployment_url and
    .deployment_commit == $deployment_commit and
    (.scheduled_at | type == "string");

  . as $all |
  ([
    $all[] |
    select(same_release) |
    select(
      ((.scheduled_at | fromdateiso8601) >= $window_start) and
      ((.scheduled_at | fromdateiso8601) <= $evaluation_end)
    )
  ] | sort_by(.scheduled_at, .finished_at)) as $samples |
  ([
    $all[] |
    select(.acceptance_epoch_id == $epoch_id and (same_release | not))
  ] | length) as $rejectedEpochSamples |
  ($samples | group_by(.scheduled_at) | map({
    scheduled_at: .[0].scheduled_at,
    samples: .,
    passed: all(.current_checks_status == "passed")
  })) as $slotGroups |
  (reduce $slotGroups[] as $slot ({};
    .[($slot.scheduled_at | fromdateiso8601 | tostring)] = $slot
  )) as $bySlot |
  ([range(0; $expected) as $offset |
    ($window_start + ($offset * 60)) as $at |
    ($bySlot[($at | tostring)] // null) as $slot |
    {
      at: $at,
      slot: $slot,
      passed: ($slot != null and $slot.passed)
    }
  ]) as $minutes |
  (reduce $minutes[] as $minute (
    {current_failure_seconds: 0, longest_failure_seconds: 0};
    if $minute.passed then
      .current_failure_seconds = 0
    else
      .current_failure_seconds += 60 |
      .longest_failure_seconds = ([
        .longest_failure_seconds,
        .current_failure_seconds
      ] | max)
    end
  )) as $interruptions |
  ($minutes | length) as $expectedCount |
  ([$minutes[] | select(.slot != null)] | length) as $covered |
  ([$minutes[] | select(.passed)] | length) as $passed |
  ([$samples[] | rest_probes(.)[]]) as $restProbes |
  ([$restProbes[] | select(.http >= 500 and .http < 600)] | length) as $rest5xx |
  ([$restProbes[].latency | numbers]) as $restLatency |
  ([$samples[].checks.disk_free_bytes | numbers | select(. > 0)]) as $knownDisks |
  ($knownDisks | min // null) as $minimumDisk |
  ($samples[0].finished_at // $samples[0].observed_at // null) as $firstAt |
  ($samples[-1].finished_at // $samples[-1].observed_at // null) as $lastAt |
  ($samples[-1].historical_windows // []) as $latestDexWindows |
  (
    ($latestDexWindows | length) == 6 and
    all($latestDexWindows[];
      .mode == "fixed_epoch_canary" and
      .status == "passed" and
      (.provider == "uniswap" or .provider == "pancakeswap") and
      .canary.asset_guid == $dex_canaries[.provider].asset_guid and
      .canary.route_key == $dex_canaries[.provider].route_key and
      .canary.quote_notional_usd ==
        $dex_canaries[.provider].quote_notional_usd and
      .canary.selected_at == $dex_canaries[.provider].selected_at
    )
  ) as $dexWindowsPassed |
  ($now_epoch >= $window_last_slot) as $timeWindowElapsed |
  {
    window: "acceptance_epoch_7d",
    acceptance_epoch_id: $epoch_id,
    epoch_status: $epoch_status,
    production_origin: $production_origin,
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    dex_canaries: $dex_canaries,
    latest_dex_windows: $latestDexWindows,
    window_started_at: ($window_start | todateiso8601),
    window_last_scheduled_at: ($window_last_slot | todateiso8601),
    evaluated_through: (
      if $expectedCount == 0 then null else ($evaluation_end | todateiso8601) end
    ),
    first_observed_at: $firstAt,
    last_observed_at: $lastAt,
    raw_eligible_samples: ($samples | length),
    rejected_epoch_samples: $rejectedEpochSamples,
    duplicate_samples: (($samples | length) - $covered),
    observed_minutes: $covered,
    missing_minutes: ($expectedCount - $covered),
    expected_minutes: $expectedCount,
    required_minutes: 10080,
    monitoring_coverage_percent: (
      if $expectedCount == 0 then 0
      else (($covered * 10000 / $expectedCount | floor) / 100)
      end
    ),
    availability_percent: (
      if $expectedCount == 0 then 0
      else (($passed * 10000 / $expectedCount | floor) / 100)
      end
    ),
    rest_5xx_percent: (
      if ($restProbes | length) == 0 then 0
      else (($rest5xx * 10000 / ($restProbes | length) | floor) / 100)
      end
    ),
    rest_p95_ms: p95($restLatency),
    longest_observed_failure_seconds: $interruptions.longest_failure_seconds,
    minimum_disk_free_bytes: $minimumDisk,
    known_disk_samples: ($knownDisks | length),
    acceptance: {
      full_7d_observation_window: $timeWindowElapsed,
      dex_fixed_24_48_72_windows_passed: $dexWindowsPassed,
      exactly_one_release: (
        ($samples | length) > 0 and
        all($samples[];
          .acceptance_epoch_id == $epoch_id and
          .deployment_id == $deployment_id and
          .deployment_url == $deployment_url and
          .deployment_commit == $deployment_commit
        )
      ),
      monitoring_coverage_at_least_99_5: (
        $expectedCount == 10080 and (($covered / $expectedCount) >= 0.995)
      ),
      availability_at_least_99_5: (
        $expectedCount == 10080 and (($passed / $expectedCount) >= 0.995)
      ),
      rest_5xx_below_0_5: (
        ($restProbes | length) > 0 and
        (($rest5xx / ($restProbes | length)) < 0.005)
      ),
      rest_p95_below_5s: (
        (p95($restLatency) // 20000) < 5000
      ),
      no_interruption_over_5m: (
        $expectedCount > 0 and
        $interruptions.longest_failure_seconds <= 300
      ),
      disk_at_least_25gib: (
        $minimumDisk != null and
        $minimumDisk >= (25 * 1024 * 1024 * 1024)
      )
    }
  } |
  .status = (
    if .epoch_status == "stopped" and
      .acceptance.full_7d_observation_window != true
    then "failed"
    elif .acceptance.full_7d_observation_window != true
    then "environment-pending"
    elif ([.acceptance[]] | all)
    then "production-recommendation"
    else "failed"
    end
  )
' "$history_file"
