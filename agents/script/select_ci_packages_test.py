#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run select_ci_packages.py against a small git repository of agents packages."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().with_name("select_ci_packages.py")

PACKAGE_FILES = {
    "agents/python/core/pyproject.toml": 'name = "dex-agents"\n',
    "agents/python/langgraph/pyproject.toml": 'dex-agents = { path = "../core" }\n',
    "agents/typescript/core/package.json": '{"name": "@superdurable/dex-agents"}\n',
    "agents/cli/typescript/pi/package.json": '{"dependencies": {"core": "file:../../../typescript/core"}}\n',
    "agents/cli/python/deepagents/pyproject.toml": (
        'langgraph = { path = "../../../python/langgraph" }\n'
    ),
    "agents/go/core/integ/stack_test.go": 'const pythonCoreDir = "../../../python/core"\n',
}


class SelectCiPackagesTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repository = Path(self.temporary_directory.name)
        subprocess.run(["git", "init", "-q"], cwd=self.repository, check=True)
        for path, text in PACKAGE_FILES.items():
            self.write(path, text)
            ci_script = self.repository / self.package_directory_of(path) / "ci.sh"
            ci_script.write_text("#!/bin/sh\n", encoding="utf-8")
            ci_script.chmod(0o755)
        self.write("agents/conformance/golden.json", "{}\n")
        subprocess.run(["git", "add", "-A"], cwd=self.repository, check=True)

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_an_adapter_change_selects_it_and_its_dependents(self) -> None:
        self.assertEqual(
            self.select(["agents/python/langgraph/src/graph.py"]),
            ["agents/cli/python/deepagents", "agents/python/langgraph"],
        )

    def test_a_python_core_change_skips_the_typescript_packages(self) -> None:
        self.assertEqual(
            self.select(["agents/python/core/src/session.py"]),
            [
                "agents/cli/python/deepagents",
                "agents/go/core",
                "agents/python/core",
                "agents/python/langgraph",
            ],
        )

    def test_a_shared_file_or_a_workflow_selects_every_package(self) -> None:
        every_package = self.select(None)
        self.assertEqual(len(every_package), len(PACKAGE_FILES))
        self.assertEqual(self.select(["agents/conformance/golden.json"]), every_package)
        self.assertEqual(self.select([".github/workflows/agents-ci.yml"]), every_package)

    def test_markdown_outside_packages_and_files_outside_agents_select_nothing(self) -> None:
        self.assertEqual(
            self.select(["agents/CHANGELOG.md", "agents/cli/README.md", "changelog.d/agents/a.md"]),
            [],
        )

    def select(self, changed_files: list[str] | None) -> list[str]:
        command = [sys.executable, str(SCRIPT), "--repository-root", str(self.repository)]
        if changed_files is not None:
            changed_files_path = self.repository / "changed-files.txt"
            changed_files_path.write_text("\n".join(changed_files) + "\n", encoding="utf-8")
            command += ["--changed-files", str(changed_files_path)]
        result = subprocess.run(command, check=True, capture_output=True, text=True)
        return json.loads(result.stdout)

    def write(self, path: str, text: str) -> None:
        file_path = self.repository / path
        file_path.parent.mkdir(parents=True, exist_ok=True)
        file_path.write_text(text, encoding="utf-8")

    @staticmethod
    def package_directory_of(path: str) -> Path:
        parts = Path(path).parts
        return Path(*parts[:4]) if parts[1] == "cli" else Path(*parts[:3])


if __name__ == "__main__":
    unittest.main()
