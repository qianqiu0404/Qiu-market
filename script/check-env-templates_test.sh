#!/usr/bin/env bash
set -Eeuo pipefail

REPOSITORY_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
CHECKER="${REPOSITORY_ROOT}/script/check-env-templates.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/qiu-env-templates.XXXXXX")"
trap 'find "${TEST_ROOT}" -depth -delete 2>/dev/null || true' EXIT

new_repository() {
  local directory="$1"
  mkdir -p "${directory}"
  git -C "${directory}" init -q
}

track_template() {
  local directory="$1" path="$2" contents="$3"
  mkdir -p "${directory}/$(dirname "${path}")"
  printf '%s' "${contents}" >"${directory}/${path}"
  git -C "${directory}" add -- "${path}"
}

allowed="${TEST_ROOT}/allowed"
new_repository "${allowed}"
track_template "${allowed}" .env.example $'# reviewed fake template\nexport APP_MODE=local\nEMPTY_OK=\n'
track_template "${allowed}" ops/production.env.template $'DATABASE_HOST=127.0.0.1\nDATABASE_PORT=5432\n'
allowed_output="$(${CHECKER} "${allowed}")"
if [[ "${allowed_output}" != *"templates=2 keys=4"* ]]; then
  printf '[env-templates-test] valid fixture count mismatch\n' >&2
  exit 1
fi
if "${CHECKER}" "${allowed}" unexpected-extra-argument >/dev/null 2>&1; then
  printf '[env-templates-test] expected extra-argument rejection\n' >&2
  exit 1
fi

rejected_count=0
for scenario in malformed invalid_key duplicate empty control; do
  repository="${TEST_ROOT}/rejected-${rejected_count}"
  new_repository "${repository}"
  case "${scenario}" in
    malformed) contents=$'NOT_AN_ASSIGNMENT\n' ;;
    invalid_key) contents=$'INVALID-KEY=fake-value-marker\n' ;;
    duplicate) contents=$'DUPLICATE=first-fake-value\nDUPLICATE=second-fake-value\n' ;;
    empty) contents=$'# comments only\n\n' ;;
    control) contents=$'VALID=fake-value-marker\tunexpected\n' ;;
  esac
  track_template "${repository}" .env.example "${contents}"
  output_file="${repository}/output"
  if "${CHECKER}" "${repository}" >"${output_file}" 2>&1; then
    printf '[env-templates-test] expected rejection for fake case=%s\n' "${rejected_count}" >&2
    exit 1
  fi
  if grep -Fq 'fake-value' "${output_file}"; then
    printf '[env-templates-test] fixture value leaked for fake case=%s\n' "${rejected_count}" >&2
    exit 1
  fi
  rejected_count=$((rejected_count + 1))
done

symlink_repository="${TEST_ROOT}/symlink"
new_repository "${symlink_repository}"
printf 'PLACEHOLDER=fake\n' >"${symlink_repository}/outside"
ln -s outside "${symlink_repository}/.env.example"
git -C "${symlink_repository}" add -- .env.example
if "${CHECKER}" "${symlink_repository}" >/dev/null 2>&1; then
  printf '[env-templates-test] expected symlink rejection\n' >&2
  exit 1
fi
rejected_count=$((rejected_count + 1))

oversized_repository="${TEST_ROOT}/oversized"
new_repository "${oversized_repository}"
mkdir -p "${oversized_repository}"
LC_ALL=C awk 'BEGIN { printf "OVERSIZED="; for (i = 0; i < 65537; i++) printf "x"; printf "\n" }' >"${oversized_repository}/.env.example"
git -C "${oversized_repository}" add -- .env.example
if "${CHECKER}" "${oversized_repository}" >/dev/null 2>&1; then
  printf '[env-templates-test] expected oversized rejection\n' >&2
  exit 1
fi
rejected_count=$((rejected_count + 1))

missing_repository="${TEST_ROOT}/missing"
new_repository "${missing_repository}"
: >"${missing_repository}/README.md"
git -C "${missing_repository}" add -- README.md
if "${CHECKER}" "${missing_repository}" >/dev/null 2>&1; then
  printf '[env-templates-test] expected missing-template rejection\n' >&2
  exit 1
fi
rejected_count=$((rejected_count + 1))

printf '[env-templates-test] PASS allowed_templates=2 allowed_keys=4 rejected=%s values_printed=false local_env_compared=false\n' "${rejected_count}"
