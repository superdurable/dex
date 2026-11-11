# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import regenerate_derived  # noqa: E402


SCRIPT = Path(__file__).with_name("regenerate_derived.py")
MAKEFILE = SCRIPT.parents[1] / "Makefile"
LOCK = "server-zd/Cargo.lock"
PIN_FILE = "server-zd/crates/dexzd-engine/tests/format_compat/golden_replay.rs"
OLD_STATE = "server-zd/testdata/v1/state.txt"
NEWEST_STATE = "server-zd/testdata/v2/state.txt"
SEEDS = "server-zd/crates/dexzd-simulation/regression-seeds.txt"
FLOW_DEFINITION = "docs/src/data/flow-definitions/order.json"
STUB_GENERATOR = f"{sys.executable} -c \"open('{LOCK}', 'a').write('regenerated\\n')\""
STATE_GENERATOR = f"{sys.executable} -c \"open('{NEWEST_STATE}', 'a').write('regenerated\\n')\""
LOCK_OVERRIDE = ("--generator-command", f"cargo-lock:server-zd={STUB_GENERATOR}")


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
        self.write(OLD_STATE, "old\n")
        self.write("server-zd/testdata/v2/README.md", "readme\n")
        self.write(NEWEST_STATE, "newest\n")
        self.write(PIN_FILE, pin_file("1" * 32, "fn test() {}"))
        self.commit("initial")
        self.git("switch", "-c", "feature")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_merge_conflict_takes_merge_head_copy_then_regenerates_and_stages(self) -> None:
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)
        self.assertIn(LOCK, self.unmerged())

        result = self.regenerate(*LOCK_OVERRIDE)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("(MERGE_HEAD)", result.stdout)
        self.assertEqual(self.read(LOCK), "main\nregenerated\n")
        self.assertEqual(self.unmerged(), [])
        self.assertEqual(self.git("show", f":{LOCK}").stdout, "main\nregenerated\n")

    def test_rebase_conflict_takes_onto_copy_rather_than_ours(self) -> None:
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("rebase", "main", check=False)
        self.assertIn(LOCK, self.unmerged())

        result = self.regenerate(*LOCK_OVERRIDE)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("(the rebase onto commit)", result.stdout)
        self.assertEqual(self.read(LOCK), "main\nregenerated\n")
        self.git("-c", "core.editor=true", "rebase", "--continue")
        self.assertEqual(self.git("show", f"HEAD:{LOCK}").stdout, "main\nregenerated\n")

    def test_rebase_stages_auto_merged_derived_file_so_continue_succeeds(self) -> None:
        self.write(LOCK, "base\nmiddle\nend\n")
        self.commit("longer lock")
        self.git("branch", "-f", "main")
        self.write(LOCK, "branch\nmiddle\nend\n")
        self.write(FLOW_DEFINITION, "{\"branch\": true}\n")
        self.commit("branch change")
        self.git("switch", "main")
        self.write(LOCK, "base\nmiddle\nmain\n")
        self.write(FLOW_DEFINITION, "{\"main\": true}\n")
        self.commit("upstream change")
        self.git("switch", "feature")
        self.git("rebase", "main", check=False)
        self.assertEqual(self.unmerged(), [FLOW_DEFINITION])

        result = self.regenerate(*LOCK_OVERRIDE, "--generator-command", "flow-definitions=true")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.git("-c", "core.editor=true", "rebase", "--continue")
        self.assertEqual(self.git("show", f"HEAD:{LOCK}").stdout, "branch\nmiddle\nmain\nregenerated\n")
        self.assertEqual(self.git("show", f"HEAD:{FLOW_DEFINITION}").stdout, "{\"main\": true}\n")

    def test_staging_leaves_files_outside_the_regenerated_groups_alone(self) -> None:
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)
        self.write("notes.txt", "unrelated\n")
        self.write("server-zd/Cargo.toml", "[workspace]\nmembers = []\n")

        result = self.regenerate(*LOCK_OVERRIDE)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        staged = self.git("diff", "--cached", "--name-only").stdout.split()
        self.assertEqual(staged, [LOCK])

    def test_changed_file_without_conflict_keeps_branch_copy_unstaged(self) -> None:
        self.write(LOCK, "branch\n")
        self.commit("branch lock")

        result = self.regenerate("--upstream", "main", *LOCK_OVERRIDE)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read(LOCK), "branch\nregenerated\n")
        self.assertEqual(self.git("diff", "--cached", "--name-only").stdout, "")

    def test_without_operation_or_flag_upstream_falls_back_to_origin_main(self) -> None:
        result = self.regenerate("--dry-run")

        self.assertEqual(result.returncode, 1)
        self.assertIn("origin/main does not exist", result.stdout)

    def test_only_affected_groups_run_unless_all(self) -> None:
        stub = "flow-definitions=" + f"{sys.executable} -c \"open('ran', 'w').write('yes')\""

        result = self.regenerate("--upstream", "main", "--generator-command", stub)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.repository / "ran").exists())

        overrides = [
            argument
            for group in ("cargo-lock:server-zd", "cargo-lock:sdk-rust", "cargo-lock:s3local",
                          "cargo-lock:examples/rust", "generated-code", "format-compat")
            for argument in ("--generator-command", f"{group}=true")
        ]
        result = self.regenerate("--upstream", "main", "--all", "--generator-command", stub, *overrides)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read("ran"), "yes")

    def test_dry_run_prints_plan_and_changes_nothing(self) -> None:
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)
        before = self.snapshot()

        result = self.regenerate("--dry-run", *LOCK_OVERRIDE)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("cargo-lock:server-zd: regenerate", result.stdout)
        self.assertIn(f"conflicted, take upstream copy: {LOCK}", result.stdout)
        self.assertEqual(self.snapshot(), before)

    def test_conflict_in_non_derived_path_refuses_without_any_change(self) -> None:
        self.write("server-zd/Cargo.toml", "[workspace]\nmembers = []\n")
        self.write(LOCK, "branch\n")
        self.commit("branch")
        self.git("switch", "main")
        self.write("server-zd/Cargo.toml", "[workspace]\nresolver = \"2\"\n")
        self.write(LOCK, "main\n")
        self.commit("main")
        self.git("switch", "feature")
        self.git("merge", "main", check=False)

        result = self.assert_refused_without_change(*LOCK_OVERRIDE)

        self.assertIn("not derived files", result.stdout)
        self.assertIn("server-zd/Cargo.toml", result.stdout)

    def test_simulation_pin_conflict_refuses_and_points_to_the_readme(self) -> None:
        self.diverge(SEEDS, upstream="1 100\n2 100\n", branch="1 100\n3 100\n")
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)

        result = self.assert_refused_without_change(*LOCK_OVERRIDE)

        self.assertIn("Pinned trajectories", result.stdout)

    def test_newest_format_compat_conflict_regenerates_newest_and_stages_it(self) -> None:
        self.diverge(NEWEST_STATE, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)

        result = self.regenerate("--generator-command", f"format-compat={STATE_GENERATOR}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.git("show", f":{NEWEST_STATE}").stdout, "main\nregenerated\n")
        self.assertEqual(self.unmerged(), [])

    def test_older_format_compat_version_conflict_refuses_without_any_change(self) -> None:
        self.diverge(OLD_STATE, upstream="old main\n", branch="old feature\n")
        self.diverge(LOCK, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)

        result = self.assert_refused_without_change(*LOCK_OVERRIDE, "--generator-command", "format-compat=true")

        self.assertIn("older versions are frozen", result.stdout)
        self.assertIn(OLD_STATE, result.stdout)

    def test_older_format_compat_version_change_refuses(self) -> None:
        self.write(OLD_STATE, "edited\n")
        self.commit("edit frozen version")

        result = self.assert_refused_without_change(
            "--upstream", "main", "--generator-command", "format-compat=true"
        )

        self.assertIn("older versions are frozen", result.stdout)

    def test_older_pin_change_refuses(self) -> None:
        self.write(PIN_FILE, pin_file("1" * 32, "fn test() {}", older_digest="b" * 32))
        self.commit("edit frozen pin")

        result = self.assert_refused_without_change(
            "--upstream", "main", "--generator-command", "format-compat=true"
        )

        self.assertIn("older, frozen format_compat version", result.stdout)

    def test_newest_pin_conflict_resolves_inside_the_pin_block(self) -> None:
        self.diverge(
            PIN_FILE,
            upstream=pin_file("2" * 32, "fn test() {}"),
            branch=pin_file("3" * 32, "fn test() {}"),
        )
        self.git("merge", "main", check=False)

        result = self.regenerate("--generator-command", "format-compat=true")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.read(PIN_FILE), pin_file("2" * 32, "fn test() {}"))
        self.assertEqual(self.unmerged(), [])

    def test_pin_file_conflict_outside_the_newest_pin_refuses_without_any_change(self) -> None:
        self.diverge(
            PIN_FILE,
            upstream=pin_file("2" * 32, "fn upstream_test() {}"),
            branch=pin_file("3" * 32, "fn branch_test() {}"),
        )
        self.git("merge", "main", check=False)

        result = self.assert_refused_without_change("--generator-command", "format-compat=true")

        self.assertIn("conflicts outside the v2 pin", result.stdout)

    def test_released_server_zd_refuses_format_compat_without_any_change(self) -> None:
        self.git("tag", "server-zd/v1.0.0", "main")
        self.diverge(NEWEST_STATE, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)

        result = self.assert_refused_without_change("--generator-command", "format-compat=true")

        self.assertIn("server-zd 1.0 is released (server-zd/v1.0.0", result.stdout)

    def test_unrelated_dirty_file_in_a_group_refuses_without_any_change(self) -> None:
        self.diverge(NEWEST_STATE, upstream="main\n", branch="feature\n")
        self.git("merge", "main", check=False)
        self.write("server-zd/testdata/v2/objects/stray", "stray\n")

        result = self.assert_refused_without_change("--generator-command", "format-compat=true")

        self.assertIn("uncommitted changes that are not conflicts", result.stdout)
        self.assertIn("server-zd/testdata/v2/objects/stray", result.stdout)

    def test_generated_code_paths_match_the_makefile(self) -> None:
        makefile = MAKEFILE.read_text(encoding="utf-8")
        block = re.search(r"^GENERATED_CODE_PATHS := \\\n((?:\t.+\n)+)", makefile, re.MULTILINE)
        self.assertIsNotNone(block, "GENERATED_CODE_PATHS is defined in the root Makefile")
        paths = tuple(line.strip(" \t\\") for line in block.group(1).splitlines())
        self.assertEqual(paths, regenerate_derived.GENERATED_CODE_PATHS)

    def assert_refused_without_change(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        before = self.snapshot()
        result = self.regenerate(*arguments)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("Blocked:", result.stdout)
        self.assertEqual(self.snapshot(), before)
        return result

    def snapshot(self) -> tuple[str, dict[str, bytes]]:
        index = self.git("ls-files", "--stage").stdout
        files = {
            str(path.relative_to(self.repository)): path.read_bytes()
            for path in sorted(self.repository.rglob("*"))
            if path.is_file() and ".git" not in path.relative_to(self.repository).parts
        }
        return index, files

    def diverge(self, path: str, upstream: str, branch: str) -> None:
        self.write(path, branch)
        self.commit("branch change")
        self.git("switch", "main")
        self.write(path, upstream)
        self.commit("upstream change")
        self.git("switch", "feature")

    def regenerate(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            (sys.executable, str(SCRIPT), *arguments),
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


def pin_file(newest_digest: str, test_body: str, older_digest: str = "a" * 32) -> str:
    return (
        "const PINNED_VERSION_DIGESTS: &[(&str, &str)] = &[\n"
        f'    ("v1", "{older_digest}"),\n'
        f'    ("v2", "{newest_digest}"),\n'
        "];\n"
        "\n"
        "#[test]\n"
        f"{test_body}\n"
    )


if __name__ == "__main__":
    unittest.main()
