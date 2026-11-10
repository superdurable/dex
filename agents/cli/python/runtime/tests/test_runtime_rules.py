# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Rules that a session cannot reach: the Flow type namespace, the download URL, and edit races."""

from __future__ import annotations

import hashlib
import socket
from pathlib import Path

import pytest

from dex_agents import ToolExecutionError
from dex_agents_cli import session_flow_type
from dex_agents_cli.environment import RuntimeEnvironment
from dex_agents_cli.local_tools.files import WorkspaceFiles


def test_the_flow_type_hashes_host_user_config_and_workspace(tmp_path: Path) -> None:
    config = tmp_path / "agent.yaml"
    config.write_text("{}")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    text = f"{socket.gethostname()}\n1000\n{config.resolve()}\n{workspace.resolve()}"
    expected = hashlib.sha256(text.encode("utf-8")).hexdigest()[:12]
    assert session_flow_type("Agent", config, workspace, user="1000") == f"Agent-{expected}"


def test_two_users_with_defaults_get_different_flow_types(tmp_path: Path) -> None:
    first = session_flow_type("Agent", None, tmp_path, user="1000")
    second = session_flow_type("Agent", None, tmp_path, user="1001")
    assert first != second
    defaults = f"{socket.gethostname()}\n1000\ndefaults\n{tmp_path.resolve()}"
    assert first == f"Agent-{hashlib.sha256(defaults.encode()).hexdigest()[:12]}"


def test_another_workspace_gets_another_flow_type(tmp_path: Path) -> None:
    (tmp_path / "a").mkdir()
    (tmp_path / "b").mkdir()
    assert session_flow_type("Agent", None, tmp_path / "a") != session_flow_type(
        "Agent", None, tmp_path / "b"
    )


@pytest.mark.parametrize(
    "url",
    ["https://mirror.example/releases", "http://127.0.0.1:8000", "http://localhost/x"],
)
def test_a_download_url_must_be_https_or_loopback_http(tmp_path: Path, url: str) -> None:
    environment = RuntimeEnvironment.from_environment(
        {"HOME": str(tmp_path), "DEX_AGENTS_DEXCLI_DOWNLOAD_URL": url}
    )
    assert environment.dexcli_release_base_url == url


@pytest.mark.parametrize(
    "url", ["http://mirror.example/releases", "ftp://127.0.0.1/x", "file:///x"]
)
def test_an_insecure_download_url_is_refused(tmp_path: Path, url: str) -> None:
    with pytest.raises(ValueError, match="must be an https URL"):
        RuntimeEnvironment.from_environment(
            {"HOME": str(tmp_path), "DEX_AGENTS_DEXCLI_DOWNLOAD_URL": url}
        )


class _FilesChangedAfterRead(WorkspaceFiles):
    """Simulates another writer that changes the file between edit_file's read and write."""

    def _read_text_and_identity(self, path: str) -> tuple[str, object]:  # type: ignore[override]
        result = super()._read_text_and_identity(path)
        (self._root / path).write_text("changed by someone else, one two")
        return result


def test_edit_file_refuses_a_file_that_changed_since_it_was_read(tmp_path: Path) -> None:
    (tmp_path / "notes.txt").write_text("one two")
    files = _FilesChangedAfterRead(tmp_path)
    with pytest.raises(ToolExecutionError, match="changed while it was being edited"):
        files.edit_file("notes.txt", "one", "three")
    assert (tmp_path / "notes.txt").read_text() == "changed by someone else, one two"
    assert sorted(path.name for path in tmp_path.iterdir()) == ["notes.txt"]


def test_edit_file_reports_how_often_the_text_occurs(tmp_path: Path) -> None:
    (tmp_path / "notes.txt").write_text("one one")
    files = WorkspaceFiles(tmp_path)
    with pytest.raises(ToolExecutionError, match="does not occur"):
        files.edit_file("notes.txt", "two", "three")
    with pytest.raises(ToolExecutionError, match="occurs 2 times"):
        files.edit_file("notes.txt", "one", "three")
    assert (
        files.edit_file("notes.txt", "one one", "two") == "edited notes.txt: replaced 1 occurrence"
    )
    assert (tmp_path / "notes.txt").read_text() == "two"
