#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$script_dir/proxy-env.sh"
qiu_export_system_proxy

support_dir="$HOME/Library/Application Support/Qiu Market/tailscale"
mkdir -p "$support_dir"
exec /opt/homebrew/bin/tailscaled \
  --tun=userspace-networking \
  --state="$support_dir/tailscaled.state" \
  --socket="$support_dir/tailscaled.sock"
