# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command against the scripted Responses API: chat, tools, and session settings."""

from __future__ import annotations

from pathlib import Path

from dex_agents_cli import session_flow_type
from dex_agents_cli_testing import scenarios
from dex_agents_cli_testing.commands import CommandHome
from dex_agents_cli_testing.fixtures import FakeConnectorsMcp
from dex_agents_openai_sdk_cli import OPENAI_SDK_CLI_FLOW_TYPE
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
        OPENAI_SDK_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_model(
        command_home, DEFAULTS, flow_type, "gpt-5-mini", "gpt-5-mini", tmp_path / "cache"
    )


def test_a_key_in_the_openai_section_is_a_configuration_error(command_home: CommandHome) -> None:
    scenarios.harness_section_key_is_a_configuration_error(command_home, DEFAULTS, "openai")


async def test_session_settings_change_the_instructions(
    command_home: CommandHome, tmp_path: Path
) -> None:
    flow_type = session_flow_type(
        OPENAI_SDK_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_instructions(
        command_home, DEFAULTS, flow_type, tmp_path / "cache"
    )


async def test_rejected_local_and_connector_writes_never_run(
    command_home: CommandHome, fake_mcp: FakeConnectorsMcp
) -> None:
    await scenarios.rejected_writes_never_run(command_home, DEFAULTS, fake_mcp)


def test_without_a_connectors_mcp_server_at_startup_it_says_to_restart(
    command_home: CommandHome,
) -> None:
    # This dexcli dev serves no connectors MCP server, as with one that is still starting.
    command_home.write_config({})
    completed = command_home.run(stdin="say hi\n")
    assert completed.returncode == 0, completed.stderr
    assert "said: hi" in completed.stdout
    assert (
        "dex-agents-openai-sdk fixes its tools when it starts, so this run has no connector "
        "tools. Restart it once the connectors MCP server answers."
    ) in completed.stdout
