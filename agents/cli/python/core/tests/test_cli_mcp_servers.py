# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Remote MCP server tools: header variables, approvals, and secrets that never leak."""

from __future__ import annotations

import io
import os
import subprocess
import sys
from collections.abc import AsyncIterator
from dataclasses import dataclass
from pathlib import Path

import pytest

from dex_agents.testing import InvocationLog, find_free_ports
from dex_agents_cli_core import session_flow_type
from tests.conftest import (
    PACKAGE_DIR,
    CliHome,
    config_yaml,
    expect,
    session_client,
    wait_for_exit,
    wait_for_text,
)
from tests.fake_connectors_mcp import (
    INVOCATION_LOG_NAME,
    READY_LINE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
)
from tests.fake_remote_mcp import REJECTED_REQUESTS_LOG_NAME
from tests.scripted_harness import SCRIPTED_FLOW_TYPE
from tests.test_cli_chat import read_session_id

SENTINEL_TOKEN = "sk-sentinel-4f1c9a7e2b"
NO_LOCAL_TOOLS = {"files": False, "shell": {"enabled": False}, "web_fetch": False, "browser": False}


@dataclass
class FakeRemoteMcp:
    """A running fake stateless MCP server that requires one bearer token."""

    port: int
    state_dir: Path
    process: subprocess.Popen[bytes]

    @property
    def url(self) -> str:
        """Return its MCP endpoint."""
        return f"http://127.0.0.1:{self.port}/mcp"

    def runs(self, tool_name: str) -> int:
        """Return how many times a tool really ran."""
        names = InvocationLog(self.state_dir / INVOCATION_LOG_NAME).names()
        return sum(1 for name in names if name.split(" ")[0] == tool_name)

    def rejected_requests(self) -> int:
        """Return how many requests came without the expected token."""
        log = self.state_dir / REJECTED_REQUESTS_LOG_NAME
        return len(log.read_text().splitlines()) if log.exists() else 0


@pytest.fixture
async def remote_mcp(tmp_path: Path) -> AsyncIterator[FakeRemoteMcp]:
    """Start the fake remote MCP server on a free port, expecting the sentinel token."""
    (port,) = find_free_ports(1)
    state_dir = tmp_path / "fake-remote-mcp"
    state_dir.mkdir()
    log_path = tmp_path / "fake-remote-mcp.log"
    with log_path.open("w") as log:
        process = subprocess.Popen(
            [sys.executable, "-m", "tests.fake_remote_mcp", str(port), SENTINEL_TOKEN],
            cwd=PACKAGE_DIR,
            env={**os.environ, STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
            stdout=log,
            stderr=subprocess.STDOUT,
        )
    try:
        await wait_for_text(log_path, READY_LINE)
        yield FakeRemoteMcp(port=port, state_dir=state_dir, process=process)
    finally:
        process.kill()
        process.wait()


def _server(url: str, *, is_trusted: bool) -> dict[str, object]:
    return {
        "url": url,
        "headers": {"Authorization": "Bearer ${STUDIO_TOKEN}", "X-Client": "dex-tests"},
        "trust_tool_annotations": is_trusted,
    }


async def test_remote_mcp_tools_run_with_the_header_from_the_environment_and_their_approvals(
    cli_home: CliHome, remote_mcp: FakeRemoteMcp, tmp_path: Path
) -> None:
    cli_home.extra_environment["STUDIO_TOKEN"] = SENTINEL_TOKEN
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {"enabled": []},
                "mcp_servers": {
                    "lib": _server(remote_mcp.url, is_trusted=True),
                    "raw": _server(remote_mcp.url, is_trusted=False),
                    "studio": _server(remote_mcp.url, is_trusted=True),
                },
                "approval": {"always": ["studio__search_assets"]},
                "local_tools": NO_LOCAL_TOOLS,
            }
        )
    )
    child = cli_home.spawn()
    child.logfile_read = io.StringIO()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline("tools")
    await expect(
        child,
        "tools: ask_user,lib__publish_asset,lib__search_assets,raw__publish_asset,"
        "raw__search_assets,studio__publish_asset,studio__search_assets",
    )
    child.sendline("cached")
    await expect(child, "cached: lib__publish_asset,lib__search_assets,raw__publish_asset")
    # A trusted read-only tool runs at once.
    child.sendline('call lib__search_assets {"query": "cats"}')
    await expect(child, "done: lib__search_assets=found 2 assets for cats")
    # An untrusted server's read waits for approval.
    child.sendline('call raw__search_assets {"query": "dogs"}')
    await expect(child, '[approval] raw__search_assets {"query": "dogs"}')
    child.sendline("y")
    await expect(child, "done: raw__search_assets=found 2 assets for dogs")
    # approval.always gates a trusted read.
    child.sendline('call studio__search_assets {"query": "owls"}')
    await expect(child, "[approval] studio__search_assets")
    child.sendline("n")
    await expect(child, "[rejected]")
    await expect(child, "done: studio__search_assets=")
    # A trusted tool without hints still waits for approval.
    child.sendline('call lib__publish_asset {"asset": "a1"}')
    await expect(child, "[approval] lib__publish_asset")
    child.sendline("y")
    await expect(child, "done: lib__publish_asset=published a1")
    workspace = cli_home.home / "dex-agent-workspace"
    flow_type = session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        snapshot = await sessions.get_agent_snapshot(session_id)
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert remote_mcp.runs("search_assets") == 2
    assert remote_mcp.runs("publish_asset") == 1
    assert remote_mcp.rejected_requests() == 0
    output = child.logfile_read.getvalue()
    log_file = cli_home.home / ".dex" / "agents" / "scripted.log"
    for text in (output, repr(snapshot), log_file.read_text()):
        assert SENTINEL_TOKEN not in text
    assert "found 2 assets for cats" in repr(snapshot)


async def test_a_rejected_token_leaves_the_remote_tools_out_with_one_line(
    cli_home: CliHome, remote_mcp: FakeRemoteMcp
) -> None:
    cli_home.extra_environment["STUDIO_TOKEN"] = "sk-wrong-token"
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {"enabled": []},
                "mcp_servers": {"studio": _server(remote_mcp.url, is_trusted=True)},
                "local_tools": NO_LOCAL_TOOLS,
            }
        )
    )
    child = cli_home.spawn()
    child.logfile_read = io.StringIO()
    await expect(child, "MCP server tools are unavailable for now; each turn tries again. See ")
    await expect(child, "> ")
    child.sendline("tools")
    await expect(child, "tools: ask_user\r\n")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert remote_mcp.rejected_requests() > 0
    assert child.logfile_read.getvalue().count("MCP server tools are unavailable") == 1
    log_file = cli_home.home / ".dex" / "agents" / "scripted.log"
    assert "sk-wrong-token" not in log_file.read_text()


def test_an_unset_header_variable_stops_the_command_with_exit_code_2(cli_home: CliHome) -> None:
    cli_home.extra_environment["STUDIO_TOKEN"] = ""
    cli_home.write_config(
        config_yaml(
            {
                "mcp_servers": {
                    "studio": {
                        "url": "https://mcp.example.com/mcp",
                        "headers": {"Authorization": "Bearer ${STUDIO_TOKEN}"},
                    }
                }
            }
        )
    )
    completed = cli_home.run(stdin="")
    assert completed.returncode == 2, completed.stderr
    assert completed.stderr.strip().endswith(
        "mcp_servers.studio.headers.Authorization needs the environment variable "
        "STUDIO_TOKEN, which is not set or is empty."
    )
    assert "Dex Web" not in completed.stdout
