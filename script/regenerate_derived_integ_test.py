# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("regenerate_derived.py")
LOCK = "server-zd/Cargo.lock"
PIN_FILE = "server-zd/crates/dexzd-engine/tests/format_compat/golden_replay.rs"
SEEDS = "server-zd/crates/dexzd-simulation/regression-seeds.txt"
FLOW_DEFINITION = "docs/src/data/flow-definitions/order.json"
STUB_GENERATOR = f"{sys.executable} -c \"open('{LOCK}', 'a').write('regenerated\\n')\""


class RegenerateDerivedIntegrationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repository = Path(self.temporary_directory.name)
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Derived Test")
        self.git("config", "user.email", "derived-test@example.com")
        self.write(LOCK, "base\n")
        self.write(SEEDS, "1 100\n")
        self.write(FLOW_DEFINITION, "{}\n")
        self.write("server-zd/Cargo.toml", "[workspace]\n")
        self.write(PIN_FILE, pin_file("v2", "0" * 32, "fn test() {}"))
        self.commit("initial")
        self.git("switch", "-c", "feature")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_merge_conflict_takes_upstream_copy_then_regenerates_and_stages(self) -> None:
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)
        self.assertIn(LOCK, self.unmerged())

        result = self.regenerate("--generator-command", f"cargo-lock:server-zd={STUB_GENERATOR}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read(LOCK), "main\nregenerated\n")
        self.assertEqual(self.unmerged(), [])
        self.assertEqual(self.git("show", f":{LOCK}").stdout, "main\nregenerated\n")

    def test_rebase_conflict_takes_upstream_copy_rather_than_ours(self) -> None:
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("rebase", "main", check=False)
        self.assertIn(LOCK, self.unmerged())

        result = self.regenerate("--generator-command", f"cargo-lock:server-zd={STUB_GENERATOR}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read(LOCK), "main\nregenerated\n")
        self.git("-c", "core.editor=true", "rebase", "--continue")
        self.assertEqual(self.git("show", f"HEAD:{LOCK}").stdout, "main\nregenerated\n")

    def test_changed_file_without_conflict_keeps_branch_copy(self) -> None:
        self.write(LOCK, "branch\n")
        self.commit("branch lock")

        result = self.regenerate("--generator-command", f"cargo-lock:server-zd={STUB_GENERATOR}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read(LOCK), "branch\nregenerated\n")
        self.assertEqual(self.git("diff", "--cached", "--name-only").stdout, "")

    def test_only_affected_groups_run_unless_all(self) -> None:
        stub = "flow-definitions=" + f"{sys.executable} -c \"open('ran', 'w').write('yes')\""

        result = self.regenerate("--generator-command", stub)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.repository / "ran").exists())

        overrides = [
            argument
            for group in ("cargo-lock:server-zd", "cargo-lock:sdk-rust", "cargo-lock:s3local",
                          "cargo-lock:examples/rust", "generated-code", "format-compat")
            for argument in ("--generator-command", f"{group}=true")
        ]
        result = self.regenerate("--all", "--generator-command", stub, *overrides)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read("ran"), "yes")

    def test_dry_run_prints_plan_and_changes_nothing(self) -> None:
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)
        before = self.read(LOCK)

        result = self.regenerate("--dry-run", "--generator-command", f"cargo-lock:server-zd={STUB_GENERATOR}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("cargo-lock:server-zd: regenerate", result.stdout)
        self.assertIn(f"conflicted, take main copy: {LOCK}", result.stdout)
        self.assertEqual(self.read(LOCK), before)
        self.assertIn(LOCK, self.unmerged())

    def test_conflict_in_non_derived_path_blocks_every_change(self) -> None:
        self.write("server-zd/Cargo.toml", "[workspace]\nmembers = []\n")
        self.write(LOCK, "branch\n")
        self.commit("branch")
        self.git("switch", "main")
        self.write("server-zd/Cargo.toml", "[workspace]\nresolver = \"2\"\n")
        self.write(LOCK, "main\n")
        self.commit("main")
        self.git("switch", "feature")
        self.git("merge", "main", check=False)
        before = self.read(LOCK)

        result = self.regenerate("--generator-command", f"cargo-lock:server-zd={STUB_GENERATOR}")

        self.assertEqual(result.returncode, 1)
        self.assertIn("not derived files", result.stdout)
        self.assertIn("server-zd/Cargo.toml", result.stdout)
        self.assertEqual(self.read(LOCK), before)

    def test_simulation_pin_conflict_points_to_the_readme_procedure(self) -> None:
        self.diverge(SEEDS, upstream="1 100\n2 100\n", branch="1 100\n3 100\n")
        self.git("merge", "main", check=False)

        result = self.regenerate()

        self.assertEqual(result.returncode, 1)
        self.assertIn("Pinned trajectories", result.stdout)
        self.assertIn(SEEDS, self.unmerged())

    def test_format_compat_pin_conflict_resolves_only_inside_the_pin_block(self) -> None:
        self.diverge(
            PIN_FILE,
            upstream=pin_file("v2", "1" * 32, "fn test() {}"),
            branch=pin_file("v2", "2" * 32, "fn test() {}"),
        )
        self.git("merge", "main", check=False)

        result = self.regenerate("--generator-command", "format-compat=true")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read(PIN_FILE), pin_file("v2", "1" * 32, "fn test() {}"))
        self.assertEqual(self.unmerged(), [])

    def test_format_compat_conflict_outside_the_pin_block_is_refused(self) -> None:
        self.diverge(
            PIN_FILE,
            upstream=pin_file("v2", "1" * 32, "fn upstream_test() {}"),
            branch=pin_file("v2", "2" * 32, "fn branch_test() {}"),
        )
        self.git("merge", "main", check=False)

        result = self.regenerate("--generator-command", "format-compat=true")

        self.assertEqual(result.returncode, 1)
        self.assertIn("conflicts outside PINNED_VERSION_DIGESTS", result.stdout)
        self.assertIn(PIN_FILE, self.unmerged())

    def diverge(self, path: str, upstream: str, branch: str) -> None:
        self.write(path, branch)
        self.commit("branch change")
        self.git("switch", "main")
        self.write(path, upstream)
        self.commit("upstream change")
        self.git("switch", "feature")

    def regenerate(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            (sys.executable, str(SCRIPT), "--upstream", "main", *arguments),
            cwd=self.repository,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def unmerged(self) -> list[str]:
        output = self.git("diff", "--name-only", "--diff-filter=U").stdout
        return [line for line in output.splitlines() if line]

    def write(self, relative_path: str, content: str) -> None:
        path = self.repository / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def read(self, relative_path: str) -> str:
        return (self.repository / relative_path).read_text(encoding="utf-8")

    def commit(self, message: str) -> None:
        self.git("add", "--all")
        self.git("commit", "--quiet", "-m", message)

    def git(self, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("git", *arguments),
            cwd=self.repository,
            check=check,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )


def pin_file(version: str, digest: str, test_body: str) -> str:
    return (
        "const PINNED_VERSION_DIGESTS: &[(&str, &str)] = &[\n"
        f'    ("v1", "{"a" * 32}"),\n'
        f'    ("{version}", "{digest}"),\n'
        "];\n"
        "\n"
        "#[test]\n"
        f"{test_body}\n"
    )


if __name__ == "__main__":
    unittest.main()
