#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
validate="$root/script/validate-branch-off-main.sh"
hook="$root/.cursor/hooks/validate-branch-off-main.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

setup_repo() {
  git init -b main "$tmp/upstream" >/dev/null
  (
    cd "$tmp/upstream"
    git config user.email "test@example.com"
    git config user.name "Test"
    echo one >file
    git add file
    git commit -m "main" >/dev/null
  )

  git clone -q "$tmp/upstream" "$tmp/clone"
  (
    cd "$tmp/clone"
    git remote rename origin upstream
    git remote add origin "$tmp/upstream"
    git fetch origin main >/dev/null
  )
}

run_cursor_hook() {
  local command=$1
  DEX_VALIDATE_BRANCH_SCRIPT="$validate" \
    printf '{"command":"%s"}' "$command" | sh "$hook"
}

setup_repo
cd "$tmp/clone"

if ! "$validate" validate-start origin/main; then
  echo "expected origin/main to validate" >&2
  exit 1
fi

git switch -q main
old=$(git rev-parse origin/main)
(
  cd "$tmp/upstream"
  echo two >>file
  git add file
  git commit -m "advance main" >/dev/null
)
git fetch origin main >/dev/null
git switch -q -c stale "$old"
if "$validate" validate-head; then
  echo "expected stale HEAD to fail validate-head" >&2
  exit 1
fi

git switch -q main
git switch -q -c feature origin/main
echo two >>file
git add file
git commit -m "feature" >/dev/null
if ! "$validate" validate-includes-main; then
  echo "expected feature branch to include origin/main" >&2
  exit 1
fi

old=$(git rev-parse main)
git commit --allow-empty -m "advance main" >/dev/null
git switch -q -c behind "$old"
if "$validate" validate-includes-main; then
  echo "expected behind branch push check to fail" >&2
  exit 1
fi

git switch -q main
output=$(run_cursor_hook "git switch -c blocked")
if ! echo "$output" | grep -q '"permission":"deny"'; then
  echo "expected cursor hook to deny branch create while behind main: $output" >&2
  exit 1
fi

output=$(run_cursor_hook "git switch -c allowed origin/main")
if ! echo "$output" | grep -q '"permission":"allow"'; then
  echo "expected cursor hook to allow origin/main branch create: $output" >&2
  exit 1
fi

echo "validate-branch-off-main integration tests passed"
