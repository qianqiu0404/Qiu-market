#!/usr/bin/env bash
set -Eeuo pipefail

REPOSITORY_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
COMPARISON_REF="${1:-refs/remotes/origin/main}"

if ! git -C "${REPOSITORY_ROOT}" rev-parse --verify "${COMPARISON_REF}^{commit}" >/dev/null 2>&1; then
  printf '[repo-audit] comparison ref is unavailable: %s\n' "${COMPARISON_REF}" >&2
  printf '[repo-audit] pass an existing local commit/ref; this command never fetches.\n' >&2
  exit 2
fi

comparison_sha="$(git -C "${REPOSITORY_ROOT}" rev-parse "${COMPARISON_REF}^{commit}")"
repository_head="$(git -C "${REPOSITORY_ROOT}" rev-parse HEAD)"

printf 'repository=%s\n' "${REPOSITORY_ROOT}"
printf 'head=%s\n' "${repository_head}"
printf 'comparison_ref=%s\n' "${COMPARISON_REF}"
printf 'comparison_sha=%s\n' "${comparison_sha}"
printf '\n%-68s %-42s %-12s %-14s %-12s %s\n' \
  WORKTREE BRANCH HEAD DIRTY_ENTRIES RELATION LAST_COMMIT

worktree_total=0
live_total=0
prunable_total=0
clean_total=0
dirty_total=0
ancestor_total=0
not_ancestor_total=0

while IFS= read -r worktree_dir; do
  worktree_total=$((worktree_total + 1))
  if [[ ! -d "${worktree_dir}" ]]; then
    prunable_total=$((prunable_total + 1))
    printf '%-68s %-42s %-12s %-14s %-12s %s\n' \
      "${worktree_dir}" - - - prunable -
    continue
  fi

  live_total=$((live_total + 1))
  worktree_branch="$(git -C "${worktree_dir}" symbolic-ref --quiet --short HEAD 2>/dev/null || printf detached)"
  worktree_head="$(git -C "${worktree_dir}" rev-parse HEAD)"
  worktree_dirty_count="$(git -C "${worktree_dir}" status --porcelain=v1 | wc -l | tr -d ' ')"
  worktree_last_commit="$(git -C "${worktree_dir}" show -s --format='%cs' "${worktree_head}")"

  if [[ "${worktree_dirty_count}" -eq 0 ]]; then
    clean_total=$((clean_total + 1))
  else
    dirty_total=$((dirty_total + 1))
  fi

  if git -C "${REPOSITORY_ROOT}" merge-base --is-ancestor "${worktree_head}" "${comparison_sha}"; then
    worktree_relation=ancestor
    ancestor_total=$((ancestor_total + 1))
  else
    worktree_relation=not-ancestor
    not_ancestor_total=$((not_ancestor_total + 1))
  fi

  printf '%-68s %-42s %-12s %-14s %-12s %s\n' \
    "${worktree_dir}" \
    "${worktree_branch}" \
    "${worktree_head:0:12}" \
    "${worktree_dirty_count}" \
    "${worktree_relation}" \
    "${worktree_last_commit}"
done < <(
  git -C "${REPOSITORY_ROOT}" worktree list --porcelain |
    awk '/^worktree / {sub(/^worktree /, ""); print}'
)

printf '\nsummary registered=%s live=%s prunable=%s clean=%s dirty=%s ancestor=%s not_ancestor=%s\n' \
  "${worktree_total}" \
  "${live_total}" \
  "${prunable_total}" \
  "${clean_total}" \
  "${dirty_total}" \
  "${ancestor_total}" \
  "${not_ancestor_total}"
printf 'note=ancestor is commit reachability only; it does not prove dirty files, squashed patches, PR state, deployment, or cleanup safety.\n'
