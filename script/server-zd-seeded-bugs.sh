#!/usr/bin/env bash
set -euo pipefail

repository_root="$(git rev-parse --show-toplevel)"
patch_directory="$repository_root/server-zd/testdata/seeded-bugs"
scratch_root="$(mktemp -d "${TMPDIR:-/tmp}/dexzd-seeded-bugs.XXXXXX")"
baseline_worktree="$scratch_root/baseline"
mutant_worktree="$scratch_root/mutant"
baseline_target_directory="$scratch_root/targets/baseline"
bounded_runner="$scratch_root/run-bounded.py"
seed_count=64
commands_per_seed=400
engine_seed_count=64
redeploy_seed_count=16
engine_active_seconds=120
build_time_limit_seconds=1500
apply_time_limit_seconds=300
engine_time_limit_seconds=900

cleanup() {
  local original_status=$?
  local cleanup_status=0
  trap - EXIT
  for worktree in "$mutant_worktree" "$baseline_worktree"; do
    if [[ -f "$worktree/.git" ]] &&
      ! git -C "$repository_root" worktree remove --force "$worktree"; then
      cleanup_status=1
    fi
  done
  if ! rm -rf "$scratch_root"; then
    cleanup_status=1
  fi
  if [[ "$original_status" -eq 0 ]]; then
    exit "$cleanup_status"
  fi
  exit "$original_status"
}
trap cleanup EXIT

# Runs a command in a new process group, killing the group when it exceeds its limit.
cat >"$bounded_runner" <<'PY'
import contextlib
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

time_limit, log_path, working_directory, *rest = sys.argv[1:]
separator = rest.index("--")
assignments, command = rest[:separator], rest[separator + 1:]
environment = os.environ.copy()
# A caller's seed selection or report directory must not change the fixed budget.
for name in (
    "DEXZD_SIMULATION_SEED",
    "DEXZD_SIMULATION_BASE_SEED",
    "DEXZD_SIMULATION_FAILURE_DIR",
    "DEXZD_SIMULATION_PRINT_EVENTS",
):
    environment.pop(name, None)
environment.update(assignment.split("=", 1) for assignment in assignments)
# The runner greps cargo's status lines, which color codes would split.
environment["CARGO_TERM_COLOR"] = "never"
# Turns SIGTERM into SystemExit so the handler below kills cargo's separate session.
signal.signal(signal.SIGTERM, lambda signal_number, frame: sys.exit(128 + signal_number))
with Path(log_path).open("w") as output:
    started = time.monotonic()
    process = subprocess.Popen(
        command,
        cwd=working_directory,
        env=environment,
        stdout=output,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    try:
        status = process.wait(timeout=int(time_limit))
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()
        print(f"Timed out after {time.monotonic() - started:.1f}s")
        sys.exit(124)
    except BaseException:
        # An interrupted runner must not leave cargo writing into the scratch tree.
        with contextlib.suppress(ProcessLookupError):
            os.killpg(process.pid, signal.SIGKILL)
        process.wait()
        raise
print(f"Completed in {time.monotonic() - started:.1f}s with exit status {status}")
sys.exit(status)
PY

# Builds both simulation test targets of a tree; a mutant's target starts as a baseline copy.
build_simulation() {
  local source_directory="$1"
  local target_directory="$2"
  local log_path="$3"
  echo "Cargo target: $target_directory"
  python3 "$bounded_runner" "$build_time_limit_seconds" "$log_path" "$source_directory/server-zd" \
    CARGO_BUILD_JOBS=4 "CARGO_TARGET_DIR=$target_directory" -- \
    cargo test --locked -p dexzd-simulation --test apply_invariants --test engine_simulation --no-run
}

# Requires the build log to show `crate` compiled from the tree at `source_directory`.
require_fresh_compile() {
  local source_directory="$1"
  local crate="$2"
  local log_path="$3"
  local plain_log_path="$log_path.plain"
  local physical_path compile_lines
  physical_path="$(cd "$source_directory" && pwd -P)/server-zd/crates/$crate"
  # Removes ANSI escape sequences in case cargo colors its status lines anyway.
  if ! sed $'s/\033\\[[0-9;]*[A-Za-z]//g' "$log_path" >"$plain_log_path"; then
    echo "Runner error: could not read the build log $log_path" >&2
    exit 1
  fi
  if ! grep -Eq '^ *Compiling [^ ]+ v[0-9]' "$plain_log_path"; then
    echo "Runner error: the build log $log_path has no cargo Compiling lines, so fresh compiles cannot be verified." >&2
    echo "Check that cargo's status output is neither colored nor quiet. The log starts with:" >&2
    head -n 20 "$plain_log_path" >&2
    exit 1
  fi
  compile_lines="$(grep -F "Compiling $crate " "$plain_log_path" || true)"
  if [[ "$compile_lines" != *"$physical_path"* &&
    "$compile_lines" != *"$source_directory/server-zd/crates/$crate"* ]]; then
    echo "The build did not freshly compile $crate from $source_directory. Its Compiling lines:" >&2
    echo "${compile_lines:-(none)}" >&2
    return 1
  fi
}

# Runs one simulation suite (apply, engine, or redeploy) with its fixed budget.
run_suite() {
  local suite="$1"
  local source_directory="$2"
  local target_directory="$3"
  local log_path="$4"
  local time_limit="$engine_time_limit_seconds"
  local -a budget
  local test_target test_name
  case "$suite" in
    apply)
      time_limit="$apply_time_limit_seconds"
      budget=("DEXZD_SIMULATION_SEEDS=$seed_count" "DEXZD_SIMULATION_COMMANDS=$commands_per_seed")
      test_target=apply_invariants
      test_name=apply_keeps_every_invariant_over_random_command_sequences
      ;;
    engine)
      budget=("DEXZD_SIMULATION_SEEDS=$engine_seed_count" "DEXZD_SIMULATION_SECONDS=$engine_active_seconds")
      test_target=engine_simulation
      test_name=the_engine_keeps_every_invariant_through_faults_and_crashes
      ;;
    redeploy)
      budget=("DEXZD_SIMULATION_SEEDS=$redeploy_seed_count" "DEXZD_SIMULATION_SECONDS=$engine_active_seconds")
      test_target=engine_simulation
      test_name=redeploys_with_a_short_history_window_keep_every_invariant
      ;;
  esac
  python3 "$bounded_runner" "$time_limit" "$log_path" "$source_directory/server-zd" \
    CARGO_BUILD_JOBS=4 "CARGO_TARGET_DIR=$target_directory" "${budget[@]}" -- \
    cargo test --locked -p dexzd-simulation --test "$test_target" "$test_name" -- --exact --nocapture
}

describe_budget() {
  case "$1" in
    apply) echo "$seed_count seeds, $commands_per_seed commands per seed" ;;
    engine) echo "$engine_seed_count engine seeds, $engine_active_seconds simulated seconds per seed" ;;
    redeploy) echo "$redeploy_seed_count redeploy-profile seeds, $engine_active_seconds simulated seconds per seed" ;;
  esac
}

failure_pattern() {
  case "$1" in
    apply) echo '^seed [0-9]+ broke an invariant at command index [0-9]+: [A-Za-z0-9-]+:' ;;
    *) echo '^engine simulation seed [0-9]+ broke an invariant at simulated time [0-9]+ µs: [A-Za-z0-9-]+:' ;;
  esac
}

git -C "$repository_root" worktree add --detach "$baseline_worktree" HEAD >/dev/null
if ! build_simulation "$baseline_worktree" "$baseline_target_directory" "$scratch_root/baseline-build.log"; then
  echo "Unpatched simulation failed to build within ${build_time_limit_seconds}s:" >&2
  tail -n 35 "$scratch_root/baseline-build.log" >&2
  exit 1
fi
if ! require_fresh_compile "$baseline_worktree" dexzd-simulation "$scratch_root/baseline-build.log"; then
  echo "The unpatched build did not compile dexzd-simulation from the baseline worktree" >&2
  exit 1
fi
for suite in apply engine redeploy; do
  baseline_log="$scratch_root/baseline-$suite.log"
  echo "Checking unpatched simulation: $(describe_budget "$suite")"
  if ! run_suite "$suite" "$baseline_worktree" "$baseline_target_directory" "$baseline_log"; then
    echo "Unpatched $suite simulation failed or exceeded its time limit:" >&2
    tail -n 35 "$baseline_log" >&2
    exit 1
  fi
done
git -C "$repository_root" worktree remove --force "$baseline_worktree"

detected_count=0
gap_count=0
detections=()
for name in wall-clock hash-map-iteration immediate-reclaim retry-fresh-snapshot \
  ack-before-persist skip-epoch-check speculative-read-leak; do
  case "$name" in
    wall-clock) suite=apply; expected_invariants=(A2-apply-twice) ;;
    hash-map-iteration) suite=apply; expected_invariants=(A2-apply-twice A2-apply-redecoded) ;;
    immediate-reclaim) suite=apply; expected_invariants=(I1-window-value-reference) ;;
    retry-fresh-snapshot) suite=apply; expected_invariants=(S5-retained-snapshot) ;;
    ack-before-persist) suite=engine; expected_invariants=(acknowledged-write-lost) ;;
    skip-epoch-check) suite=redeploy; expected_invariants=(voided-batch-applied) ;;
    speculative-read-leak) suite=engine; expected_invariants=(speculative-read) ;;
  esac
  patch_path="$patch_directory/$name.patch"
  patched_crate="$(sed -n -E 's@^diff --git a/server-zd/crates/([^/]+)/.*@\1@p' "$patch_path" | head -n 1)"
  git -C "$repository_root" worktree add --detach "$mutant_worktree" HEAD >/dev/null
  if ! git -C "$mutant_worktree" apply --check "$patch_path"; then
    echo "Patch does not apply: $patch_path" >&2
    exit 1
  fi
  git -C "$mutant_worktree" apply "$patch_path"
  build_log="$scratch_root/$name-build.log"
  log_path="$scratch_root/$name.log"
  mutant_target_directory="$scratch_root/targets/$name"
  # Reuses third-party builds; workspace crates recompile from the mutant's path.
  # The copy keeps modification times, which cargo compares.
  cp -Rp "$baseline_target_directory" "$mutant_target_directory"
  echo "Scratch disk after copying the baseline target: $(df -h "$scratch_root" | tail -n 1)"
  echo "Checking $name: $(describe_budget "$suite")"
  if ! build_simulation "$mutant_worktree" "$mutant_target_directory" "$build_log"; then
    echo "The $name mutant failed to build:" >&2
    tail -n 35 "$build_log" >&2
    gap_count=$((gap_count + 1))
  elif ! require_fresh_compile "$mutant_worktree" "$patched_crate" "$build_log"; then
    gap_count=$((gap_count + 1))
  elif run_output="$(run_suite "$suite" "$mutant_worktree" "$mutant_target_directory" "$log_path")"; then
    echo "$run_output"
    echo "UNDETECTED: $name passed the simulation" >&2
    gap_count=$((gap_count + 1))
  else
    run_status=$?
    echo "$run_output"
    if [[ "$run_status" -eq 124 ]]; then
      echo "The $name run exceeded its time limit" >&2
      gap_count=$((gap_count + 1))
    elif ! failure_line="$(grep -m 1 -E "$(failure_pattern "$suite")" "$log_path")"; then
      echo "The $name run failed without a simulation invariant violation:" >&2
      tail -n 35 "$log_path" >&2
      gap_count=$((gap_count + 1))
    else
      observed_invariant="${failure_line#*: }"
      observed_invariant="${observed_invariant%%:*}"
      is_expected=false
      for expected_invariant in "${expected_invariants[@]}"; do
        if [[ "$observed_invariant" == "$expected_invariant" ]]; then
          is_expected=true
          break
        fi
      done
      if [[ "$is_expected" == true ]]; then
        echo "$failure_line"
        elapsed="${run_output#Completed in }"
        detections+=("$name in ${elapsed%% with*} ($suite suite): $failure_line")
        detected_count=$((detected_count + 1))
      else
        echo "The $name run broke a different invariant (expected ${expected_invariants[*]}):" >&2
        echo "$failure_line" >&2
        gap_count=$((gap_count + 1))
      fi
    fi
  fi
  git -C "$repository_root" worktree remove --force "$mutant_worktree"
  rm -rf "$mutant_target_directory"
done

for detection in ${detections[@]+"${detections[@]}"}; do
  echo "Detected $detection"
done
echo "Detected $detected_count seeded bugs; $gap_count gaps; unpatched simulations passed."
[[ "$gap_count" -eq 0 ]]
