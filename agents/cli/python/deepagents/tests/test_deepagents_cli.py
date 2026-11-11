# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command with Deep Agents against the scripted Responses API: chat, tools, and settings."""

from __future__ import annotations

from collections import Counter
from pathlib import Path

from dex_agents_cli import session_flow_type
from dex_agents_cli_testing import scenarios
from dex_agents_cli_testing.commands import CommandHome, call, expect, wait_for_exit
from dex_agents_cli_testing.fixtures import FakeConnectorsMcp
from dex_agents_deepagents_cli import DEEPAGENTS_CLI_FLOW_TYPE
from tests.conftest import MOCK_API_KEY

DEFAULTS: dict[str, object] = {}
DEEP_AGENTS_FILE_AND_SUBAGENT_TOOLS = {"ls", "glob", "grep", "execute", "task"}


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
        DEEPAGENTS_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_model(
        command_home, DEFAULTS, flow_type, "openai:gpt-5-mini", "gpt-5-mini", tmp_path / "cache"
    )


def test_a_key_in_the_deepagents_section_is_a_configuration_error(
    command_home: CommandHome,
) -> None:
    scenarios.harness_section_key_is_a_configuration_error(command_home, DEFAULTS, "deepagents")


async def test_the_runtime_tools_replace_deep_agents_file_tools_and_subagents(
    command_home: CommandHome,
) -> None:
    command_home.write_config({**scenarios.NO_CONNECTORS, "system_prompt": "Be brief."})
    child = command_home.spawn()
    await expect(child, "> ")
    child.sendline("prompt")
    await expect(child, "prompt: Be brief.")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    tools = command_home.model_api.model_requests()[-1].body["tools"]
    names = Counter(str(tool["name"]) for tool in tools)
    assert {"read_file", "write_file", "edit_file", "write_todos"} <= set(names)
    assert not DEEP_AGENTS_FILE_AND_SUBAGENT_TOOLS & set(names)
    assert max(names.values()) == 1


async def test_write_todos_runs_at_once_and_returns_the_plan(command_home: CommandHome) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    child = command_home.spawn()
    await expect(child, "> ")
    todos = [
        {"content": "read notes", "status": "in_progress"},
        {"content": "write summary", "status": "pending"},
    ]
    child.sendline(call("write_todos", {"todos": todos}))
    await expect(child, "write_todos: Updated the todo list:")
    await expect(child, "[pending] write summary")
    await expect(child, "> ")
    assert "[approval]" not in (child.before or "")
    child.sendeof()
    assert await wait_for_exit(child) == 0
