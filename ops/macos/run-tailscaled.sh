#!/usr/bin/env bash
set -euo pipefail

support_dir="$HOME/Library/Application Support/Qiu Market/tailscale"
mkdir -p "$support_dir"
exec /opt/homebrew/bin/tailscaled \
  --tun=userspace-networking \
  --state="$support_dir/tailscaled.state" \
  --socket="$support_dir/tailscaled.sock"
