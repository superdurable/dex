# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command with LiteLLM against the scripted Responses API: chat, tools, and settings."""

from __future__ import annotations

import base64
import hashlib
import json
import struct
import zlib
from pathlib import Path

from dex_agents_builtin_cli import BUILTIN_CLI_FLOW_TYPE
from dex_agents_cli import session_flow_type
from dex_agents_cli_testing import scenarios
from dex_agents_cli_testing.commands import CommandHome, expect, wait_for_exit
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
