#!/usr/bin/env bash
set -Eeuo pipefail

if [[ "$#" -gt 1 ]]; then
  printf '[env-templates] FAIL RuleID=E000 category=invalid_invocation\n' >&2
  exit 2
fi

REPOSITORY_ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}"
MAX_TEMPLATE_BYTES=65536

if ! git -C "${REPOSITORY_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf '[env-templates] not a Git worktree\n' >&2
  exit 2
fi

is_env_template() {
  local basename="$1"
  case "${basename}" in
    .env.example|.env.template|.env.*.example|.env.*.template|*.env.example|*.env.template|*.env.*.example|*.env.*.template)
      return 0
      ;;
    *) return 1 ;;
  esac
}

report_failure() {
  local path="$1" rule="$2" category="$3"
  printf '[env-templates] FAIL path=' >&2
  printf '%q' "${path}" >&2
  printf ' RuleID=%s category=%s\n' "${rule}" "${category}" >&2
}

template_count=0
key_count=0
failure_count=0

while IFS= read -r -d '' path; do
  basename="${path##*/}"
  if ! is_env_template "${basename}"; then
    continue
  fi
  template_count=$((template_count + 1))

  full_path="${REPOSITORY_ROOT}/${path}"
  if [[ ! -f "${full_path}" || -L "${full_path}" ]]; then
    report_failure "${path}" E001 non_regular_template
    failure_count=$((failure_count + 1))
    continue
  fi

  size="$(LC_ALL=C wc -c <"${full_path}" | tr -d '[:space:]')"
  if [[ ! "${size}" =~ ^[0-9]+$ ]] || [[ "${size}" -gt "${MAX_TEMPLATE_BYTES}" ]]; then
    report_failure "${path}" E010 template_too_large
    failure_count=$((failure_count + 1))
    continue
  fi

  result="$(LC_ALL=C awk '
    BEGIN { first_error = ""; count = 0 }
    {
      if (first_error != "") next
      if ($0 ~ /[[:cntrl:]]/) { first_error = "E020"; next }
      if ($0 ~ /^[ \t]*$/ || $0 ~ /^[ \t]*#/) next
      assignment = $0
      if (assignment ~ /^export /) assignment = substr(assignment, 8)
      separator = index(assignment, "=")
      if (separator == 0) { first_error = "E020"; next }
      key = substr(assignment, 1, separator - 1)
      if (key !~ /^[A-Za-z_][A-Za-z0-9_]*$/) { first_error = "E030"; next }
      if (seen[key]) { first_error = "E040"; next }
      seen[key] = 1
      count++
    }
    END {
      if (first_error != "") print "FAIL " first_error
      else if (count == 0) print "FAIL E050"
      else print "PASS " count
    }
  ' "${full_path}")"

  case "${result}" in
    "FAIL E020")
      report_failure "${path}" E020 invalid_assignment_syntax
      failure_count=$((failure_count + 1))
      ;;
    "FAIL E030")
      report_failure "${path}" E030 invalid_key_syntax
      failure_count=$((failure_count + 1))
      ;;
    "FAIL E040")
      report_failure "${path}" E040 duplicate_key
      failure_count=$((failure_count + 1))
      ;;
    "FAIL E050")
      report_failure "${path}" E050 no_declared_keys
      failure_count=$((failure_count + 1))
      ;;
    "PASS "*)
      count="${result#PASS }"
      key_count=$((key_count + count))
      printf '[env-templates] PASS path='
      printf '%q' "${path}"
      printf ' keys=%s\n' "${count}"
      ;;
    *)
      report_failure "${path}" E020 invalid_assignment_syntax
      failure_count=$((failure_count + 1))
      ;;
  esac
done < <(git -C "${REPOSITORY_ROOT}" ls-files -z --cached)

if [[ "${template_count}" -eq 0 ]]; then
  printf '[env-templates] FAIL RuleID=E060 category=no_tracked_env_templates\n' >&2
  exit 1
fi
if [[ "${failure_count}" -ne 0 ]]; then
  printf '[env-templates] FAIL templates=%s failures=%s values_printed=false\n' "${template_count}" "${failure_count}" >&2
  exit 1
fi

printf '[env-templates] PASS templates=%s keys=%s values_printed=false compared_to_local_env=false\n' "${template_count}" "${key_count}"
