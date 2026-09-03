# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("changed_components.py")
COMPONENT_KEYS = ("go", "rust", "java", "python", "typescript", "server", "cli")
BASELINE_TAGS = (
    "sdk-go/v0.1.0",
    "sdk-rust/v0.1.0",
    "sdk-java/v0.1.0",
    "sdk-python/v0.1.0",
    "sdk-typescript/v0.1.0",
    "server/v0.1.0",
    "cli-v0.1.0",
)
INITIAL_FILES = (
    "sdk-go/client.go",
    "sdk-rust/Cargo.toml",
    "sdk-rust/Cargo.lock",
    "sdk-rust/crates/dex-blob-cache/src/lib.rs",
    "sdk-rust/crates/dex-blob-cache-jni/src/lib.rs",
    "sdk-rust/crates/dex-blob-cache-python/src/lib.rs",
    "sdk-rust/crates/dex-blob-cache-node/src/lib.rs",
    "sdk-java/build.gradle",
    "sdk-python/pyproject.toml",
    "sdk-typescript/package.json",
    "server/main.go",
    "protos/api.proto",
    "cli/main.go",
    "web/package.json",
    "go.work",
)


class ChangedComponentsIntegrationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repository = Path(self.temporary_directory.name)
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.com")
        for relative_path in INITIAL_FILES:
            path = self.repository / relative_path
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("initial\n", encoding="utf-8")
        self.commit("initial")
        for tag in BASELINE_TAGS:
            self.git("tag", tag)

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def git(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("git", *arguments),
            cwd=self.repository,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def commit(self, message: str) -> None:
        self.git("add", ".")
        self.git("commit", "-m", message)

    def change(self, relative_path: str) -> None:
        path = self.repository / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("a", encoding="utf-8") as changed_file:
            changed_file.write("changed\n")
        self.commit(f"change {relative_path}")

    def plan(self, version: str = "1.2.3") -> tuple[subprocess.CompletedProcess[str], dict[str, str]]:
        output = self.repository / "release-output"
        result = subprocess.run(
            ("python3", str(SCRIPT), version, "--output", str(output)),
            cwd=self.repository,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        values: dict[str, str] = {}
        if output.exists():
            values = dict(line.split("=", 1) for line in output.read_text().splitlines())
        return result, values

    def assert_selected(self, values: dict[str, str], *selected: str) -> None:
        selected_keys = set(selected)
        for key in COMPONENT_KEYS:
            self.assertEqual(values[key], "true" if key in selected_keys else "false", key)

    def test_no_component_changes(self) -> None:
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values)

    def test_one_sdk_change(self) -> None:
        self.change("sdk-go/client.go")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go")

    def test_shared_native_change_triggers_dependent_sdks(self) -> None:
        self.change("sdk-rust/crates/dex-blob-cache/src/lib.rs")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "rust", "java", "python", "typescript")

    def test_server_change_triggers_server_and_cli(self) -> None:
        self.change("server/main.go")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "server", "cli")

    def test_current_server_tag_is_preferred_over_historical_tag(self) -> None:
        self.change("server/main.go")
        self.git("tag", "server-v0.2.0")
        self.change("web/package.json")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "cli")

    def test_web_change_triggers_only_cli(self) -> None:
        self.change("web/package.json")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "cli")

    def test_mixed_changes_select_multiple_releases(self) -> None:
        self.change("sdk-go/client.go")
        self.change("sdk-typescript/package.json")
        self.change("protos/api.proto")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go", "typescript", "server", "cli")

    def test_missing_baseline_selects_first_release(self) -> None:
        self.git("tag", "-d", "sdk-go/v0.1.0")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go")

    def test_existing_target_tag_fails_preflight(self) -> None:
        self.git("tag", "sdk-go/v1.2.3")
        self.change("sdk-go/client.go")
        result, _ = self.plan()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sdk-go/v1.2.3", result.stderr)


if __name__ == "__main__":
    unittest.main()
