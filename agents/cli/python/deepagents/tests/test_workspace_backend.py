# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command's Deep Agents backend: workspace confinement and per-session artifacts."""

from __future__ import annotations

import hashlib
import os
import stat
import time
from datetime import timedelta
from pathlib import Path
from typing import Any

import pytest
from deepagents.backends import filesystem as deep_agents_filesystem

from dex_agents_deepagents_cli import (
    WorkspaceShellBackend,
    build_workspace_backend,
    remove_stale_session_artifacts,
)
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
    opened: list[Path] = []
    path_open = Path.open

    def recording_open(path: Path, *arguments: Any, **options: Any) -> Any:
        opened.append(path)
        return path_open(path, *arguments, **options)

    monkeypatch.setattr(Path, "open", recording_open)
    backend = WorkspaceShellBackend(workspace)

    result = backend.grep(OUTSIDE_SECRET, str(workspace))
    filtered = backend.grep(OUTSIDE_SECRET, str(workspace), "leak*")

    assert result.matches is not None
    assert [match["path"] for match in result.matches] == [str(workspace / "own.txt")]
    assert all("leak" not in match["path"] for match in filtered.matches or [])
    # The search never opens a file whose real path is outside the workspace.
    assert {path.name for path in opened} <= {"own.txt"}


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


def test_a_session_directory_that_is_a_link_is_an_error_result_not_an_exception(
    tmp_path: Path,
) -> None:
    artifacts = tmp_path / "artifacts"
    backend = build_workspace_backend(tmp_path / "workspace", artifacts)
    with bind_artifacts_session("session-a"):
        assert backend.write("/large_tool_results/call-1", "SESSION_A_RESULT").error is None
    [session_a] = list(artifacts.iterdir())
    (artifacts / _session_directory_name("session-b")).symlink_to(session_a)

    with bind_artifacts_session("session-b"):
        read = backend.read("/large_tool_results/call-1")
        written = backend.write("/large_tool_results/call-2", "never")
        listed = backend.ls("/large_tool_results/")
        found = backend.glob("*", "/large_tool_results/")
        searched = backend.grep("SESSION", "/large_tool_results/")

    assert read.error is not None and "not a directory" in read.error
    assert written.error is not None
    assert not (session_a / "large_tool_results" / "call-2").exists()
    assert not listed.entries
    assert not found.matches
    assert not searched.matches


def test_looser_artifact_directories_become_private(tmp_path: Path) -> None:
    artifacts = tmp_path / "artifacts"
    kind_directory = artifacts / _session_directory_name("session-a") / "large_tool_results"
    kind_directory.mkdir(parents=True)
    for directory in (artifacts, kind_directory.parent, kind_directory):
        directory.chmod(0o755)
    backend = build_workspace_backend(tmp_path / "workspace", artifacts)

    with bind_artifacts_session("session-a"):
        assert backend.write("/large_tool_results/call-1", "result").error is None

    for directory in (artifacts, kind_directory.parent, kind_directory):
        assert stat.S_IMODE(directory.stat().st_mode) == 0o700


def test_stale_session_artifacts_are_removed_without_following_links(tmp_path: Path) -> None:
    artifacts = tmp_path / "artifacts"
    backend = build_workspace_backend(tmp_path / "workspace", artifacts)
    for session_id in ("stale", "fresh"):
        with bind_artifacts_session(session_id):
            assert backend.write("/large_tool_results/call-1", session_id).error is None
    stale = artifacts / _session_directory_name("stale")
    fresh = artifacts / _session_directory_name("fresh")
    long_ago = time.time() - timedelta(days=31).total_seconds()
    for path in (stale / "large_tool_results" / "call-1", stale / "large_tool_results", stale):
        os.utime(path, (long_ago, long_ago))
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "keep.txt").write_text("keep")
    os.utime(outside, (long_ago, long_ago))
    (artifacts / "linked").symlink_to(outside)

    removed = remove_stale_session_artifacts(artifacts)

    assert removed == [stale]
    assert not stale.exists()
    assert (fresh / "large_tool_results" / "call-1").read_text() == "fresh"
    assert (outside / "keep.txt").read_text() == "keep"


def _session_directory_name(session_id: str) -> str:
    return hashlib.sha256(session_id.encode()).hexdigest()
