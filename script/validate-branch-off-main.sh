#!/usr/bin/env bash
# Validate that feature branches start from and include the latest origin/main.
set -euo pipefail

MAIN_REMOTE=${MAIN_REMOTE:-origin}
MAIN_BRANCH=${MAIN_BRANCH:-main}
MAIN_REF="${MAIN_REMOTE}/${MAIN_BRANCH}"

usage() {
  cat <<EOF
Usage: $(basename "$0") <command>

Commands:
  fetch-main              Fetch ${MAIN_REF}
  resolve-main            Print ${MAIN_REF} SHA
  validate-start <ref>    Exit 0 when <ref> matches ${MAIN_REF}
  validate-head           Exit 0 when HEAD matches ${MAIN_REF}
EOF
}

in_git_repo() {
  git rev-parse --git-dir >/dev/null 2>&1
}

fetch_main() {
  if ! in_git_repo; then
    echo "validate-branch-off-main: not inside a git repository" >&2
    return 1
  fi
  git fetch "$MAIN_REMOTE" "$MAIN_BRANCH" --quiet
}

resolve_main() {
  git rev-parse "$MAIN_REF"
}

resolve_ref() {
  local ref=$1
  git rev-parse "$ref"
}

validate_start_ref() {
  local start_ref=$1
  fetch_main
  local main_sha start_sha
  main_sha=$(resolve_main)
  start_sha=$(resolve_ref "$start_ref")
  if [ "$start_sha" != "$main_sha" ]; then
    echo "Branch start $start_ref ($start_sha) is not ${MAIN_REF} ($main_sha)." >&2
    echo "Fetch and branch from ${MAIN_REF}:" >&2
    echo "  git fetch ${MAIN_REMOTE} ${MAIN_BRANCH}" >&2
    echo "  git switch -c <branch> ${MAIN_REF}" >&2
    return 1
  fi
}

validate_head() {
  validate_start_ref HEAD
}

main() {
  if [ $# -lt 1 ]; then
    usage >&2
    return 1
  fi
  case "$1" in
    fetch-main)
      fetch_main
      ;;
    resolve-main)
      fetch_main
      resolve_main
      ;;
    validate-start)
      shift
      [ $# -eq 1 ] || {
        echo "validate-start requires a ref" >&2
        return 1
      }
      validate_start_ref "$1"
      ;;
    validate-head)
      validate_head
      ;;
    *)
      usage >&2
      return 1
      ;;
  esac
}

main "$@"
