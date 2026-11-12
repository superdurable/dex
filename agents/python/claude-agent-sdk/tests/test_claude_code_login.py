# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A resumed CLI keeps the secure-storage key of the application's Claude Code login."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from tests.claude_test_agent import EXTRA_CLI_ENVIRONMENT_VARIABLE
from tests.conftest import MOCK_API_KEY, ClaudeAgentHarness
from tests.fake_claude_cli import CLI_ENVIRONMENT_LOG_NAME

SECURE_STORAGE_VARIABLE = "CLAUDE_SECURESTORAGE_CONFIG_DIR"
APPLICATION_SECURE_STORAGE_DIR = "/srv/claude/secure-storage"


async def test_resumed_cli_keys_secure_storage_by_the_original_config_dir(
    harness: ClaudeAgentHarness, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv(SECURE_STORAGE_VARIABLE, raising=False)
    await harness.stack.start_worker()
    new_cli, resumed_cli = await run_two_turns(harness)

    config_dir = str(harness.state_dir / "claude-config")
    assert new_cli == {
        "kind": "new",
        "CLAUDE_CONFIG_DIR": config_dir,
        SECURE_STORAGE_VARIABLE: config_dir,
    }
    assert resumed_cli["kind"] == "resume"
    # The SDK resumes from a temporary config directory.
    assert resumed_cli["CLAUDE_CONFIG_DIR"] not in (None, config_dir)
    assert resumed_cli[SECURE_STORAGE_VARIABLE] == config_dir


async def test_resumed_cli_keys_secure_storage_by_the_default_without_a_config_dir(
    harness: ClaudeAgentHarness, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.delenv("CLAUDE_CONFIG_DIR", raising=False)
    monkeypatch.delenv(SECURE_STORAGE_VARIABLE, raising=False)
    # Keeps the Worker and the CLI away from the host's Claude config and keychain.
    home = tmp_path / "home"
    home.mkdir()
    monkeypatch.setenv("HOME", str(home))
    await harness.stack.start_worker(
        extra_env={
            EXTRA_CLI_ENVIRONMENT_VARIABLE: json.dumps(
                {"CLAUDE_CONFIG_DIR": None, "ANTHROPIC_API_KEY": MOCK_API_KEY}
            )
        }
    )
    new_cli, resumed_cli = await run_two_turns(harness)

    assert new_cli == {"kind": "new", "CLAUDE_CONFIG_DIR": None, SECURE_STORAGE_VARIABLE: ""}
    assert resumed_cli["kind"] == "resume"
    assert resumed_cli["CLAUDE_CONFIG_DIR"] is not None
    assert resumed_cli[SECURE_STORAGE_VARIABLE] == ""


async def test_application_secure_storage_dir_reaches_every_cli(
    harness: ClaudeAgentHarness, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv(SECURE_STORAGE_VARIABLE, raising=False)
    await harness.stack.start_worker(
        extra_env={
            EXTRA_CLI_ENVIRONMENT_VARIABLE: json.dumps(
                {SECURE_STORAGE_VARIABLE: APPLICATION_SECURE_STORAGE_DIR}
            )
        }
    )
    new_cli, resumed_cli = await run_two_turns(harness)

    assert new_cli[SECURE_STORAGE_VARIABLE] == APPLICATION_SECURE_STORAGE_DIR
    assert resumed_cli["kind"] == "resume"
    assert resumed_cli[SECURE_STORAGE_VARIABLE] == APPLICATION_SECURE_STORAGE_DIR


async def run_two_turns(harness: ClaudeAgentHarness) -> list[dict[str, Any]]:
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say one")
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.send_user_message(session_id, "say two")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == "said: two"
    log = (harness.state_dir / CLI_ENVIRONMENT_LOG_NAME).read_text()
    records: list[dict[str, Any]] = [json.loads(line) for line in log.splitlines()]
    assert [record["kind"] for record in records] == ["new", "resume"]
    return records
