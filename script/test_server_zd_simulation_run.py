# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Exercise nightly process supervision and release instrumentation through the CLI."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


REPOSITORY = Path(__file__).resolve().parent.parent
RUNNER = REPOSITORY / "script/server-zd-simulation-run.py"


class SimulationRunnerIntegrationTests(unittest.TestCase):
    def run_command(self, source, expected_status, expected_result, expected_seed="none", options=()):
        with tempfile.TemporaryDirectory() as directory:
            output_path = Path(directory) / "outputs"
            log_path = Path(directory) / "simulation.log"
            result = subprocess.run(
                [sys.executable, str(RUNNER), *options, "--timeout", "1", "--log", str(log_path),
                 "--", sys.executable, "-u", "-c", source],
                env={**os.environ, "GITHUB_OUTPUT": str(output_path),
                     "DEXZD_SIMULATION_BASE_SEED": "42"},
                capture_output=True, text=True, timeout=10, check=False,
            )
            self.assertEqual(result.returncode, expected_status, result.stdout + result.stderr)
            outputs = dict(line.split("=", 1) for line in output_path.read_text().splitlines())
            self.assertEqual(outputs["result"], expected_result)
            self.assertEqual(outputs["last_completed_seed"], expected_seed)
            self.assertIn("base seed: 42", result.stdout)
            self.assertIn(log_path.read_text(), result.stdout)
            return result.stdout

    def test_success_reports_last_seed(self):
        self.run_command(
            'print("simulation completed seed: 42"); print("simulation completed seed: 43")',
            0, "success", "43",
        )

    def test_timeout_reports_progress_and_kills_descendants(self):
        output = self.run_command(
            'import subprocess, sys, time; '
            'child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"]); '
            'print(f"child pid: {child.pid}"); '
            'print("simulation completed seed: 42"); time.sleep(30)',
            124, "timeout", "42",
        )
        child_pid = output.split("child pid: ")[1].splitlines()[0]
        status = subprocess.run(
            ["ps", "-p", child_pid, "-o", "stat="],
            capture_output=True, text=True, check=False,
        )
        self.assertTrue(status.returncode == 1 or status.stdout.strip().startswith("Z"), status.stdout)

    def test_timeout_before_first_seed(self):
        self.run_command("import time; time.sleep(30)", 124, "timeout")

    def test_timeout_allows_nested_session_owner_to_clean_up(self):
        output = self.run_command(
            'import os, signal, subprocess, sys, time\n'
            'child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"], '
            'start_new_session=True)\n'
            'def stop(signal_number, frame):\n'
            '    os.killpg(child.pid, signal.SIGKILL)\n'
            '    child.wait()\n'
            '    print("nested session stopped")\n'
            '    sys.exit(0)\n'
            'signal.signal(signal.SIGTERM, stop)\n'
            'time.sleep(30)\n',
            124, "timeout",
        )
        self.assertIn("nested session stopped", output)

    def test_invariant_failure_is_distinct_from_command_failure(self):
        self.run_command(
            'print("seed 42 broke an invariant at command index 5: A2-apply-twice: mismatch"); '
            'raise SystemExit(101)', 101, "invariant failure",
        )
        self.run_command('print("compiler error"); raise SystemExit(7)', 7, "command failure")

    def test_nested_acceptance_timeout(self):
        self.run_command('print("Timed out after 300.1s"); raise SystemExit(1)', 1, "timeout")

    def test_expected_mutant_failure_does_not_misclassify_acceptance_failure(self):
        self.run_command(
            'print("seed 42 broke an invariant at command index 5: A2-apply-twice: mismatch"); '
            'print("1 detection gap"); raise SystemExit(1)',
            1, "acceptance failure", options=("--acceptance",),
        )

    def test_older_release_progress_is_idempotent_and_fails_on_unknown_source(self):
        with tempfile.TemporaryDirectory() as directory:
            test_directory = Path(directory) / "crates/dexzd-simulation/tests"
            test_directory.mkdir(parents=True)
            paths = [test_directory / filename for filename in ("apply_invariants.rs", "engine_simulation.rs")]
            for path in paths:
                path.write_text("match result { Ok(summary) => total.merge(&summary), }")
            for _ in range(2):
                subprocess.run([sys.executable, str(RUNNER), "--prepare-progress"],
                               cwd=directory, check=True)
            for path in paths:
                source = path.read_text()
                self.assertEqual(source.count("simulation completed seed:"), 1)
                self.assertIn("total.merge(&summary);", source)
            paths[0].write_text("unsupported test loop")
            result = subprocess.run([sys.executable, str(RUNNER), "--prepare-progress"],
                                    cwd=directory, capture_output=True, check=False)
            self.assertNotEqual(result.returncode, 0)

    def test_workflow_partitions_all_requested_seeds(self):
        workflow = (REPOSITORY / ".github/workflows/server-zd-simulation-nightly.yml").read_text()
        source = workflow.split("python3 - <<'PYTHON'\n", 1)[1].split("          PYTHON", 1)[0]
        source = "\n".join(line[10:] for line in source.splitlines())
        for apply_count, engine_count in ((1, 64), (500, 250), (1000, 1000), (101, 127)):
            with tempfile.TemporaryDirectory() as directory:
                output_path = Path(directory) / "outputs"
                subprocess.run(
                    [sys.executable, "-c", source],
                    env={**os.environ, "APPLY_SEEDS": str(apply_count),
                         "ENGINE_SEEDS": str(engine_count), "GITHUB_OUTPUT": str(output_path)},
                    check=True,
                )
                outputs = dict(line.split("=", 1) for line in output_path.read_text().splitlines())
                for suite, count in (("apply", apply_count), ("engine", engine_count)):
                    shards = json.loads(outputs[f"{suite}_shards"])
                    offsets = [offset for shard in shards
                               for offset in range(shard["offset"], shard["offset"] + shard["count"])]
                    self.assertEqual(offsets, list(range(count)))
                    self.assertLessEqual(max(shard["count"] for shard in shards), 127 if suite == "engine" else 100)
                    if suite == "engine":
                        self.assertGreaterEqual(min(shard["count"] for shard in shards), 64)


if __name__ == "__main__":
    unittest.main()
