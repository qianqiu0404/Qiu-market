#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
if [ "$#" -gt 0 ]; then
  shift
fi

support_dir="$HOME/Library/Application Support/Qiu Market"
observation_dir="${QIU_MARKET_OBSERVATION_DIR:-$support_dir/observations}"
epoch_file="${QIU_MARKET_ACCEPTANCE_EPOCH_FILE:-$observation_dir/acceptance-epoch.json}"
production_origin="${QIU_MARKET_PRODUCTION_ORIGIN:-https://qiu-market.vercel.app}"

usage() {
  cat >&2 <<'USAGE'
Usage:
  manage-acceptance-epoch.sh status
  manage-acceptance-epoch.sh start \
    --deployment-id dpl_... \
    --deployment-url https://immutable-deployment.vercel.app \
    --commit <40-hex-sha> \
    [--epoch-id <id>] \
    [--production-origin https://qiu-market.vercel.app]
  manage-acceptance-epoch.sh stop

Start is allowed only when the Production BFF reports the exact immutable
deployment URL and release commit. The acceptance window begins at the next
UTC wall-clock minute.
USAGE
  exit 2
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required acceptance epoch dependency is unavailable: $1" >&2
    exit 1
  fi
}

iso_at() {
  jq -nr --argjson epoch "$1" '$epoch | todateiso8601'
}

header_value() {
  local header_file="$1"
  local header_name="$2"
  awk -v name="$header_name" '
    BEGIN { IGNORECASE = 1 }
    {
      sub(/\r$/, "")
      split($0, parts, ":")
      if (tolower(parts[1]) == tolower(name)) {
        sub(/^[^:]*:[[:space:]]*/, "", $0)
        value = $0
      }
    }
    END { print value }
  ' "$header_file"
}

write_epoch() {
  local destination="$1"
  local payload="$2"
  local temp_file
  temp_file="$(mktemp "$observation_dir/.acceptance-epoch.XXXXXX")"
  printf '%s\n' "$payload" > "$temp_file"
  chmod 600 "$temp_file"
  mv "$temp_file" "$destination"
}

case "$action" in
  status)
    if [ ! -s "$epoch_file" ]; then
      echo "acceptance epoch: not-started"
      exit 0
    fi
    jq '{
      schema_version,
      epoch_id,
      status,
      production_origin,
      deployment_id,
      deployment_url,
      deployment_commit,
      created_at,
      started_at,
      stopped_at
    }' "$epoch_file"
    ;;
  start)
    require_command curl
    require_command jq
    deployment_id=""
    deployment_url=""
    deployment_commit=""
    epoch_id=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --deployment-id)
          [ "$#" -ge 2 ] || usage
          deployment_id="$2"
          shift 2
          ;;
        --deployment-url)
          [ "$#" -ge 2 ] || usage
          deployment_url="${2%/}"
          shift 2
          ;;
        --commit)
          [ "$#" -ge 2 ] || usage
          deployment_commit="$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')"
          shift 2
          ;;
        --epoch-id)
          [ "$#" -ge 2 ] || usage
          epoch_id="$2"
          shift 2
          ;;
        --production-origin)
          [ "$#" -ge 2 ] || usage
          production_origin="${2%/}"
          shift 2
          ;;
        *)
          usage
          ;;
      esac
    done
    if [[ ! "$deployment_id" =~ ^dpl_[A-Za-z0-9]+$ ]]; then
      echo "A valid immutable Vercel deployment ID is required." >&2
      exit 1
    fi
    if [[ ! "$deployment_url" =~ ^https://[A-Za-z0-9.-]+\.vercel\.app$ ]]; then
      echo "A valid immutable HTTPS Vercel deployment URL is required." >&2
      exit 1
    fi
    if [[ ! "$deployment_commit" =~ ^[0-9a-f]{40}$ ]]; then
      echo "A full 40-character release commit is required." >&2
      exit 1
    fi
    if [[ ! "$production_origin" =~ ^https://[A-Za-z0-9.-]+$ ]]; then
      echo "A valid HTTPS Production origin is required." >&2
      exit 1
    fi
    if [ "$deployment_url" = "$production_origin" ]; then
      echo "The Production alias is not an immutable deployment URL." >&2
      exit 1
    fi
    if [ -s "$epoch_file" ] &&
      [ "$(jq -r '.status // ""' "$epoch_file")" = active ]; then
      echo "An acceptance epoch is already active; stop it explicitly first." >&2
      exit 1
    fi

    mkdir -p "$observation_dir"
    chmod 700 "$observation_dir"
    headers_file="$(mktemp "$observation_dir/.epoch-probe-headers.XXXXXX")"
    body_file="$(mktemp "$observation_dir/.epoch-probe-body.XXXXXX")"
    cleanup() {
      rm -f -- "$headers_file" "$body_file"
    }
    trap cleanup EXIT
    probe_http="$(
      curl --silent --show-error --max-time 20 \
        --dump-header "$headers_file" \
        --output "$body_file" \
        --write-out '%{http_code}' \
        "$production_origin/api/v1/trading/markets/BTC-USDT/status" || true
    )"
    observed_status="$(header_value "$headers_file" X-Qiu-Market-Provenance)"
    observed_commit="$(header_value "$headers_file" X-Qiu-Market-Release-Commit)"
    observed_id="$(header_value "$headers_file" X-Qiu-Market-Deployment-ID)"
    observed_url="$(header_value "$headers_file" X-Qiu-Market-Deployment-URL)"
    if [ "$probe_http" != 200 ] ||
      [ "$observed_status" != VERIFIED ] ||
      [ "$observed_commit" != "$deployment_commit" ] ||
      [ "$observed_id" != "$deployment_id" ] ||
      [ "${observed_url%/}" != "$deployment_url" ]; then
      echo "Production BFF does not report the requested immutable release identity." >&2
      jq -n \
        --arg http "$probe_http" \
        --arg provenance "$observed_status" \
        --arg expected_id "$deployment_id" \
        --arg observed_id "$observed_id" \
        --arg expected_commit "$deployment_commit" \
        --arg observed_commit "$observed_commit" \
        --arg expected_url "$deployment_url" \
        --arg observed_url "$observed_url" \
        '{
          http_status: ($http | tonumber? // 0),
          provenance: $provenance,
          expected_deployment_id: $expected_id,
          observed_deployment_id: $observed_id,
          expected_commit: $expected_commit,
          observed_commit: $observed_commit,
          expected_deployment_url: $expected_url,
          observed_deployment_url: $observed_url
        }' >&2
      exit 1
    fi

    mkdir -p "$observation_dir/archive"
    chmod 700 "$observation_dir/archive"
    if [ -s "$epoch_file" ]; then
      archived_at="$(date -u '+%Y%m%dT%H%M%SZ')"
      archive_id="$(
        jq -r '.epoch_id // "unknown"' "$epoch_file" |
          tr -cd 'A-Za-z0-9._-'
      )"
      archive_target="$observation_dir/archive/acceptance-epoch-$archive_id-$archived_at.json"
      if [ -e "$archive_target" ]; then
        echo "Refusing to overwrite an existing acceptance epoch archive." >&2
        exit 1
      fi
      cp "$epoch_file" "$archive_target"
      chmod 600 "$archive_target"
    fi

    created_epoch="$(date -u '+%s')"
    started_epoch=$(((created_epoch / 60 + 1) * 60))
    created_at="$(iso_at "$created_epoch")"
    started_at="$(iso_at "$started_epoch")"
    if [ -z "$epoch_id" ]; then
      epoch_id="qiu-market-$(date -u '+%Y%m%dT%H%M%SZ')-$RANDOM"
    fi
    if [[ ! "$epoch_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$ ]]; then
      echo "The acceptance epoch ID must be 8-128 safe characters." >&2
      exit 1
    fi
    payload="$(
      jq -n \
        --arg epoch_id "$epoch_id" \
        --arg production_origin "$production_origin" \
        --arg deployment_id "$deployment_id" \
        --arg deployment_url "$deployment_url" \
        --arg deployment_commit "$deployment_commit" \
        --arg created_at "$created_at" \
        --arg started_at "$started_at" \
        '{
          schema_version: 1,
          epoch_id: $epoch_id,
          status: "active",
          production_origin: $production_origin,
          deployment_id: $deployment_id,
          deployment_url: $deployment_url,
          deployment_commit: $deployment_commit,
          created_at: $created_at,
          started_at: $started_at,
          stopped_at: null
        }'
    )"
    write_epoch "$epoch_file" "$payload"
    echo "Acceptance epoch starts at $started_at on the verified release."
    jq . "$epoch_file"
    ;;
  stop)
    require_command jq
    if [ ! -s "$epoch_file" ]; then
      echo "No acceptance epoch exists." >&2
      exit 1
    fi
    if [ "$(jq -r '.status // ""' "$epoch_file")" != active ]; then
      echo "Acceptance epoch is already stopped."
      exit 0
    fi
    mkdir -p "$observation_dir"
    stopped_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    payload="$(
      jq --arg stopped_at "$stopped_at" '
        .status = "stopped" |
        .stopped_at = $stopped_at
      ' "$epoch_file"
    )"
    write_epoch "$epoch_file" "$payload"
    echo "Acceptance epoch stopped at $stopped_at; evidence was preserved."
    ;;
  *)
    usage
    ;;
esac
