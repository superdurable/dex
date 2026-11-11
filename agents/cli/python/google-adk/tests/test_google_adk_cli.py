# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command with Google ADK against the scripted Gemini API: chat, tools, and settings."""

from __future__ import annotations

from pathlib import Path

from dex_agents_cli import session_flow_type
from dex_agents_cli_testing import scenarios
from dex_agents_cli_testing.commands import CommandHome
from dex_agents_cli_testing.fixtures import FakeConnectorsMcp
from dex_agents_google_adk_cli import GOOGLE_ADK_CLI_FLOW_TYPE
from tests.conftest import MOCK_API_KEY

DEFAULTS: dict[str, object] = {}


async def test_a_turn_answers_and_resume_continues_the_session(command_home: CommandHome) -> None:
    await scenarios.chat_turn_and_resume(command_home, DEFAULTS)
    requests = command_home.model_api.model_requests()
    assert "/models/gemini-2.5-flash:streamGenerateContent" in requests[0].path
    assert {request.headers.get("x-goog-api-key") for request in requests} == {MOCK_API_KEY}


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
        GOOGLE_ADK_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_model(
        command_home,
        DEFAULTS,
        flow_type,
        "gemini-2.5-pro",
        "gemini-2.5-pro",
        tmp_path / "cache",
    )


def test_a_key_in_the_google_adk_section_is_a_configuration_error(
    command_home: CommandHome,
) -> None:
    scenarios.harness_section_key_is_a_configuration_error(command_home, DEFAULTS, "google_adk")
