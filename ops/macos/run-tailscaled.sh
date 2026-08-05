#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$script_dir/proxy-env.sh"

# The local Karing/Clash network extension already intercepts its fake-IP DNS
# answers. Adding an explicit HTTP proxy on top of that makes tailscaled's
# control-plane DNS cache see the proxy address where it expects the Tailscale
# hostname. Keep tailscaled direct by default while retaining an explicit,
# reversible opt-in for networks that truly require the loopback proxy.
tailscale_proxy_mode="${QIU_MARKET_TAILSCALE_SYSTEM_PROXY:-off}"
case "$tailscale_proxy_mode" in
  off)
    unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
    ;;
  auto|http://127.0.0.1:*|http://localhost:*)
    QIU_MARKET_SYSTEM_PROXY="$tailscale_proxy_mode"
    qiu_export_system_proxy
    ;;
  *)
    echo "Invalid QIU_MARKET_TAILSCALE_SYSTEM_PROXY; use off, auto, or a loopback HTTP URL." >&2
    exit 1
    ;;
esac

support_dir="$HOME/Library/Application Support/Qiu Market/tailscale"
mkdir -p "$support_dir"
exec /opt/homebrew/bin/tailscaled \
  --tun=userspace-networking \
  --state="$support_dir/tailscaled.state" \
  --socket="$support_dir/tailscaled.sock"
