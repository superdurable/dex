#!/usr/bin/env bash
# Reruns points of a recorded result set with its recorded settings.
# Usage: reproduce.sh RESULTS_DIR [VARIANT [CONCURRENCY]]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
results_dir="${1:?usage: reproduce.sh RESULTS_DIR [VARIANT [CONCURRENCY]]}"
variant="${2:-}"
point_concurrency="${3:-}"
[[ -f "$results_dir/config.env" && -f "$results_dir/points.txt" ]] || {
  echo "$results_dir has no config.env and points.txt" >&2
  exit 2
}

set -a
# shellcheck source=/dev/null
source "$results_dir/config.env"
set +a

points_file="$(mktemp)"
trap 'rm -f "$points_file"' EXIT
awk -v variant="$variant" -v concurrency="$point_concurrency" \
  '(variant == "" || $1 == variant) && (concurrency == "" || $2 == concurrency)' \
  "$results_dir/points.txt" > "$points_file"
if [[ ! -s "$points_file" ]]; then
  echo "no recorded point matches '${variant:-any variant}' at concurrency '${point_concurrency:-any}'" >&2
  exit 2
fi
DEXBENCH_POINTS="$points_file" "$script_dir/run-remote.sh"
