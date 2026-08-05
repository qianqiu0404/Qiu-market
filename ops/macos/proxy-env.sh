#!/usr/bin/env bash

# Qiu Market runs as LaunchAgents/LaunchDaemons, so its processes do not inherit
# the proxy configured in macOS System Settings. Clash Verge fake-IP DNS can
# still affect those processes, leaving them with unroutable synthetic
# addresses. Export the active loopback HTTPS proxy explicitly when available.
qiu_export_system_proxy() {
  local mode="${QIU_MARKET_SYSTEM_PROXY:-auto}"
  local proxy_url=""
  local proxy_dump=""
  local proxy_enabled=""
  local proxy_host=""
  local proxy_port=""

  case "$mode" in
    off)
      return 0
      ;;
    auto)
      ;;
    http://127.0.0.1:*|http://localhost:*)
      proxy_url="$mode"
      ;;
    *)
      echo "Ignoring unsupported QIU_MARKET_SYSTEM_PROXY value; use auto, off, or a loopback http:// URL." >&2
      return 0
      ;;
  esac

  if [ -z "$proxy_url" ]; then
    if [ -n "${HTTPS_PROXY:-${https_proxy:-}}" ]; then
      proxy_url="${HTTPS_PROXY:-${https_proxy:-}}"
    elif [ -n "${HTTP_PROXY:-${http_proxy:-}}" ]; then
      proxy_url="${HTTP_PROXY:-${http_proxy:-}}"
    fi
  fi

  if [ -z "$proxy_url" ]; then
    if [ -n "${QIU_MARKET_SYSTEM_PROXY_DUMP:-}" ]; then
      proxy_dump="$QIU_MARKET_SYSTEM_PROXY_DUMP"
    elif [ -x /usr/sbin/scutil ]; then
      proxy_dump="$(/usr/sbin/scutil --proxy 2>/dev/null || true)"
    fi
    proxy_enabled="$(
      printf '%s\n' "$proxy_dump" |
        awk '$1 == "HTTPSEnable" && $2 == ":" { print $3; exit }'
    )"
    proxy_host="$(
      printf '%s\n' "$proxy_dump" |
        awk '$1 == "HTTPSProxy" && $2 == ":" { print $3; exit }'
    )"
    proxy_port="$(
      printf '%s\n' "$proxy_dump" |
        awk '$1 == "HTTPSPort" && $2 == ":" { print $3; exit }'
    )"

    [ "$proxy_enabled" = "1" ] || return 0
    case "$proxy_host" in
      127.0.0.1|localhost) ;;
      *) return 0 ;;
    esac
    case "$proxy_port" in
      ''|*[!0-9]*) return 0 ;;
    esac
    if [ "$proxy_port" -lt 1 ] || [ "$proxy_port" -gt 65535 ]; then
      return 0
    fi
    if [ "${QIU_MARKET_SKIP_PROXY_CONNECTIVITY_CHECK:-false}" != "true" ] &&
      [ -x /usr/bin/nc ] &&
      ! /usr/bin/nc -z -w 1 "$proxy_host" "$proxy_port" >/dev/null 2>&1; then
      return 0
    fi
    proxy_url="http://$proxy_host:$proxy_port"
  fi

  case "$proxy_url" in
    http://127.0.0.1:*|http://localhost:*) ;;
    *) return 0 ;;
  esac

  HTTP_PROXY="${HTTP_PROXY:-$proxy_url}"
  HTTPS_PROXY="${HTTPS_PROXY:-$proxy_url}"
  http_proxy="${http_proxy:-$HTTP_PROXY}"
  https_proxy="${https_proxy:-$HTTPS_PROXY}"
  NO_PROXY="${NO_PROXY:+$NO_PROXY,}127.0.0.1,localhost,::1"
  no_proxy="${no_proxy:+$no_proxy,}127.0.0.1,localhost,::1"
  export HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
}
