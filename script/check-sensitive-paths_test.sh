#!/usr/bin/env bash
set -Eeuo pipefail

REPOSITORY_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
CHECKER="${REPOSITORY_ROOT}/script/check-sensitive-paths.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/qiu-sensitive-paths.XXXXXX")"
trap 'find "${TEST_ROOT}" -depth -delete 2>/dev/null || true' EXIT

new_repository() {
  local directory="$1"
  mkdir -p "${directory}"
  git -C "${directory}" init -q
}

track_empty_path() {
  local directory="$1" path="$2"
  mkdir -p "${directory}/$(dirname "${path}")"
  : >"${directory}/${path}"
  git -C "${directory}" add -f -- "${path}"
}

allowed="${TEST_ROOT}/allowed"
new_repository "${allowed}"
for path in \
  .env.example \
  .env.production.template \
  config/app.env.example \
  config/credentials.example.json \
  keys/server.key.template \
  database/seed-dashboard.sql; do
  track_empty_path "${allowed}" "${path}"
done
"${CHECKER}" "${allowed}" >/dev/null

rejected_count=0
for path in \
  .env \
  nested/.env.local \
  keys/server.key \
  wallet/wallet.dat \
  signer/validator.seed \
  tss/node-01.share \
  tss/share-node-02 \
  state/app.sqlite3 \
  state/production.backup \
  infra/terraform.tfstate \
  home/.npmrc \
  auth/credentials.json \
  runtime/pgdata/PG_VERSION \
  $'unsafe\nname.txt' \
  $'unsafe\tname.txt'; do
  repository="${TEST_ROOT}/rejected-${rejected_count}"
  new_repository "${repository}"
  track_empty_path "${repository}" "${path}"
  if "${CHECKER}" "${repository}" >/dev/null 2>&1; then
    printf '[sensitive-paths-test] expected rejection for fake case=%s\n' "${rejected_count}" >&2
    exit 1
  fi
  rejected_count=$((rejected_count + 1))
done

ignore_repository="${TEST_ROOT}/ignore"
new_repository "${ignore_repository}"
cp "${REPOSITORY_ROOT}/.gitignore" "${ignore_repository}/.gitignore"
for path in \
  .env \
  nested/.env.production \
  signer.key \
  wallet.dat \
  state.sqlite3 \
  tss/node/share-one \
  database/production.backup \
  home/.npmrc; do
  mkdir -p "${ignore_repository}/$(dirname "${path}")"
  : >"${ignore_repository}/${path}"
  git -C "${ignore_repository}" check-ignore -q -- "${path}"
done
for path in .env.example nested/.env.production.template signer.key.example; do
  mkdir -p "${ignore_repository}/$(dirname "${path}")"
  : >"${ignore_repository}/${path}"
  if git -C "${ignore_repository}" check-ignore -q -- "${path}"; then
    printf '[sensitive-paths-test] reviewable template was ignored: %s\n' "${path}" >&2
    exit 1
  fi
done

printf '[sensitive-paths-test] PASS allowed=6 rejected=%s ignore_rules=11 contents_read=false\n' "${rejected_count}"
