#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Check workflow_paths.py against the repository workflows and small workflow fixtures."""

from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import workflow_paths  # noqa: E402

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]

MAIN_CI = """name: Main CI (all)
on:
  schedule:
    - cron: "0 */3 * * *"
jobs:
  plan:
    runs-on: ubuntu-latest
  sdk-go:
    needs: plan
    uses: ./.github/workflows/sdk-go-ci.yml
  web:
    needs: plan
    uses: ./.github/workflows/web-ci.yml
"""

SDK_GO = """name: Go SDK CI
on:
  pull_request:
    types: [opened, synchronize]
    paths:
      - "sdk-go/**"
      # A comment between paths.
      - "**/*.proto"
  workflow_call:
"""

WEB = """name: Web CI
on:
  pull_request:
    paths:
    - 'web/**'
"""


class GlobTest(unittest.TestCase):
    def test_globs_match_like_github_paths_filters(self) -> None:
        cases = [
            ("web/**", "web/app/page.tsx", True),
            ("**/*.rs", "server-zd/src/main.rs", True),
            ("**/*.rs", "build.rs", True),
            ("web/**/*.go", "web/server.go", True),
            ("web/**/*.go", "web/app/page.tsx", False),
            ("docs/reference/*/changelog.mdx", "docs/reference/web/changelog.mdx", True),
            ("docs/reference/*/changelog.mdx", "docs/reference/a/b/changelog.mdx", False),
            ("Makefile", "cli/Makefile", False),
        ]
        for pattern, path, is_match in cases:
            with self.subTest(pattern=pattern, path=path):
                self.assertEqual(workflow_paths.matches_any(path, [pattern]), is_match)


class SelectMainSuitesTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.workflows = Path(self.temporary_directory.name)
        (self.workflows / "main-ci-all.yml").write_text(MAIN_CI, encoding="utf-8")
        (self.workflows / "sdk-go-ci.yml").write_text(SDK_GO, encoding="utf-8")
        (self.workflows / "web-ci.yml").write_text(WEB, encoding="utf-8")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_changed_paths_select_only_matching_suites(self) -> None:
        self.assertEqual(self.select(["web/app/page.tsx"]), ["web"])
        self.assertEqual(self.select(["protos/dex/flow.proto"]), ["sdk-go"])
        self.assertEqual(self.select(["docs/index.mdx"]), [])

    def test_no_changed_files_or_a_selection_change_selects_every_suite(self) -> None:
        self.assertEqual(self.select(None), ["sdk-go", "web"])
        self.assertEqual(self.select([".github/scripts/workflow_paths.py"]), ["sdk-go", "web"])

    def test_a_pattern_this_script_cannot_read_selects_the_suite(self) -> None:
        (self.workflows / "web-ci.yml").write_text(WEB.replace("web/**", "web/{app,lib}/**"), encoding="utf-8")
        self.assertEqual(self.select(["docs/index.mdx"]), ["web"])

    def select(self, changed_files: list[str] | None) -> list[str]:
        return workflow_paths.select_main_suites(self.workflows, changed_files)


GATE = """name: Check Gate CI
jobs:
  check-gate:
    steps:
      - uses: dorny/paths-filter@v3
        id: filter
        with:
          filters: |
            sdk_go:
              - 'sdk-go/**'
              # A comment between paths.
              - '**/*.proto'
            web:
              - 'web/**'
      - name: Wait for required workflows
        env:
          FILTER_SDK_GO: ${{ steps.filter.outputs.sdk_go }}
          FILTER_WEB: ${{ steps.filter.outputs.web }}
        run: |
          [[ "$FILTER_SDK_GO" == "true" ]] && expected+=("Go SDK CI")
          [[ "$FILTER_WEB" == "true" ]] && expected+=("Web CI")
"""


class CheckGateFiltersTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.workflows = Path(self.temporary_directory.name)
        (self.workflows / "sdk-go-ci.yml").write_text(SDK_GO, encoding="utf-8")
        (self.workflows / "web-ci.yml").write_text(WEB, encoding="utf-8")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_matching_filters_pass(self) -> None:
        (self.workflows / "check-gate-ci.yml").write_text(GATE, encoding="utf-8")
        self.assertEqual(workflow_paths.check_gate_filters(self.workflows), [])

    def test_a_filter_with_extra_paths_fails(self) -> None:
        gate = GATE.replace("              - 'web/**'\n", "              - 'web/**'\n              - 'sdk-rust/**'\n")
        (self.workflows / "check-gate-ci.yml").write_text(gate, encoding="utf-8")
        differences = workflow_paths.check_gate_filters(self.workflows)
        self.assertEqual(len(differences), 1)
        self.assertIn("only in the gate ['sdk-rust/**']", differences[0])


class RepositoryWorkflowsTest(unittest.TestCase):
    def test_gate_filters_and_dexcli_paths_match_the_workflows(self) -> None:
        workflows = REPOSITORY_ROOT / ".github/workflows"
        self.assertEqual(workflow_paths.check_gate_filters(workflows), [])
        self.assertEqual(workflow_paths.check_dexcli_paths(workflows, REPOSITORY_ROOT / "cli/internal"), [])

    def test_a_new_cli_internal_package_must_trigger_the_sdk_suites(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            cli_internal = Path(temporary_directory) / "cli/internal"
            (cli_internal / "dev").mkdir(parents=True)
            (cli_internal / "newpackage").mkdir()
            differences = workflow_paths.check_dexcli_paths(REPOSITORY_ROOT / ".github/workflows", cli_internal)
        self.assertEqual(len(differences), len(workflow_paths.SDK_SUITES_THAT_RUN_DEXCLI_DEV))
        self.assertTrue(all("newpackage" in difference for difference in differences))

    def test_every_main_ci_suite_has_readable_pull_request_paths(self) -> None:
        workflows = REPOSITORY_ROOT / ".github/workflows"
        suites = workflow_paths.read_called_workflows(workflows / workflow_paths.MAIN_CI_WORKFLOW)
        for job, suite_file in suites.items():
            with self.subTest(job=job):
                self.assertTrue(workflow_paths.read_pull_request_paths(workflows / suite_file))


if __name__ == "__main__":
    unittest.main()
