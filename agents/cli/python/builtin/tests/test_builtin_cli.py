# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command with LiteLLM against the scripted Responses API: chat, tools, and settings."""

from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import struct
import zlib
from pathlib import Path
from typing import Any

from dex_agents import AgentPlan, AgentSessionClient, PlanStatus
from dex_agents_builtin_cli import BUILTIN_CLI_FLOW_TYPE
from dex_agents_cli_core import session_flow_type
from dex_agents_cli_testing import scenarios
from dex_agents_cli_testing.commands import (
    CommandHome,
    call,
    expect,
    read_session_id,
    session_client,
    wait_for_exit,
)
from dex_agents_cli_testing.fixtures import FakeConnectorsMcp
from tests.conftest import MOCK_API_KEY

DEFAULTS: dict[str, object] = {}


async def test_a_turn_answers_and_resume_continues_the_session(command_home: CommandHome) -> None:
    await scenarios.chat_turn_and_resume(command_home, DEFAULTS)
    requests = command_home.model_api.model_requests()
    assert requests[0].body["model"] == "gpt-5.2"
    assert {request.headers.get("authorization") for request in requests} == {
        f"Bearer {MOCK_API_KEY}"
    }


async def test_read_file_runs_at_once_and_write_file_after_approval(
    command_home: CommandHome,
) -> None:
    await scenarios.local_file_tools(command_home, DEFAULTS)


async def test_a_connector_read_runs_at_once_and_a_write_runs_once_after_approval(
    command_home: CommandHome, fake_mcp: FakeConnectorsMcp
) -> None:
    await scenarios.connector_read_and_approved_write(command_home, DEFAULTS, fake_mcp)


async def test_session_settings_change_the_model(command_home: CommandHome, tmp_path: Path) -> None:
    flow_type = session_flow_type(
        BUILTIN_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_model(
        command_home, DEFAULTS, flow_type, "openai/gpt-5-mini", "gpt-5-mini", tmp_path / "cache"
    )


def test_a_key_in_the_builtin_section_is_a_configuration_error(command_home: CommandHome) -> None:
    scenarios.harness_section_key_is_a_configuration_error(command_home, DEFAULTS, "builtin")


async def test_session_settings_change_the_instructions(
    command_home: CommandHome, tmp_path: Path
) -> None:
    flow_type = session_flow_type(
        BUILTIN_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_instructions(
        command_home, DEFAULTS, flow_type, tmp_path / "cache"
    )


async def test_rejected_local_and_connector_writes_never_run(
    command_home: CommandHome, fake_mcp: FakeConnectorsMcp
) -> None:
    await scenarios.rejected_writes_never_run(command_home, DEFAULTS, fake_mcp)


async def test_an_attached_image_reaches_the_responses_api(command_home: CommandHome) -> None:
    png = png_bytes()
    (command_home.home / "chart.png").write_bytes(png)
    command_home.write_config(scenarios.NO_CONNECTORS)
    child = command_home.spawn()
    await expect(child, "> ")
    child.sendline("/image ~/chart.png")
    await expect(child, "[image attached] chart.png (image/png")
    child.sendline("say hi")
    await expect(child, "said: hi")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    stored = command_home.home / ".dex" / "agents" / "media" / "sha256"
    assert (stored / hashlib.sha256(png).hexdigest()).read_bytes() == png
    data_url = "data:image/png;base64," + base64.b64encode(png).decode("ascii")
    bodies = [json.dumps(request.body) for request in command_home.model_api.model_requests()]
    assert any(data_url in body for body in bodies), bodies


async def test_plan_mode_drafts_a_plan_that_execute_plan_runs(
    command_home: CommandHome, tmp_path: Path
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    child = command_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    flow_type = session_flow_type(
        BUILTIN_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    todos = [{"content": "Read the notes", "status": "pending"}]
    async with session_client(command_home.address, tmp_path / "cache", flow_type) as sessions:
        before_planning = len(command_home.model_api.model_requests())
        planning_message = call("write_todos", {"todos": todos})
        assert await sessions.send_user_message(session_id, planning_message, plan_mode=True)
        draft = await wait_for_plan(sessions, session_id, PlanStatus.DRAFT)
        assert [task.content for task in draft.tasks] == ["Read the notes"]
        planning_requests = command_home.model_api.model_requests()[before_planning:]
        assert planning_requests
        assert all(tool_names(request.body) == {"write_todos"} for request in planning_requests)
        before_running = len(command_home.model_api.model_requests())
        # The draft is stored before its planning turn ends; execute_plan waits for that end.
        assert await execute_plan_when_turn_ends(sessions, session_id, draft.revision)
        await wait_for_plan(sessions, session_id, PlanStatus.ACTIVE)
    running_request = command_home.model_api.model_requests()[before_running]
    assert {"write_todos", "read_file"} <= tool_names(running_request.body)
    child.sendeof()
    assert await wait_for_exit(child) == 0


async def test_a_planning_turn_cannot_call_another_tool(
    command_home: CommandHome, tmp_path: Path
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    command_home.workspace.mkdir(parents=True, exist_ok=True)
    (command_home.workspace / "notes.txt").write_text("buy milk\n")
    child = command_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    flow_type = session_flow_type(
        BUILTIN_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    async with session_client(command_home.address, tmp_path / "cache", flow_type) as sessions:
        planning_message = call("read_file", {"path": "notes.txt"})
        assert await sessions.send_user_message(session_id, planning_message, plan_mode=True)
        for _ in range(300):
            snapshot = await sessions.get_agent_snapshot(session_id)
            if "unbound read_file" in str(snapshot.messages):
                break
            await asyncio.sleep(0.1)
        else:
            raise AssertionError("the planning turn never answered")
        assert snapshot.plan is None
        assert snapshot.tool_calls == []
    assert "buy milk" not in json.dumps([r.body for r in command_home.model_api.model_requests()])
    child.sendeof()
    assert await wait_for_exit(child) == 0


def tool_names(body: dict[str, Any]) -> set[str]:
    """Return the names of the tools a Responses API request offers."""
    return {str(tool.get("name")) for tool in body.get("tools") or []}


async def wait_for_plan(
    sessions: AgentSessionClient, session_id: str, status: PlanStatus
) -> AgentPlan:
    """Poll the session until its plan has a status, and return the plan."""
    for _ in range(300):
        snapshot = await sessions.get_agent_snapshot(session_id)
        if snapshot.plan is not None and snapshot.plan.status is status:
            return snapshot.plan
        await asyncio.sleep(0.1)
    raise AssertionError(f"the plan of {session_id} never became {status}")


def png_bytes() -> bytes:
    """Return a 1x1 PNG."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        checksum = struct.pack(">I", zlib.crc32(kind + data))
        return struct.pack(">I", len(data)) + kind + data + checksum

    header = struct.pack(">IIBBBBB", 1, 1, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(b"\x00\xff\x00\x00", 9))
        + chunk(b"IEND", b"")
    )


async def execute_plan_when_turn_ends(
    sessions: AgentSessionClient, session_id: str, revision: int, timeout: float = 30.0
) -> bool:
    """Returns whether execute_plan accepted the revision before the timeout."""
    deadline = asyncio.get_running_loop().time() + timeout
    while True:
        if await sessions.execute_plan(session_id, revision):
            return True
        if asyncio.get_running_loop().time() >= deadline:
            return False
        await asyncio.sleep(0.1)
