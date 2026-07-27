#!/usr/bin/env bash
set -euo pipefail

# LaunchAgents inherit only the system PATH. PostgreSQL is installed through
# Homebrew on this Mac mini, so make the dependency lookup deterministic while
# retaining the caller's existing PATH for interactive runs.
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
support_dir="$HOME/Library/Application Support/Qiu Market"
observation_dir="${QIU_MARKET_OBSERVATION_DIR:-$support_dir/observations}"
production_origin="${QIU_MARKET_PRODUCTION_ORIGIN:-https://qiu-market.vercel.app}"
funnel_origin="${QIU_MARKET_FUNNEL_ORIGIN:-https://xiuqiudemac-mini.tail2e4386.ts.net}"
lock_dir="$observation_dir/.observer.lock"

mkdir -p "$observation_dir"
if ! mkdir "$lock_dir" 2>/dev/null; then
  echo "Qiu Market production observation is already running."
  exit 0
fi

temp_dir="$(mktemp -d "$observation_dir/.run.XXXXXX")"
cleanup() {
  rm -rf -- "$temp_dir"
  rmdir "$lock_dir" 2>/dev/null || true
}
trap cleanup EXIT

for command in curl jq psql; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required observer dependency is unavailable: $command" >&2
    exit 1
  fi
done
if [ ! -f "$repo_root/.env" ]; then
  echo "Qiu Market repository .env is unavailable: $repo_root/.env" >&2
  exit 1
fi

# shellcheck disable=SC1091
source "$repo_root/.env"

curl_code() {
  local output="$1"
  shift
  local code
  code="$(curl --silent --show-error --max-time 20 \
    --output "$output" --write-out '%{http_code}' "$@" \
    2>>"$temp_dir/curl-errors.log" || true)"
  if [[ "$code" =~ ^[0-9]{3}$ ]]; then
    printf '%s' "$code"
  else
    printf '000'
  fi
}

site_http="$(curl_code "$temp_dir/site.html" "$production_origin/markets")"
funnel_health_http="$(curl_code "$temp_dir/funnel-health.txt" "$funnel_origin/healthz")"
unsigned_http="$(curl_code "$temp_dir/unsigned.json" \
  --request POST \
  --header 'content-type: application/json' \
  --data '{"consumer_token":"production-observer","venue":"all","universe":"provider_union"}' \
  "$funnel_origin/api/v2/get_market_overview")"
trading_http="$(curl_code "$temp_dir/trading.json" \
  "$production_origin/api/v1/trading/markets/BTC-USDT/status")"

dashboard_body() {
  local venue="$1"
  jq -nc --arg venue "$venue" '{
    consumer_token: "production-observer",
    page: 1,
    page_size: 100,
    venue: $venue,
    filter: "assets",
    sort_by: "rank",
    sort_direction: "asc",
    include_uncovered: true,
    universe: "provider_top50"
  }'
}

uniswap_http="$(curl_code "$temp_dir/uniswap.json" \
  --request POST \
  --header 'content-type: application/json' \
  --data "$(dashboard_body uniswap)" \
  "$production_origin/api/v2/get_asset_dashboard")"
pancake_http="$(curl_code "$temp_dir/pancakeswap.json" \
  --request POST \
  --header 'content-type: application/json' \
  --data "$(dashboard_body pancakeswap)" \
  "$production_origin/api/v2/get_asset_dashboard")"

dex_summary() {
  local provider="$1"
  local http_code="$2"
  local source_file="$3"
  if [ "$http_code" = 200 ] && jq -e '.code == 2000 and (.result | type == "array")' \
    "$source_file" >/dev/null 2>&1; then
    jq -c --arg provider "$provider" '{
      provider: $provider,
      http_status: 200,
      total: (.total // (.result | length)),
      displayed_assets: ([.result[] | select(.display_available == true)] | length),
      current_route_assets: ([.result[] | select(.dex_route_available == true)] | length),
      reference_only_assets: ([
        .result[] |
        select(.coverage_status == "reference_only")
      ] | length),
      unavailable_assets: ([
        .result[] |
        select(.display_available != true)
      ] | length)
    }' "$source_file"
  else
    jq -nc --arg provider "$provider" --arg http "$http_code" '{
      provider: $provider,
      http_status: ($http | tonumber? // 0),
      total: 0,
      displayed_assets: 0,
      current_route_assets: 0,
      reference_only_assets: 0,
      unavailable_assets: 0
    }'
  fi
}

uniswap_summary="$(dex_summary uniswap "$uniswap_http" "$temp_dir/uniswap.json")"
pancake_summary="$(dex_summary pancakeswap "$pancake_http" "$temp_dir/pancakeswap.json")"

trading_state=""
trading_sequence=""
trading_last_error=""
if [ "$trading_http" = 200 ]; then
  trading_state="$(jq -r '.state // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
  trading_sequence="$(jq -r '.sequence // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
  trading_last_error="$(jq -r '.last_error // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
fi

psql_command=(
  psql -X -v ON_ERROR_STOP=1
  -h "$MARKET_MASTER_DB_HOST"
  -p "$MARKET_MASTER_DB_PORT"
  -U "$MARKET_MASTER_DB_USER"
  -d "$MARKET_MASTER_DB_NAME"
  -At
)
coverage_json="[]"
database_ok=false
if PGPASSWORD="$MARKET_MASTER_DB_PASSWORD" coverage_json="$("${psql_command[@]}" <<'SQL' 2>"$temp_dir/postgres-errors.log"
WITH requested_windows(hours) AS (
    VALUES (24), (48), (72)
),
providers(provider) AS (
    VALUES ('uniswap'), ('pancakeswap')
),
observations AS (
    SELECT requested_windows.hours,
           quote.provider,
           quote.asset_guid,
           quote.route_key,
           quote.quote_notional_usd,
           quote.observed_at,
           LAG(quote.observed_at) OVER (
               PARTITION BY requested_windows.hours,
                            quote.provider,
                            quote.asset_guid,
                            quote.route_key,
                            quote.quote_notional_usd
               ORDER BY quote.observed_at
           ) AS previous_at
    FROM requested_windows
    JOIN dex_quote_observation quote
      ON quote.observed_at >=
         now() - make_interval(hours => requested_windows.hours)
),
route_groups AS (
    SELECT hours,
           provider,
           asset_guid,
           route_key,
           quote_notional_usd,
           MIN(observed_at) AS first_at,
           MAX(observed_at) AS last_at,
           COUNT(*) AS samples,
           COALESCE(
               MAX(EXTRACT(EPOCH FROM (observed_at - previous_at))),
               0
           ) AS max_gap_seconds
    FROM observations
    GROUP BY hours, provider, asset_guid, route_key, quote_notional_usd
),
summary AS (
    SELECT requested_windows.hours,
           providers.provider,
           COUNT(route_groups.*) AS observed_groups,
           COUNT(route_groups.*) FILTER (
               WHERE route_groups.first_at <=
                     now() - make_interval(hours => requested_windows.hours)
                     + interval '30 minutes'
           ) AS full_window_groups,
           COUNT(route_groups.*) FILTER (
               WHERE route_groups.first_at <=
                     now() - make_interval(hours => requested_windows.hours)
                     + interval '30 minutes'
                 AND route_groups.last_at >= now() - interval '10 minutes'
                 AND route_groups.max_gap_seconds <= 600
           ) AS qualifying_groups,
           MAX(route_groups.samples) AS maximum_samples,
           MIN(route_groups.max_gap_seconds) FILTER (
               WHERE route_groups.first_at <=
                     now() - make_interval(hours => requested_windows.hours)
                     + interval '30 minutes'
           ) AS best_full_window_gap_seconds,
           MIN(route_groups.first_at) AS oldest_observation_at,
           MAX(route_groups.last_at) AS freshest_observation_at
    FROM requested_windows
    CROSS JOIN providers
    LEFT JOIN route_groups
      ON route_groups.hours = requested_windows.hours
     AND route_groups.provider = providers.provider
    GROUP BY requested_windows.hours, providers.provider
)
SELECT COALESCE(
    jsonb_agg(
        jsonb_build_object(
            'hours', hours,
            'provider', provider,
            'status', CASE WHEN qualifying_groups > 0 THEN 'passed' ELSE 'pending' END,
            'observed_groups', observed_groups,
            'full_window_groups', full_window_groups,
            'qualifying_groups', qualifying_groups,
            'maximum_samples', COALESCE(maximum_samples, 0),
            'best_full_window_gap_seconds',
                ROUND(best_full_window_gap_seconds::numeric, 2),
            'oldest_observation_at', oldest_observation_at,
            'freshest_observation_at', freshest_observation_at
        )
        ORDER BY hours, provider
    ),
    '[]'::jsonb
)::text
FROM summary;
SQL
)"; then
  database_ok=true
else
  coverage_json="[]"
fi

sample_ok=false
if [ "$site_http" = 200 ] &&
  [ "$funnel_health_http" = 200 ] &&
  [ "$unsigned_http" = 401 ] &&
  [ "$trading_http" = 200 ] &&
  [ "$uniswap_http" = 200 ] &&
  [ "$pancake_http" = 200 ] &&
  [ "$trading_state" = ready ] &&
  [ -z "$trading_last_error" ] &&
  [ "$database_ok" = true ]; then
  sample_ok=true
fi

historical_complete=false
if jq -e '
  length == 6 and
  all(.status == "passed")
' <<<"$coverage_json" >/dev/null 2>&1; then
  historical_complete=true
fi

observed_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
latest_report="$observation_dir/latest.json"
history_file="$observation_dir/production-soak.jsonl"
jq -n \
  --arg observed_at "$observed_at" \
  --arg production_origin "$production_origin" \
  --arg funnel_origin "$funnel_origin" \
  --arg site_http "$site_http" \
  --arg funnel_health_http "$funnel_health_http" \
  --arg unsigned_http "$unsigned_http" \
  --arg trading_http "$trading_http" \
  --arg trading_state "$trading_state" \
  --arg trading_sequence "$trading_sequence" \
  --arg trading_last_error "$trading_last_error" \
  --arg sample_ok "$sample_ok" \
  --arg database_ok "$database_ok" \
  --arg historical_complete "$historical_complete" \
  --argjson uniswap "$uniswap_summary" \
  --argjson pancakeswap "$pancake_summary" \
  --argjson coverage "$coverage_json" \
  '{
    schema_version: 1,
    observed_at: $observed_at,
    status: (
      if $sample_ok != "true" then "failed"
      elif $historical_complete == "true" then "passed"
      else "observing"
      end
    ),
    current_checks_status: (
      if $sample_ok == "true" then "passed" else "failed" end
    ),
    historical_acceptance_status: (
      if $historical_complete == "true" then "passed" else "pending" end
    ),
    production_origin: $production_origin,
    funnel_origin: $funnel_origin,
    checks: {
      production_page_http: ($site_http | tonumber? // 0),
      funnel_health_http: ($funnel_health_http | tonumber? // 0),
      unsigned_funnel_rest_http: ($unsigned_http | tonumber? // 0),
      trading_bff_http: ($trading_http | tonumber? // 0),
      trading_state: $trading_state,
      trading_sequence: $trading_sequence,
      trading_last_error: $trading_last_error,
      database_ok: ($database_ok == "true")
    },
    dex: [$uniswap, $pancakeswap],
    historical_windows: $coverage
  }' > "$temp_dir/latest.json"

mv "$temp_dir/latest.json" "$latest_report"
jq -c . "$latest_report" >> "$history_file"
chmod 600 "$latest_report" "$history_file"
jq . "$latest_report"

if [ "$sample_ok" != true ]; then
  echo "Qiu Market production observation failed one or more current checks." >&2
  exit 1
fi
