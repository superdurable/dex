# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("changed_components.py")
WEB_RELEASE_WORKFLOW = SCRIPT.parents[2] / ".github/workflows/docker-image-web-release.yml"
CLI_RELEASE_WORKFLOW = SCRIPT.parents[2] / ".github/workflows/cli-release.yml"
CHANGED_COMPONENTS_WORKFLOW = SCRIPT.parents[2] / ".github/workflows/release-changed-components.yml"
COMPONENT_KEYS = ("go", "rust", "java", "python", "typescript", "web", "cli", "agents")
BASELINE_TAGS = (
    "sdk-go/v0.1.0",
    "sdk-rust/v0.1.0",
    "sdk-java/v0.1.0",
    "sdk-python/v0.1.0",
    "sdk-typescript/v0.1.0",
    "cli-v0.1.0",
    "web/v0.1.0",
    "agents/v0.1.0",
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
    "protos/api.proto",
    "cli/main.go",
    "web/package.json",
    "packages/flow-definition-renderer/src/index.ts",
    "go.work",
    "agents/python/core/pyproject.toml",
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

    def plan(
        self, version: str = "1.2.3", *extra_arguments: str
    ) -> tuple[subprocess.CompletedProcess[str], dict[str, str]]:
        output = self.repository / "release-output"
        result = subprocess.run(
            ("python3", str(SCRIPT), version, "--output", str(output), *extra_arguments),
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

    def test_go_sdk_change_triggers_web_and_cli(self) -> None:
        self.change("sdk-go/client.go")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go", "web", "cli")

    def test_shared_native_change_triggers_dependent_sdks(self) -> None:
        self.change("sdk-rust/crates/dex-blob-cache/src/lib.rs")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "rust", "java", "python", "typescript")

    def test_web_change_triggers_web_and_cli(self) -> None:
        self.change("web/package.json")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "web", "cli")

    def test_flow_definition_renderer_change_triggers_web_and_cli(self) -> None:
        self.change("packages/flow-definition-renderer/src/index.ts")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "web", "cli")

    def test_web_release_workflow_change_triggers_web_only(self) -> None:
        self.change(".github/workflows/docker-image-web-release.yml")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "web")
        self.assertEqual(values["web_tag"], "web/v1.2.3")

    def test_generated_sdk_api_change_triggers_web_and_cli(self) -> None:
        self.change("sdk-go/gen/dexpb/dex.pb.go")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go", "web", "cli")

    def test_missing_web_baseline_selects_first_release(self) -> None:
        self.git("tag", "-d", "web/v0.1.0")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "web")
        self.assertEqual(values["web_baseline"], "")

    def test_mixed_changes_select_multiple_releases(self) -> None:
        self.change("sdk-go/client.go")
        self.change("sdk-typescript/package.json")
        self.change("protos/api.proto")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go", "typescript", "web", "cli")

    def test_missing_baseline_selects_first_release(self) -> None:
        self.git("tag", "-d", "sdk-go/v0.1.0")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go")
        self.assertEqual(values["go_baseline"], "")

    def test_existing_target_tag_fails_preflight(self) -> None:
        self.git("tag", "sdk-go/v1.2.3")
        self.change("sdk-go/client.go")
        result, _ = self.plan()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sdk-go/v1.2.3", result.stderr)

    def test_agents_change_without_agents_version_is_skipped(self) -> None:
        self.change("agents/python/core/pyproject.toml")
        result, values = self.plan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values)
        self.assertEqual(values["agents_tag"], "")
        self.assertEqual(values["agents_version"], "")

    def test_agents_change_releases_with_its_own_version(self) -> None:
        self.change("agents/typescript/core/package.json")
        self.change("sdk-go/client.go")
        result, values = self.plan("1.2.3", "--agents-version", "0.2.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go", "web", "cli", "agents")
        self.assertEqual(values["agents_tag"], "agents/v0.2.0")
        self.assertEqual(values["agents_baseline"], "agents/v0.1.0")
        self.assertEqual(values["agents_version"], "0.2.0")
        self.assertEqual(values["go_tag"], "sdk-go/v1.2.3")

    def test_agents_version_without_agents_changes_is_skipped(self) -> None:
        self.change("sdk-go/client.go")
        result, values = self.plan("1.2.3", "--agents-version", "0.2.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "go", "web", "cli")

    def test_agents_publish_workflow_change_selects_agents(self) -> None:
        self.change(".github/workflows/agents-python-publish.yml")
        result, values = self.plan("1.2.3", "--agents-version", "0.2.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "agents")

    def test_missing_agents_baseline_selects_first_agents_release(self) -> None:
        self.git("tag", "-d", "agents/v0.1.0")
        result, values = self.plan("1.2.3", "--agents-version", "0.1.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_selected(values, "agents")
        self.assertEqual(values["agents_tag"], "agents/v0.1.0")
        self.assertEqual(values["agents_baseline"], "")

    def test_existing_agents_tag_fails_preflight(self) -> None:
        self.git("tag", "agents/v0.2.0")
        self.change("agents/python/core/pyproject.toml")
        result, _ = self.plan("1.2.3", "--agents-version", "0.2.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("agents/v0.2.0", result.stderr)

    def test_invalid_agents_version_fails(self) -> None:
        result, _ = self.plan("1.2.3", "--agents-version", "v0.2")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Invalid semantic version", result.stderr)

    def test_coordinated_release_dispatches_agents_publishers(self) -> None:
        coordinated_workflow = CHANGED_COMPONENTS_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn('--agents-version "${AGENTS_VERSION}"', coordinated_workflow)
        for workflow in ("agents-python-publish.yml", "agents-typescript-publish.yml"):
            self.assertIn(f"workflow: {workflow}", coordinated_workflow)
        self.assertIn('gh workflow run "${PUBLISHER_WORKFLOW}"', coordinated_workflow)
        self.assertIn('-f "publish=true"', coordinated_workflow)

    def test_docker_web_release_uses_web_path_tag(self) -> None:
        workflow = WEB_RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("startsWith(github.event.release.tag_name, 'web/v')", workflow)
        self.assertIn('image_tag="${RELEASE_TAG#web/}"', workflow)
        self.assertNotIn("server/v", workflow)

    def test_release_workflows_maintain_breaking_changes_section(self) -> None:
        coordinated_workflow = CHANGED_COMPONENTS_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("releases/generate-notes", coordinated_workflow)
        self.assertIn("previous_tag_name=${baseline}", coordinated_workflow)
        self.assertIn("## Breaking Changes\\n\\nNone.", coordinated_workflow)
        self.assertIn("--notes-file", coordinated_workflow)

        cli_workflow = CLI_RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("Maintain Breaking Changes section", cli_workflow)
        self.assertIn("grep -qx '## Breaking Changes'", cli_workflow)
        self.assertIn("## Breaking Changes\\n\\nNone.", cli_workflow)


if __name__ == "__main__":
    unittest.main()
