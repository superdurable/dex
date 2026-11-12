# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command's Deep Agents backend: workspace confinement and per-session artifacts."""

from __future__ import annotations

import stat
from pathlib import Path

import pytest
from deepagents.backends import filesystem as deep_agents_filesystem

from dex_agents_deepagents_cli import WorkspaceShellBackend, build_workspace_backend
from dex_agents_deepagents_cli.workspace_backend import bind_artifacts_session

OUTSIDE_SECRET = "SECRET_OUTSIDE_WORKSPACE"


@pytest.fixture
def linked_workspace(tmp_path: Path) -> Path:
    """Return a workspace with one own file and links to a file and a directory outside it."""
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "outside.txt").write_text(f"{OUTSIDE_SECRET}\n")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "own.txt").write_text(f"{OUTSIDE_SECRET} in the workspace\n")
    (workspace / "leak.txt").symlink_to(outside / "outside.txt")
    (workspace / "leak-dir").symlink_to(outside)
    return workspace.resolve()


@pytest.mark.parametrize("search_engine", ["python_fallback", "ripgrep"])
def test_grep_leaves_out_files_whose_real_path_is_outside_the_workspace(
    linked_workspace: Path, monkeypatch: pytest.MonkeyPatch, search_engine: str
) -> None:
    workspace = linked_workspace
    if search_engine == "python_fallback":
        monkeypatch.setattr(deep_agents_filesystem, "_resolve_ripgrep_path", lambda: None)
    else:
        # Any engine that follows the link reports both files.
        def follow_links(*_: object, **__: object) -> tuple[dict[str, list[tuple[int, str]]], bool]:
            return {
                str(workspace / "own.txt"): [(1, f"{OUTSIDE_SECRET} in the workspace")],
                str(workspace / "leak.txt"): [(1, OUTSIDE_SECRET)],
                str(workspace / "leak-dir" / "outside.txt"): [(1, OUTSIDE_SECRET)],
            }, False

        monkeypatch.setattr(WorkspaceShellBackend, "_ripgrep_search", follow_links)
    backend = WorkspaceShellBackend(workspace)

    result = backend.grep(OUTSIDE_SECRET, str(workspace))

    assert result.matches is not None
    assert [match["path"] for match in result.matches] == [str(workspace / "own.txt")]


def test_ls_and_glob_leave_out_links_to_outside_the_workspace(linked_workspace: Path) -> None:
    workspace = linked_workspace
    backend = WorkspaceShellBackend(workspace)

    listed = backend.ls(str(workspace))
    found = backend.glob("**/*.txt", str(workspace))

    assert listed.entries is not None
    assert [entry["path"] for entry in listed.entries] == [str(workspace / "own.txt")]
    assert found.matches is not None
    assert [match["path"] for match in found.matches] == [str(workspace / "own.txt")]


def test_reads_and_writes_through_a_link_to_outside_the_workspace_are_refused(
    linked_workspace: Path,
) -> None:
    workspace = linked_workspace
    backend = WorkspaceShellBackend(workspace)

    read = backend.read(str(workspace / "leak.txt"))
    written = backend.write(str(workspace / "leak-dir" / "new.txt"), "never")

    assert read.error is not None and "outside the workspace" in read.error
    assert written.error is not None and "outside the workspace" in written.error
    assert not (workspace.parent / "outside" / "new.txt").exists()


def test_two_sessions_keep_their_own_large_tool_results(tmp_path: Path) -> None:
    artifacts = tmp_path / "artifacts"
    artifacts.mkdir(mode=0o700)
    backend = build_workspace_backend(tmp_path / "workspace", artifacts)
    result_path = "/large_tool_results/call-1-0-0"

    with bind_artifacts_session("session-a"):
        assert backend.write(result_path, "SESSION_A_RESULT").error is None
    with bind_artifacts_session("session-b"):
        assert backend.write(result_path, "SESSION_B_RESULT").error is None
        session_b = backend.read(result_path)
    with bind_artifacts_session("session-a"):
        session_a = backend.read(result_path)

    assert session_a.file_data is not None and session_b.file_data is not None
    assert session_a.file_data["content"] == "SESSION_A_RESULT"
    assert session_b.file_data["content"] == "SESSION_B_RESULT"
    session_directories = [path for path in artifacts.iterdir() if path.is_dir()]
    assert len(session_directories) == 2
    for directory in session_directories:
        assert stat.S_IMODE(directory.stat().st_mode) == 0o700
        assert stat.S_IMODE((directory / "large_tool_results").stat().st_mode) == 0o700


def test_artifacts_outside_a_session_fail(tmp_path: Path) -> None:
    artifacts = tmp_path / "artifacts"
    artifacts.mkdir(mode=0o700)
    backend = build_workspace_backend(tmp_path / "workspace", artifacts)

    written = backend.write("/large_tool_results/call-1-0-0", "never")

    assert written.error is not None and "while a session runs" in written.error
    assert list(artifacts.iterdir()) == []
