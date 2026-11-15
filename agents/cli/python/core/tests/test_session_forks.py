# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Session forks from the command line: the shared lines, and fork then open on an engine."""

from __future__ import annotations

import asyncio
import json
import re
from pathlib import Path
from typing import Any

import pexpect
import pytest
from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents import AGENT_STATE, SESSION_TERMINAL_LEASE, AgentSessionForkingFlow, AgentState
from dex_agents_cli_core.session_forks import (
    CHAT_FORK_USAGE_LINE,
    build_chat_fork_failure_line,
    build_fork_usage_error,
    build_forked_line,
    build_open_prompt,
    build_resume_hint,
    fork_task_queue,
    missing_session_error,
    new_cli_session_id,
    other_harness_fork_error,
    unsupported_server_error,
)
from dex_agents_cli_core.session_task_queues import session_task_queue
from tests.conftest import CliHome, config_yaml, expect, wait_for_exit
from tests.scripted_harness import SCRIPTED_FLOW_TYPE

AGENTS_DIR = Path(__file__).resolve().parents[4]
FIXTURE: dict[str, Any] = json.loads(
    (AGENTS_DIR / "conformance" / "session_fork.json").read_text(encoding="utf-8")
)
CLI = FIXTURE["cli"]
NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}
PROMPT = "\r\n> "
COMMAND = "dex-agents-scripted"
_SESSION_ID = re.compile(r"[a-z0-9-]+-\d{8}-\d{6}-[0-9a-f]{6}")


def test_the_cli_lines_match_the_shared_templates() -> None:
    values = {
        "source_session_id": "a-1",
        "after_message": 4,
        "session_id": "b-2",
        "command_name": COMMAND,
        "flow_type": "Other",
        "error": "no",
    }
    rendered = {
        "forked": build_forked_line("a-1", 4, "b-2"),
        "open_prompt": build_open_prompt("b-2"),
        "resume_hint": build_resume_hint(COMMAND, "b-2"),
        "usage": build_fork_usage_error(COMMAND),
        "chat_usage": CHAT_FORK_USAGE_LINE,
        "chat_failure": build_chat_fork_failure_line("no"),
        "unsupported_server": unsupported_server_error(COMMAND),
        "other_harness": other_harness_fork_error("b-2", "Other", COMMAND),
        "missing_session": missing_session_error("b-2", COMMAND),
    }
    for name, line in rendered.items():
        assert line == CLI[name].format(**values), name
    for case in CLI["lines"]:
        assert CLI[case["template"]].format(**case["values"]) == case["line"]


@pytest.mark.parametrize("case", CLI["task_queues"], ids=lambda case: case["name"])
def test_the_fork_task_queue_matches_the_shared_cases(case: dict[str, Any]) -> None:
    assert session_task_queue(case["source_session_id"]) == case["source_task_queue"] or (
        case["source_task_queue"] == ""
    )
    assert (
        fork_task_queue(case["source_session_id"], case["source_task_queue"], case["session_id"])
        == case["expected_task_queue"]
    )


def test_a_new_session_id_has_the_cli_format() -> None:
    assert _SESSION_ID.fullmatch(new_cli_session_id("builtin"))


async def _read_state(cli_home: CliHome, tmp_path: Path, session_id: str) -> tuple[Any, ...]:
    """Return the next message sequence, the lease, and the task queue; no Worker is needed."""
    cache_dir = tmp_path / "reader-cache"
    cache_dir.mkdir(exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([AgentSessionForkingFlow(SCRIPTED_FLOW_TYPE)], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=cli_home.address),
    )
    try:
        stored = await client.get_attributes(session_id, AGENT_STATE, SESSION_TERMINAL_LEASE)
        flow_state = await client.get_flow_state(session_id)
    finally:
        await client.close()
        blob_cache.close()
    state: AgentState = stored[AGENT_STATE]
    return state.next_message_sequence, stored.get(SESSION_TERMINAL_LEASE), flow_state.task_queue


async def _expect_regex(child: pexpect.spawn, pattern: str) -> re.Match[str]:
    await asyncio.to_thread(child.expect, pattern, timeout=60)
    match = child.match
    assert isinstance(match, re.Match)
    return match


async def test_fork_in_the_chat_opens_the_fork_and_the_source_keeps_working(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn()
    await expect(child, "Session ")
    await expect(child, PROMPT)
    child.sendline("first")
    await expect(child, "echo: first")
    await expect(child, PROMPT)
    child.sendline("second")
    await expect(child, "echo: second")
    await expect(child, PROMPT)
    child.sendline("/fork 1")
    await expect(child, "[not forked] message 1 of session ")
    await expect(child, PROMPT)

    child.sendline("/fork 2")
    match = await _expect_regex(child, r"Forked session (\S+) after message 2 into session (\S+)\.")
    source_id, fork_id = match.group(1), match.group(2)
    await expect(child, build_open_prompt(fork_id))
    child.sendline("y")
    await expect(child, f"Session {fork_id}")
    await expect(child, PROMPT)
    child.sendline("third")
    await expect(child, "echo: third")
    await expect(child, PROMPT)
    child.sendline("/exit")
    assert await wait_for_exit(child) == 0

    fork_messages, fork_lease, fork_queue = await _read_state(cli_home, tmp_path, fork_id)
    assert fork_messages == 5
    assert fork_lease is None
    assert fork_queue == session_task_queue(fork_id)

    resumed = cli_home.spawn("--resume", source_id)
    await expect(resumed, f"Session {source_id}")
    await expect(resumed, PROMPT)
    resumed.sendline("fourth")
    await expect(resumed, "echo: fourth")
    await expect(resumed, PROMPT)
    resumed.sendline("/exit")
    assert await wait_for_exit(resumed) == 0
    source_messages, _, _ = await _read_state(cli_home, tmp_path, source_id)
    assert source_messages == 7


async def test_the_fork_subcommand_forks_without_a_terminal_and_says_how_to_open_it(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn()
    await expect(child, "Session ")
    await expect(child, PROMPT)
    child.sendline("first")
    await expect(child, "echo: first")
    await expect(child, PROMPT)
    child.sendline("/exit")
    source_id = (await _expect_regex(child, r"Session (\S+) keeps running")).group(1)
    assert await wait_for_exit(child) == 0

    forked = cli_home.run("fork", source_id)
    assert forked.returncode == 0, forked.stderr
    found = re.search(
        rf"Forked session {source_id} after message 2 into session (\S+)\.", forked.stdout
    )
    assert found is not None, forked.stdout
    fork_id = found.group(1)
    assert build_resume_hint(COMMAND, fork_id) in forked.stdout
    fork_messages, _, fork_queue = await _read_state(cli_home, tmp_path, fork_id)
    assert (fork_messages, fork_queue) == (3, session_task_queue(fork_id))

    refused = cli_home.run("fork", source_id, "--at", "1")
    assert refused.returncode == 1
    assert "is inside a turn" in refused.stderr

    missing = cli_home.run("fork")
    assert missing.returncode == 2
    assert build_fork_usage_error(COMMAND) in missing.stderr
