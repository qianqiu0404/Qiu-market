#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ ! -x "$repo_root/ops/macos/manage-runtime-release.sh" ]; then
  echo "manage-runtime-release.sh must be executable in the release checkout." >&2
  exit 1
fi
fixture_dir="$(mktemp -d /tmp/qiu-market-release-candidate.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

fixture_repo="$fixture_dir/repo"
support_dir="$fixture_dir/support"
mkdir -p "$fixture_repo" "$support_dir"
git -C "$repo_root" archive HEAD | tar -xf - -C "$fixture_repo"

# Overlay the implementation under test so this fixture also works before the
# parent worktree commits the current change.
mkdir -p "$fixture_repo/ops/macos/fixtures/release-candidate-bin"
for file in \
  ops/macos/fixtures/release-candidate-bin/release-tool \
  ops/macos/fixtures/release-candidate-bin/runtime-tool \
  ops/macos/manage-release-candidate.sh \
  ops/macos/manage-runtime-release.sh \
  ops/macos/production-lib.sh \
  ops/macos/release-production.sh; do
  install -m 700 "$repo_root/$file" "$fixture_repo/$file"
done

git -C "$fixture_repo" init -q
git -C "$fixture_repo" config user.name qiu-market-fixture
git -C "$fixture_repo" config user.email fixture@invalid
git -C "$fixture_repo" add .
git -C "$fixture_repo" commit -qm fixture
commit="$(git -C "$fixture_repo" rev-parse HEAD)"
baseline_runtime_commit="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

# prepare must not source production credentials. This file would terminate a
# shell if read; the artifact-only phase must remain independent of it.
printf 'return 97\n' > "$support_dir/production.env"
chmod 600 "$support_dir/production.env"

export QIU_MARKET_SUPPORT_DIR="$support_dir"
manager="$fixture_repo/ops/macos/manage-release-candidate.sh"
release_tool="$fixture_repo/ops/macos/release-production.sh"
runtime_tool="$fixture_repo/ops/macos/manage-runtime-release.sh"

"$manager" prepare "$commit" > "$fixture_dir/prepare.out"

binary_manifest="$support_dir/releases/$commit/manifest.env"
runtime_manifest="$support_dir/runtime-releases/$commit/runtime-manifest.env"
test -x "$support_dir/releases/$commit/market-services"
test -d "$support_dir/releases/$commit/migrations"
test "$(sed -n 's/^schema_version=//p' "$binary_manifest")" = 2
test "$(sed -n 's/^git_commit=//p' "$binary_manifest")" = "$commit"
test "$(sed -n 's/^git_commit=//p' "$runtime_manifest")" = "$commit"
expected_last_migration="$(find "$fixture_repo/migrations" -maxdepth 1 -name '*.sql' -print | sort | tail -1 | xargs basename)"
test "$(sed -n 's/^migration_last=//p' "$binary_manifest")" = "$expected_last_migration"
test "$(sed -n 's/^migration_count=//p' "$binary_manifest")" = \
  "$(find "$support_dir/releases/$commit/migrations" -maxdepth 1 -name '*.sql' | wc -l | tr -d ' ')"
"$release_tool" artifact "$commit" >/dev/null
"$runtime_tool" verify "$commit" >/dev/null

if "$manager" activate "$commit" >/dev/null 2>&1; then
  echo "Candidate activation unexpectedly ran without --execute." >&2
  exit 1
fi
if "$release_tool" deploy "$commit" >/dev/null 2>&1; then
  echo "Binary deployment unexpectedly ran without --execute." >&2
  exit 1
fi
if "$runtime_tool" activate "$commit" >/dev/null 2>&1; then
  echo "Runtime activation unexpectedly ran without --execute." >&2
  exit 1
fi
if "$release_tool" deploy "$commit" --execute >/dev/null 2>&1; then
  echo "Direct binary deployment bypassed the candidate coordinator." >&2
  exit 1
fi
if "$runtime_tool" activate "$commit" --execute >/dev/null 2>&1; then
  echo "Direct runtime activation bypassed the candidate coordinator." >&2
  exit 1
fi
if "$release_tool" rollback "/tmp/qiu-market-fixture-previous" --execute >/dev/null 2>&1; then
  echo "Direct binary rollback bypassed the candidate coordinator." >&2
  exit 1
fi
if QIU_MARKET_CANDIDATE_COORDINATED=true \
  "$release_tool" deploy "$commit" --execute >/dev/null 2>&1; then
  echo "Legacy boolean coordination bypassed the one-time token gate." >&2
  exit 1
fi

# The coordinator path is exercised with child side effects mocked. The mock
# children still call the production token verifier and must consume one exact
# context per action.
fixture_release_tool="$fixture_repo/ops/macos/fixtures/release-candidate-bin/release-tool"
fixture_runtime_tool="$fixture_repo/ops/macos/fixtures/release-candidate-bin/runtime-tool"
chmod 700 "$fixture_release_tool" "$fixture_runtime_tool"
mkdir -p "$support_dir/bin" "$support_dir/releases/baseline" \
  "$support_dir/runtime-releases/$baseline_runtime_commit"
printf '#!/usr/bin/env bash\nexit 0\n' \
  > "$support_dir/releases/baseline/market-services"
chmod 700 "$support_dir/releases/baseline/market-services"
ln -s "$support_dir/releases/baseline/market-services" \
  "$support_dir/bin/market-services"
ln -s "$support_dir/runtime-releases/$baseline_runtime_commit" "$support_dir/runtime-current"
export QIU_MARKET_RELEASE_TEST_MODE=true
export QIU_MARKET_RELEASE_TOOL="$fixture_release_tool"
export QIU_MARKET_RUNTIME_TOOL="$fixture_runtime_tool"
export QIU_MARKET_TEST_PRODUCTION_LIB="$fixture_repo/ops/macos/production-lib.sh"
export QIU_MARKET_RELEASE_FIXTURE_LOG="$fixture_dir/coordinated.log"
"$manager" activate "$commit" --execute >/dev/null
grep -Fx "release:deploy:$commit" "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx "runtime:activate:$commit" "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx 'phase=active' "$support_dir/release-state/candidate-activation.env" >/dev/null
test ! -e "$support_dir/release-state/candidate-activation.lock/context.env"
> "$QIU_MARKET_RELEASE_FIXTURE_LOG"
export QIU_MARKET_RELEASE_FIXTURE_FAIL_RUNTIME_SUBJECTS="$commit"
if "$manager" activate "$commit" --execute >/dev/null 2>&1; then
  echo "Coordinator accepted a failed runtime activation." >&2
  exit 1
fi
unset QIU_MARKET_RELEASE_FIXTURE_FAIL_RUNTIME_SUBJECTS
grep -Fx "runtime:activate-failed:$commit" "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx "release:rollback:$support_dir/releases/baseline/market-services" \
  "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx "runtime:activate:$baseline_runtime_commit" "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx 'phase=rolled-back-after-runtime-failure' \
  "$support_dir/release-state/candidate-activation.env" >/dev/null
test ! -e "$support_dir/release-state/candidate-activation.lock"

> "$QIU_MARKET_RELEASE_FIXTURE_LOG"
export QIU_MARKET_RELEASE_FIXTURE_FAIL_RUNTIME_SUBJECTS="$commit,$baseline_runtime_commit"
if "$manager" activate "$commit" --execute >"$fixture_dir/compensation-failed.out" 2>&1; then
  echo "Coordinator accepted a failed previous-runtime compensation." >&2
  exit 1
fi
unset QIU_MARKET_RELEASE_FIXTURE_FAIL_RUNTIME_SUBJECTS
grep -Fx "runtime:activate-failed:$commit" "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx "release:rollback:$support_dir/releases/baseline/market-services" \
  "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx "runtime:activate-failed:$baseline_runtime_commit" \
  "$QIU_MARKET_RELEASE_FIXTURE_LOG" >/dev/null
grep -Fx 'phase=compensation-failed' \
  "$support_dir/release-state/candidate-activation.env" >/dev/null
grep -F 'CRITICAL:' "$fixture_dir/compensation-failed.out" >/dev/null
test ! -e "$support_dir/release-state/candidate-activation.lock"
unset QIU_MARKET_RELEASE_TEST_MODE QIU_MARKET_RELEASE_TOOL \
  QIU_MARKET_RUNTIME_TOOL QIU_MARKET_TEST_PRODUCTION_LIB \
  QIU_MARKET_RELEASE_FIXTURE_LOG

printf '\nfixture dirty marker\n' >> "$fixture_repo/README.md"
if "$manager" prepare "$commit" >/dev/null 2>&1; then
  echo "Candidate preparation accepted a dirty worktree." >&2
  exit 1
fi
git -C "$fixture_repo" checkout -q -- README.md

migration="$support_dir/releases/$commit/migrations/$expected_last_migration"
cp "$migration" "$migration.original"
printf '\n-- tampered\n' >> "$migration"
if "$release_tool" artifact "$commit" >/dev/null 2>&1; then
  echo "Release verification accepted a tampered migration set." >&2
  exit 1
fi
mv "$migration.original" "$migration"
"$release_tool" artifact "$commit" >/dev/null

runtime_file="$support_dir/runtime-releases/$commit/ops/macos/guardian.sh"
cp "$runtime_file" "$runtime_file.original"
printf '\n# tampered\n' >> "$runtime_file"
if "$runtime_tool" verify "$commit" >/dev/null 2>&1; then
  echo "Runtime verification accepted a tampered bundle." >&2
  exit 1
fi
mv "$runtime_file.original" "$runtime_file"
"$runtime_tool" verify "$commit" >/dev/null

echo "Qiu Market immutable release candidate fixtures passed."
