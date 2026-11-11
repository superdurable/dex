# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Terminal scenarios every harness command passes against its model API stub."""

from __future__ import annotations

from collections.abc import Mapping
from pathlib import Path
from typing import Any

from dex_agents_cli_testing.commands import (
    APPROVE_PROMPT,
    CommandHome,
    call,
    expect,
    read_session_id,
    session_client,
    wait_for_exit,
)
from dex_agents_cli_testing.fixtures import FakeConnectorsMcp

NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}


async def chat_turn_and_resume(home: CommandHome, config: Mapping[str, Any]) -> None:
    """Chat one turn, exit, and continue the session with ``--resume``."""
    home.write_config({**NO_CONNECTORS, **config})
    first = home.spawn()
    session_id = await read_session_id(first)
    await expect(first, "> ")
    first.sendline("say hello")
    await expect(first, "said: hello")
    await expect(first, "> ")
    first.sendeof()
    assert await wait_for_exit(first) == 0

    second = home.spawn("--resume", session_id)
    assert await read_session_id(second) == session_id
    await expect(second, "> ")
    second.sendline("recall")
    await expect(second, "history: say hello")
    second.sendeof()
    assert await wait_for_exit(second) == 0


async def local_file_tools(home: CommandHome, config: Mapping[str, Any]) -> None:
    """Run ``read_file`` at once, and ``write_file`` once, only after approval."""
    home.write_config({**NO_CONNECTORS, **config})
    home.workspace.mkdir(parents=True, exist_ok=True)
    (home.workspace / "notes.txt").write_text("buy milk\n")
    child = home.spawn()
    await expect(child, "> ")
    child.sendline(call("read_file", {"path": "notes.txt"}))
    await expect(child, "buy milk")
    await expect(child, "> ")
    assert "[approval]" not in (child.before or "")

    child.sendline(call("write_file", {"path": "todo.txt", "content": "call mom"}))
    await expect(child, "[approval] write_file")
    await expect(child, APPROVE_PROMPT)
    assert not (home.workspace / "todo.txt").exists()
    child.sendline("y")
    await expect(child, "write_file: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert (home.workspace / "todo.txt").read_text() == "call mom"


async def connector_read_and_approved_write(
    home: CommandHome, config: Mapping[str, Any], fake_mcp: FakeConnectorsMcp
) -> None:
    """Run a connector read at once, and a connector write once, after approval."""
    home.write_config({**config, "connectors": {"mcp": fake_mcp.url}})
    child = home.spawn()
    await expect(child, "> ")
    child.sendline(call("hackernews_get_top_stories", {}))
    await expect(child, "hackernews_get_top_stories: 1. Durable agents; 2. Local first")
    await expect(child, "> ")
    assert fake_mcp.runs("hackernews_get_top_stories") == 1

    child.sendline(call("slack_post_thread_reply", {"thread": "T1", "text": "on it"}))
    # Frameworks print the arguments with or without spaces after separators.
    await expect(child, "[approval] slack_post_thread_reply {")
    await expect(child, APPROVE_PROMPT)
    assert fake_mcp.runs("slack_post_thread_reply") == 0
    child.sendline("y")
    await expect(child, "slack_post_thread_reply: posted on it in T1")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert fake_mcp.runs("slack_post_thread_reply") == 1


async def session_settings_change_the_model(
    home: CommandHome,
    config: Mapping[str, Any],
    flow_type: str,
    model: str,
    model_seen_by_api: str,
    cache_dir: Path,
) -> None:
    """Switch the model in the session settings; the next turn's request names it."""
    home.write_config({**NO_CONNECTORS, **config})
    child = home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    async with session_client(home.address, cache_dir, flow_type) as sessions:
        await sessions.update_session_settings(session_id, {"model": model})
    child.sendline("model")
    await expect(child, f"model: {model_seen_by_api}")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0


def harness_section_key_is_a_configuration_error(
    home: CommandHome, config: Mapping[str, Any], section: str
) -> None:
    """Reject a key in the harness's section with exit code 2, before any model call."""
    home.write_config({**NO_CONNECTORS, **config, section: {"max_turns": 3}})
    completed = home.run(stdin="say never\n")
    assert completed.returncode == 2, completed.stdout + completed.stderr
    assert f'schema at "/{section}/max_turns"' in completed.stderr
    assert not home.model_api.model_requests()
