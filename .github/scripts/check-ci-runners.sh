#!/usr/bin/env bash

set -euo pipefail

workflow_dir="${1:-.github/workflows}"
runner_policy="runs-on: \${{ github.ref == 'refs/heads/main' && github.event_name != 'pull_request' && 'linux-dind-"
failures=0

shopt -s nullglob
workflows=("$workflow_dir"/*-ci.yml "$workflow_dir"/*-ci.yaml)

if (( ${#workflows[@]} == 0 )); then
  echo "No CI workflows found in $workflow_dir."
  exit 1
fi

for workflow in "${workflows[@]}"; do
  case "$(basename "$workflow")" in
    check-gate-ci.yml|cla-ci.yml)
      continue
      ;;
  esac

  while IFS=: read -r line_number runner_line; do
    if [[ "$runner_line" != *"$runner_policy"* ]]; then
      echo "$workflow:$line_number must route main push and manual jobs to a role-specific runner."
      failures=$((failures + 1))
    fi
  done < <(grep -nE '^[[:space:]]*runs-on:' "$workflow" || true)
done

if (( failures > 0 )); then
  exit 1
fi

echo "All CI jobs route main push and manual jobs to role-specific runners."
