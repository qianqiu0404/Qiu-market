#!/usr/bin/env bash
set -euo pipefail

support_dir="$HOME/Library/Application Support/Qiu Market"
history_file="${QIU_MARKET_SOAK_HISTORY:-$support_dir/observations/production-soak.jsonl}"

if [ ! -s "$history_file" ]; then
  echo "No Qiu Market production soak samples are available." >&2
  exit 1
fi

window_end_epoch="$(date -u '+%s')"
window_end_epoch=$((window_end_epoch - window_end_epoch % 60))
window_start_epoch=$((window_end_epoch - 7 * 24 * 60 * 60 + 60))

jq -s \
  --argjson window_start "$window_start_epoch" \
  --argjson window_end "$window_end_epoch" '
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

  map(
    select(
      ((.observed_at | fromdateiso8601) >= $window_start) and
      ((.observed_at | fromdateiso8601) < ($window_end + 60))
    )
  ) | sort_by(.observed_at) as $samples |
  (reduce $samples[] as $sample ({};
    (($sample.observed_at | fromdateiso8601) / 60 | floor | tostring) as $minute |
    .[$minute] = $sample
  )) as $byMinute |
  ([range($window_start; $window_end + 1; 60) as $at |
    ($byMinute[(($at / 60 | floor) | tostring)] // null) as $sample |
    {
      at: $at,
      sample: $sample,
      passed: ($sample != null and $sample.current_checks_status == "passed")
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
  ($minutes | length) as $expected |
  ([$minutes[] | select(.sample != null)] | length) as $covered |
  ([$minutes[] | select(.passed)] | length) as $passed |
  ([$samples[] | rest_probes(.)[]]) as $restProbes |
  ([$restProbes[] | select(.http >= 500 and .http < 600)] | length) as $rest5xx |
  ([$restProbes[].latency | numbers]) as $restLatency |
  ([$samples[].checks.disk_free_bytes | numbers | select(. > 0)]) as $knownDisks |
  ($knownDisks | min // null) as $minimumDisk |
  ($samples[0].observed_at // null) as $firstAt |
  ($samples[-1].observed_at // null) as $lastAt |
  (if $firstAt == null then null else ($firstAt | fromdateiso8601) end) as $firstEpoch |
  (if $lastAt == null then null else ($lastAt | fromdateiso8601) end) as $lastEpoch |
  {
    window: "rolling_7d",
    first_observed_at: $firstAt,
    last_observed_at: $lastAt,
    raw_samples: ($samples | length),
    observed_minutes: $covered,
    missing_minutes: ($expected - $covered),
    expected_minutes: $expected,
    monitoring_coverage_percent: (
      if $expected == 0 then 0 else (($covered * 10000 / $expected | floor) / 100) end
    ),
    availability_percent: (
      if $expected == 0 then 0 else (($passed * 10000 / $expected | floor) / 100) end
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
      full_7d_observation_window: (
        $firstEpoch != null and
        $lastEpoch != null and
        $firstEpoch <= ($window_start + 120) and
        $lastEpoch >= ($window_end - 120)
      ),
      monitoring_coverage_at_least_99_5: (
        $expected > 0 and (($covered / $expected) >= 0.995)
      ),
      availability_at_least_99_5: (
        $expected > 0 and (($passed / $expected) >= 0.995)
      ),
      rest_5xx_below_0_5: (
        ($restProbes | length) > 0 and
        (($rest5xx / ($restProbes | length)) < 0.005)
      ),
      rest_p95_below_5s: (
        (p95($restLatency) // 20000) < 5000
      ),
      no_interruption_over_5m: (
        $interruptions.longest_failure_seconds <= 300
      ),
      disk_at_least_25gib: (
        $minimumDisk != null and
        $minimumDisk >= (25 * 1024 * 1024 * 1024)
      )
    }
  } |
  .status = (
    if .acceptance.full_7d_observation_window != true then "environment-pending"
    elif ([.acceptance[]] | all) then "production-recommendation"
    else "failed"
    end
  )
' "$history_file"
