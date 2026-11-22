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

sys.path.insert(0, str(Path(__file__).parent))
from check_agents_go_modules import REPOSITORY, go_module_errors, go_module_tags  # noqa: E402

SCRIPT = Path(__file__).with_name("check_agents_go_modules.py")
CORE = "github.com/superdurable/dex/agents/go/core"
CORE_GO_MOD = f"""module {CORE}

go 1.26.0

require (
\tgithub.com/superdurable/dex/blob-cache-go v0.1.2
\tgithub.com/superdurable/dex/sdk-go v1.45.6
)

require google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
"""
# An adapter as the release tags it: the core at the release version, built from ../core in this repository.
ADAPTER_GO_MOD = f"""module github.com/superdurable/dex/agents/go/adapter

go 1.26.0

require (
\tgithub.com/cloudwego/eino v0.9.23
\t{CORE} v0.6.16
\tgithub.com/superdurable/dex/sdk-go v1.45.6
\tgithub.com/superdurable/dex-connectors-library/sdkgo v0.39.0
)

replace {CORE} => ../core
"""


class CheckAgentsGoModulesTest(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.repository = Path(temporary_directory.name)
        self.write_module("core", CORE_GO_MOD)
        self.write_module("adapter", ADAPTER_GO_MOD)

    def write_module(self, directory: str, text: str) -> None:
        path = self.repository / "agents/go" / directory / "go.mod"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def assert_one_error(self, version: str | None, *fragments: str) -> None:
        errors = go_module_errors(self.repository, version)
        self.assertEqual(len(errors), 1, errors)
        for fragment in fragments:
            self.assertIn(fragment, errors[0])

    def test_released_modules_pass_and_get_one_tag_each(self) -> None:
        self.assertEqual(go_module_errors(self.repository), [])
        self.assertEqual(go_module_errors(self.repository, "0.6.16"), [])
        self.assertEqual(
            go_module_tags(self.repository, "0.6.16"), ["agents/go/adapter/v0.6.16", "agents/go/core/v0.6.16"]
        )

    def test_prerelease_versions_pass(self) -> None:
        self.write_module("adapter", ADAPTER_GO_MOD.replace(f"{CORE} v0.6.16", f"{CORE} v0.7.0-rc.1"))
        self.assertEqual(go_module_errors(self.repository, "0.7.0-rc.1"), [])

    def test_the_release_needs_agents_modules_at_its_version(self) -> None:
        self.assert_one_error(
            "0.6.17",
            f"agents/go/adapter/go.mod: requires {CORE} v0.6.16, not the release version v0.6.17",
            # The stamp reads pyproject.toml with tomllib, so it needs Python 3.11, as Agents CI runs it.
            "run uv run --no-project --python 3.11 python agents/script/stamp_agents_release_version.py 0.6.17 --go",
        )

    def test_agents_modules_require_each_other_at_one_version(self) -> None:
        self.write_module(
            "second",
            f"module github.com/superdurable/dex/agents/go/second\n\nrequire {CORE} v0.6.15\n\nreplace {CORE} => ../core\n",
        )
        self.assert_one_error(
            None,
            "different versions",
            f"agents/go/adapter/go.mod requires {CORE} v0.6.16",
            f"agents/go/second/go.mod requires {CORE} v0.6.15",
        )

    def test_pseudo_versions_of_superdurable_modules_fail(self) -> None:
        for module in ("github.com/superdurable/dex/sdk-go", "github.com/superdurable/dex-connectors-library/sdkgo"):
            with self.subTest(module=module):
                text = ADAPTER_GO_MOD.splitlines(keepends=True)
                text = "".join(
                    f"\t{module} v1.45.7-0.20261121035520-ce07e544e166\n" if line.startswith(f"\t{module} ") else line
                    for line in text
                )
                self.write_module("adapter", text)
                self.assert_one_error(None, f"requires {module} at the pseudo-version", "unreleased commit")

    def test_replace_of_another_module_fails(self) -> None:
        for replace in (
            "replace github.com/superdurable/dex/sdk-go => ../../../sdk-go",
            "replace github.com/cloudwego/eino => github.com/example/eino v0.9.24",
            f"replace {CORE} v0.6.16 => ../core",
        ):
            with self.subTest(replace=replace):
                self.write_module("adapter", ADAPTER_GO_MOD + replace + "\n")
                self.assert_one_error(None, f"agents/go/adapter/go.mod: {replace}:", "builds without replace")

    def test_exclude_fails(self) -> None:
        self.write_module("adapter", ADAPTER_GO_MOD + "exclude github.com/cloudwego/eino v0.9.22\n")
        self.assert_one_error(None, "exclude github.com/cloudwego/eino v0.9.22", "builds without exclude")

    def test_agents_require_needs_its_local_replace(self) -> None:
        for replace in ("", f"replace {CORE} => ../../core"):
            with self.subTest(replace=replace):
                self.write_module(
                    "adapter", ADAPTER_GO_MOD.replace(f"replace {CORE} => ../core\n", replace + "\n" if replace else "")
                )
                errors = go_module_errors(self.repository)
                self.assertIn(
                    f"agents/go/adapter/go.mod: requires {CORE} without replace {CORE} => ../core, "
                    "so this repository would not build the local module",
                    errors,
                )

    def test_agents_require_must_name_a_module_in_the_repository(self) -> None:
        missing = "github.com/superdurable/dex/agents/go/missing"
        self.write_module("adapter", ADAPTER_GO_MOD + f"\nrequire {missing} v0.6.16\n\nreplace {missing} => ../missing\n")
        self.assert_one_error(None, f"requires {missing}, which is not a module under agents/go")

    def test_agents_require_must_be_a_release_version(self) -> None:
        self.write_module(
            "adapter", ADAPTER_GO_MOD.replace(f"{CORE} v0.6.16", f"{CORE} v0.0.0-00010101000000-000000000000")
        )
        self.assert_one_error(None, f"requires {CORE} v0.0.0-00010101000000-000000000000, which is not a release")

    def test_module_path_must_match_its_tag(self) -> None:
        self.write_module("adapter", ADAPTER_GO_MOD.replace("agents/go/adapter", "agents/go/eino", 1))
        self.assert_one_error(
            None,
            "agents/go/adapter/go.mod: declares module github.com/superdurable/dex/agents/go/eino",
            "agents/go/adapter/v<version> resolves only github.com/superdurable/dex/agents/go/adapter",
        )

    def test_release_versions_go_cannot_tag_fail(self) -> None:
        for version, fragment in (
            ("0.7.0.1", "v0.7.0.1 is not a Go module release version"),
            ("0.7.0-rc.01", "v0.7.0-rc.01 is not a Go module release version"),
            ("0.6.16-0.20261121000000-abcdef123456", "is not a Go module release version"),
            ("2.0.0", "no /v2 suffix"),
        ):
            with self.subTest(version=version):
                errors = go_module_errors(self.repository, version)
                self.assertTrue(any(fragment in error for error in errors), errors)

    def test_command_prints_the_tags_or_the_errors(self) -> None:
        result = self.run_check("--version", "0.6.16")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "agents/go/adapter/v0.6.16\nagents/go/core/v0.6.16\n")
        result = self.run_check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "checked 2 agents Go modules\n")
        result = self.run_check("--version", "0.6.17")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("not the release version v0.6.17", result.stderr)

    def test_a_repository_without_agents_go_modules_passes(self) -> None:
        with tempfile.TemporaryDirectory() as empty_repository:
            self.assertEqual(go_module_errors(Path(empty_repository), "0.6.16"), [])
            self.assertEqual(go_module_tags(Path(empty_repository), "0.6.16"), [])

    def test_a_sparse_checkout_without_a_module_fails(self) -> None:
        self.commit_fixture()
        self.git("sparse-checkout", "set", "--cone", "agents/go/core")
        self.assertFalse((self.repository / "agents/go/adapter/go.mod").exists())
        missing = "agents/go/adapter/go.mod: git tracks it, but this checkout does not have it"
        for version in (None, "0.6.16"):
            with self.subTest(version=version):
                self.assert_one_error(version, missing, "git sparse-checkout add agents/go")
        result = self.run_check("--version", "0.6.16")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn(missing, result.stderr)

    def test_a_release_checks_the_go_mods_of_the_commit_it_tags(self) -> None:
        self.commit_fixture()
        # The tags name HEAD, which still requires the core at v0.6.16.
        self.write_module("adapter", ADAPTER_GO_MOD.replace(f"{CORE} v0.6.16", f"{CORE} v0.6.17"))
        self.assertEqual(go_module_errors(self.repository), [])
        self.assert_one_error(
            "0.6.17", "agents/go/adapter/go.mod: differs from HEAD", "commit or discard the change"
        )
        self.git("checkout", "--", "agents/go/adapter/go.mod")
        self.write_module("second", f"module github.com/superdurable/dex/agents/go/second\n\ngo 1.26.0\n")
        self.assertEqual(go_module_errors(self.repository), [])
        self.assert_one_error("0.6.16", "agents/go/second/go.mod: differs from HEAD")
        self.git("add", "agents/go/second/go.mod")
        self.assert_one_error("0.6.16", "agents/go/second/go.mod: differs from HEAD")
        self.git("commit", "--quiet", "-m", "second")
        self.assertEqual(go_module_errors(self.repository, "0.6.16"), [])

    def test_this_repository_passes(self) -> None:
        self.assertEqual(go_module_errors(REPOSITORY), [])
        self.assertIn("agents/go/core/v0.6.16", go_module_tags(REPOSITORY, "0.6.16"))

    def commit_fixture(self) -> None:
        self.git("init", "--quiet", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.com")
        self.git("add", ".")
        self.git("commit", "--quiet", "-m", "agents Go modules")

    def git(self, *arguments: str) -> None:
        subprocess.run(("git", *arguments), cwd=self.repository, check=True, capture_output=True, text=True)

    def run_check(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            (sys.executable, str(SCRIPT), "--repository", str(self.repository), *arguments),
            check=False,
            text=True,
            capture_output=True,
        )


if __name__ == "__main__":
    unittest.main()
