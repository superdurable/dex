# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The CLI core passes the CLI cases of agents/conformance/approval_display.json."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from dex_agents import AgentInterruptDisplay
from dex_agents_cli_core.local_tools.approval_display import (
    build_file_preview,
    build_shell_command_display,
    escape_control_characters,
    is_credential_file_name,
)
from dex_agents_cli_core.local_tools.files import WorkspaceFiles

AGENTS_DIR = Path(__file__).resolve().parents[4]
CLI_CASES: dict[str, Any] = json.loads(
    (AGENTS_DIR / "conformance" / "approval_display.json").read_text(encoding="utf-8")
)["cli"]


@pytest.mark.parametrize("path", CLI_CASES["credential_files"]["credential"])
def test_a_credential_file_is_found_as_the_conformance_suite_says(path: str) -> None:
    assert is_credential_file_name(path)


@pytest.mark.parametrize("path", CLI_CASES["credential_files"]["not_credential"])
def test_another_file_is_not_a_credential_file(path: str) -> None:
    assert not is_credential_file_name(path)


@pytest.mark.parametrize("case", CLI_CASES["file_previews"], ids=lambda case: repr(case["path"]))
def test_a_file_write_preview_is_the_conformance_suites_diff(case: dict[str, Any]) -> None:
    assert build_file_preview(case["path"], case["current"], case["new"]) == case["preview"]


@pytest.mark.parametrize("case", CLI_CASES["shell_displays"], ids=lambda case: case["command"][:40])
def test_a_shell_display_redacts_inline_credentials_as_the_conformance_suite_says(
    case: dict[str, str],
) -> None:
    assert build_shell_command_display(case["workspace"], case["command"]) == (
        AgentInterruptDisplay(target=case["target"], summary=case["summary"])
    )


@pytest.mark.parametrize("case", CLI_CASES["escaped_paths"])
def test_control_characters_in_paths_are_escaped(case: dict[str, str]) -> None:
    assert escape_control_characters(case["text"]) == case["escaped"]


def test_a_preview_reads_only_the_file_a_write_would_replace(tmp_path: Path) -> None:
    workspace = tmp_path / "workspace"
    outside = tmp_path / "outside"
    (workspace / "notes").mkdir(parents=True)
    outside.mkdir()
    (workspace / "notes" / "a.txt").write_text("inside")
    (outside / "a.txt").write_text("outside secret")
    (workspace / "link.txt").symlink_to(workspace / "notes" / "a.txt")
    (workspace / "outside-link").symlink_to(outside)
    (workspace / "notes-link").symlink_to(workspace / "notes")
    files = WorkspaceFiles(workspace)

    assert files.read_text_for_preview("notes/a.txt", 1024) == ("notes/a.txt", "inside")
    # The write refuses a final link, so the preview reads nothing through one.
    assert files.read_text_for_preview("link.txt", 1024) is None
    assert files.read_text_for_preview("outside-link/a.txt", 1024) is None
    assert files.read_text_for_preview("../outside/a.txt", 1024) is None
    assert files.read_text_for_preview(str(outside / "a.txt"), 1024) is None
    # A directory link that stays inside works, as it does for the write.
    assert files.read_text_for_preview("notes-link/a.txt", 1024) == ("notes/a.txt", "inside")
    assert files.read_text_for_preview("notes/new/b.txt", 1024) == ("notes/new/b.txt", None)
    assert files.read_text_for_preview("outside-link/new/b.txt", 1024) is None
    assert files.read_text_for_preview("notes/a.txt", 3) is None
