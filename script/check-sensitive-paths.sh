#!/usr/bin/env bash
set -Eeuo pipefail

REPOSITORY_ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}"

if ! git -C "${REPOSITORY_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf '[sensitive-paths] not a Git worktree\n' >&2
  exit 2
fi

is_reviewable_template() {
  local basename="$1"
  case "${basename}" in
    *.example|*.example.*|*.template|*.template.*) return 0 ;;
    *) return 1 ;;
  esac
}

sensitive_reason() {
  local path="$1"
  local lower basename
  lower="$(printf '%s' "${path}" | tr '[:upper:]' '[:lower:]')"
  basename="${lower##*/}"

  if [[ "$(printf '%s' "${path}" | LC_ALL=C tr -d '\000-\037\177')" != "${path}" ]]; then
    printf 'S001 unsafe control character in tracked path'
    return 0
  fi
  if is_reviewable_template "${basename}"; then
    return 1
  fi

  case "${basename}" in
    .env|.env.*|*.env|*.env.*|.envrc|.envrc.*)
      printf 'S010 dotenv file'
      return 0
      ;;
    *.pem|*.key|*.p12|*.pfx|*.jks|id_rsa|id_rsa.*|id_ed25519|id_ed25519.*|private-key*|private_key*|privkey*)
      printf 'S020 private key or keystore path'
      return 0
      ;;
    seed|seed.*|*.seed|*.mnemonic|mnemonic|mnemonic.*|seed-phrase*|seed_phrase*)
      printf 'S030 seed or mnemonic path'
      return 0
      ;;
    wallet.dat|wallet.json|wallet-state*|wallet_state*|keystore|keystore.*|utc--*)
      printf 'S040 wallet state path'
      return 0
      ;;
    *.db|*.sqlite|*.sqlite3|*.mdb|*.ldb|*.rdb|*.dump|*.backup|*.tfstate|*.tfstate.*|dump.rdb|dump.sql|database-dump.sql|database_dump.sql|production-dump*|production_dump*)
      printf 'S050 database or infrastructure state path'
      return 0
      ;;
    .npmrc|.pypirc|.netrc|credentials|credentials.json|service-account.json|service_account.json|kubeconfig)
      printf 'S060 credential path'
      return 0
      ;;
  esac

  case "/${lower}" in
    */secrets/*|*/.secrets/*)
      printf 'S070 secret directory path'
      return 0
      ;;
    */.aws/credentials|*/.docker/config.json|*/.config/gcloud/application_default_credentials.json)
      printf 'S060 credential store path'
      return 0
      ;;
    */pgdata/*|*/postgres-data/*)
      printf 'S050 database cluster state path'
      return 0
      ;;
  esac

  case "${lower}" in
    *tss*)
      case "${basename}" in
        *.share|*.tss-share|share|share.*|share-*|share_*|*share*.json|*share*.txt|*share*.bin|*share*.bak|*share*.backup)
          printf 'S080 TSS share path'
          return 0
          ;;
      esac
      ;;
  esac
  return 1
}

violation_count=0
while IFS= read -r -d '' path; do
  if reason="$(sensitive_reason "${path}")"; then
    printf '[sensitive-paths] rejected tracked path: ' >&2
    printf '%q' "${path}" >&2
    printf ' (%s)\n' "${reason}" >&2
    violation_count=$((violation_count + 1))
  fi
done < <(git -C "${REPOSITORY_ROOT}" ls-files -z --cached)

if [[ "${violation_count}" -ne 0 ]]; then
  printf '[sensitive-paths] FAIL violations=%s (path metadata only; file contents were not read)\n' "${violation_count}" >&2
  exit 1
fi

printf '[sensitive-paths] PASS tracked_paths_only=true contents_read=false\n'
