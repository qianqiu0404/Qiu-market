#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

proxy_dump='
<dictionary> {
  HTTPSEnable : 1
  HTTPSPort : 7897
  HTTPSProxy : 127.0.0.1
}
'

(
  unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
  export QIU_MARKET_SYSTEM_PROXY=auto
  export QIU_MARKET_SYSTEM_PROXY_DUMP="$proxy_dump"
  export QIU_MARKET_SKIP_PROXY_CONNECTIVITY_CHECK=true
  # shellcheck disable=SC1091
  source "$script_dir/proxy-env.sh"
  qiu_export_system_proxy
  [ "$HTTP_PROXY" = "http://127.0.0.1:7897" ]
  [ "$HTTPS_PROXY" = "http://127.0.0.1:7897" ]
  [ "$http_proxy" = "$HTTP_PROXY" ]
  [ "$https_proxy" = "$HTTPS_PROXY" ]
  case ",$NO_PROXY," in
    *,127.0.0.1,localhost,::1,*) ;;
    *) exit 1 ;;
  esac
)

(
  unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
  export QIU_MARKET_SYSTEM_PROXY=off
  export QIU_MARKET_SYSTEM_PROXY_DUMP="$proxy_dump"
  # shellcheck disable=SC1091
  source "$script_dir/proxy-env.sh"
  qiu_export_system_proxy
  [ -z "${HTTPS_PROXY:-}" ]
)

(
  export HTTP_PROXY=http://127.0.0.1:9000
  export HTTPS_PROXY=http://127.0.0.1:9001
  unset http_proxy https_proxy NO_PROXY no_proxy
  export QIU_MARKET_SYSTEM_PROXY=auto
  export QIU_MARKET_SYSTEM_PROXY_DUMP="$proxy_dump"
  export QIU_MARKET_SKIP_PROXY_CONNECTIVITY_CHECK=true
  # shellcheck disable=SC1091
  source "$script_dir/proxy-env.sh"
  qiu_export_system_proxy
  [ "$HTTP_PROXY" = "http://127.0.0.1:9000" ]
  [ "$HTTPS_PROXY" = "http://127.0.0.1:9001" ]
)

(
  unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
  export QIU_MARKET_SYSTEM_PROXY=auto
  export QIU_MARKET_SYSTEM_PROXY_DUMP='
<dictionary> {
  HTTPSEnable : 1
  HTTPSPort : 8080
  HTTPSProxy : proxy.example.com
}
'
  export QIU_MARKET_SKIP_PROXY_CONNECTIVITY_CHECK=true
  # shellcheck disable=SC1091
  source "$script_dir/proxy-env.sh"
  qiu_export_system_proxy
  [ -z "${HTTPS_PROXY:-}" ]
)

printf '%s\n' "Qiu Market proxy environment tests passed."
