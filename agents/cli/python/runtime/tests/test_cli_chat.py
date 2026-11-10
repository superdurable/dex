# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Terminal chat: a turn, approvals, questions, Ctrl-C, and resuming a session."""

from __future__ import annotations

import asyncio
import json
from typing import Any

import pexpect

from dex_agents.testing import EXECUTE_COMPLETED_EVENT, read_step_history
from tests.conftest import CliHome, config_yaml, expect, wait_for_exit

NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}


async def read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


async def count_executed_steps(cli_home: CliHome, session_id: str, step_type: str) -> int:
    history = await read_step_history(cli_home.address, session_id)
    return sum(
        1
        for entry in history
        if entry.step_type == step_type and entry.event_type == EXECUTE_COMPLETED_EVENT
    )


async def test_an_approved_write_runs_once_and_a_rejected_one_never_runs(
    cli_home: CliHome,
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    workspace = cli_home.home / "dex-agent-workspace"
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline('call write_file {"path": "notes/today.txt", "content": "buy milk"}')
    await expect(child, '[approval] write_file {"path": "notes/today.txt"')
    await expect(child, "Approve? [y/N, or n <reason>]")
    child.sendline("y")
    await expect(child, "[approved]")
    await expect(child, "done: write_file=wrote 8 bytes to notes/today.txt")
    await expect(child, "> ")
    assert (workspace / "notes" / "today.txt").read_text() == "buy milk"

    child.sendline('call write_file {"path": "other.txt", "content": "no"}')
    await expect(child, "[approval] write_file")
    child.sendline("n not today")
    await expect(child, "[rejected]")
    await expect(child, "done: write_file=")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert not (workspace / "other.txt").exists()
    assert await count_executed_steps(cli_home, session_id, "CallTool") == 1


async def test_a_question_is_answered_in_the_terminal(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline("ask")
    await expect(child, "[question] Which color?")
    await expect(child, "2) blue")
    child.sendline("2")
    await expect(child, "[answered]")
    await expect(child, 'done: ask_user={"status":"answered","answer":"blue"}')
    child.sendeof()
    assert await wait_for_exit(child) == 0


async def test_ctrl_c_cancels_the_turn_and_ctrl_c_while_idle_exits(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline("slow")
    await asyncio.sleep(2)
    child.sendintr()
    await expect(child, "[cancelling the turn; Ctrl-C again exits]")
    await expect(child, "[turn cancelled]")
    await expect(child, "> ")
    child.sendline("still here")
    await expect(child, "echo: still here")
    await expect(child, "> ")
    child.sendintr()
    await expect(
        child, f"The session is kept. Resume it with: dex-agents-scripted --resume {session_id}"
    )
    assert await wait_for_exit(child) == 0


async def test_resume_continues_the_conversation(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    first = cli_home.spawn()
    session_id = await read_session_id(first)
    await expect(first, "> ")
    first.sendline("remember the apple")
    await expect(first, "echo: remember the apple")
    first.sendeof()
    assert await wait_for_exit(first) == 0

    second = cli_home.spawn("--resume", session_id)
    assert await read_session_id(second) == session_id
    await expect(second, "> ")
    second.sendline("history")
    await expect(second, "history: remember the apple")
    second.sendeof()
    assert await wait_for_exit(second) == 0


async def test_resume_of_an_unknown_session_fails(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    completed = cli_home.run("--resume", "no-such-session")
    assert completed.returncode == 2
    assert "there is no dex-agents-scripted session no-such-session" in completed.stderr


async def test_sessions_lists_this_agents_sessions(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    completed = cli_home.run("sessions")
    assert completed.returncode == 0, completed.stderr
    assert session_id in completed.stdout
    assert json.dumps(session_id)[1:-1] in completed.stdout
