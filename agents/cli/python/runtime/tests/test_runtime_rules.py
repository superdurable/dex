# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Rules that a session cannot reach: the Flow type namespace, the download URL, and edit races."""

from __future__ import annotations

import json
import re
import socket
from pathlib import Path
from typing import Any

import pytest

from dex_agents import ToolExecutionError
from dex_agents_cli import (
    SessionEnvironmentIdentity,
    current_session_environment_identity,
    namespace_session_flow_type,
    session_flow_type,
)
from dex_agents_cli.dexcli_install import DexcliInstallError, check_download_url
from dex_agents_cli.environment import RuntimeEnvironment
from dex_agents_cli.harness import session_start_environment
from dex_agents_cli.local_tools.files import MAX_EDIT_BYTES, WorkspaceFiles

FLOW_TYPE_FIXTURE: dict[str, Any] = json.loads(
    (Path(__file__).resolve().parents[4] / "conformance" / "cli_session_flow_types.json").read_text(
        encoding="utf-8"
    )
)


@pytest.mark.parametrize("case", FLOW_TYPE_FIXTURE["cases"], ids=lambda case: case["name"])
def test_the_flow_type_matches_the_shared_cases(case: dict[str, Any]) -> None:
    identity = SessionEnvironmentIdentity(
        host_name=case["host_name"],
        user_id=case["user_id"],
        config_identity=case["config_identity"],
        workspace=case["workspace"],
    )
    harness_flow_type = FLOW_TYPE_FIXTURE["harness_flow_type"]
    assert namespace_session_flow_type(harness_flow_type, identity) == case["flow_type"]


def test_the_flow_type_hashes_host_user_config_and_workspace(tmp_path: Path) -> None:
    config = tmp_path / "agent.yaml"
    config.write_text("{}")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    identity = current_session_environment_identity(config, workspace, user="1000")
    assert identity == SessionEnvironmentIdentity(
        host_name=socket.gethostname(),
        user_id="1000",
        config_identity=str(config.resolve()),
        workspace=str(workspace.resolve()),
    )
    flow_type = session_flow_type("Agent", config, workspace, user="1000")
    assert flow_type == namespace_session_flow_type("Agent", identity)
    assert re.fullmatch(r"Agent-[0-9a-z]{6}", flow_type)


def test_two_users_with_defaults_get_different_flow_types(tmp_path: Path) -> None:
    first = session_flow_type("Agent", None, tmp_path, user="1000")
    second = session_flow_type("Agent", None, tmp_path, user="1001")
    assert first != second
    defaults = current_session_environment_identity(None, tmp_path, user="1000")
    assert defaults.config_identity == "defaults"
    assert first == namespace_session_flow_type("Agent", defaults)


def test_a_new_session_records_the_hashed_values_and_the_user_name(tmp_path: Path) -> None:
    identity = current_session_environment_identity(None, tmp_path, user="1000")
    environment = session_start_environment(identity)
    assert {key: value for key, value in environment.items() if key != "user_name"} == {
        "host_name": identity.host_name,
        "user_id": "1000",
        "config_identity": "defaults",
        "workspace": str(tmp_path.resolve()),
    }
    assert environment["user_name"] != ""


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
def test_a_download_url_must_be_https_or_loopback_http(url: str) -> None:
    check_download_url(url)


@pytest.mark.parametrize(
    "url", ["http://mirror.example/releases", "ftp://127.0.0.1/x", "file:///x"]
)
def test_an_insecure_download_url_is_refused(url: str) -> None:
    with pytest.raises(DexcliInstallError, match="is not an https URL"):
        check_download_url(url)


def test_the_download_url_is_checked_only_when_downloading(tmp_path: Path) -> None:
    environment = RuntimeEnvironment.from_environment(
        {"HOME": str(tmp_path), "DEX_AGENTS_DEXCLI_DOWNLOAD_URL": "http://mirror.example"}
    )
    assert environment.dexcli_release_base_url == "http://mirror.example"


class _FilesChangedAfterRead(WorkspaceFiles):
    """Simulates another writer that changes the file between edit_file's read and write."""

    def _read_text_and_identity(  # type: ignore[override]
        self, path: str, limit: int = MAX_EDIT_BYTES
    ) -> tuple[str, object]:
        result = super()._read_text_and_identity(path, limit)
        (self._root / path).write_text("changed by someone else, one two")
        return result


def test_edit_file_refuses_a_file_that_changed_since_it_was_read(tmp_path: Path) -> None:
    (tmp_path / "notes.txt").write_text("one two")
    files = _FilesChangedAfterRead(tmp_path)
    with pytest.raises(ToolExecutionError, match="read the file again and retry the edit"):
        files.edit_file("notes.txt", "one", "three")
    assert (tmp_path / "notes.txt").read_text() == "changed by someone else, one two"
    assert sorted(path.name for path in tmp_path.iterdir()) == ["notes.txt"]


def test_edit_file_refuses_a_file_over_its_limit(tmp_path: Path) -> None:
    content = "OLD" + "x" * (MAX_EDIT_BYTES - 3) + "TAIL"
    (tmp_path / "large.txt").write_text(content)
    files = WorkspaceFiles(tmp_path)
    with pytest.raises(
        ToolExecutionError,
        match=f"is larger than {MAX_EDIT_BYTES} bytes; edit_file does not change it",
    ):
        files.edit_file("large.txt", "OLD", "NEW")
    assert (tmp_path / "large.txt").read_text() == content


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
