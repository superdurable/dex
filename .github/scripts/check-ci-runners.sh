#!/usr/bin/env bash

set -euo pipefail

workflow_dir="${1:-.github/workflows}"
hosted_runner_pattern='^[[:space:]]*runs-on:[[:space:]]+((ubuntu|macos|windows)-[A-Za-z0-9.-]+|\$\{\{[[:space:]]*matrix\.(os|runner|platform\.runner)[[:space:]]*\}\})[[:space:]]*$'
failures=0

shopt -s nullglob
workflows=("$workflow_dir"/*.yml "$workflow_dir"/*.yaml)

if (( ${#workflows[@]} == 0 )); then
  echo "No workflows found in $workflow_dir."
  exit 1
fi

for workflow in "${workflows[@]}"; do
  while IFS=: read -r line_number runner_line; do
    if [[ ! "$runner_line" =~ $hosted_runner_pattern ]]; then
      echo "$workflow:$line_number must use a GitHub-hosted runner or a hosted runner matrix."
      failures=$((failures + 1))
    fi
  done < <(grep -nE '^[[:space:]]*runs-on:' "$workflow" || true)

  if grep -nE 'self-hosted|linux-dind' "$workflow"; then
    echo "$workflow must not reference retired self-hosted runners."
    failures=$((failures + 1))
  fi
done

if (( failures > 0 )); then
  exit 1
fi

echo "All workflows use GitHub-hosted runners."
