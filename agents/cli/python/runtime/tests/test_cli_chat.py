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
import os
import signal
import socket
from pathlib import Path
from typing import Any

import pexpect

from dex_agents import AgentSnapshot, MessageRole
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    read_step_history,
    resolve_dexcli_path,
    wait_for_agent_snapshot,
)
from dex_agents_cli import session_flow_type
from tests.conftest import CliHome, config_yaml, expect, session_client, wait_for_exit
from tests.scripted_harness import SCRIPTED_FLOW_TYPE

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


async def read_start_flow_input(cli_home: CliHome, session_id: str) -> tuple[str, Any]:
    process = await asyncio.create_subprocess_exec(
        str(resolve_dexcli_path()),
        "flow",
        "history",
        session_id,
        "--server",
        cli_home.address,
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
    )
    stdout, _ = await process.communicate()
    assert process.returncode == 0
    started = json.loads(stdout)["events"][0]["payload"]
    return started["flowType"], started["initialStart"]["stepInput"]


async def test_a_new_session_records_where_the_command_runs(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    workspace = cli_home.home / "dex-agent-workspace"
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    flow_type, start_input = await read_start_flow_input(cli_home, session_id)
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert flow_type == session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    environment = start_input["start_environment"]
    assert environment.pop("user_name") != ""
    assert environment == {
        "host_name": socket.gethostname(),
        "user_id": str(os.getuid()),
        "config_identity": os.path.realpath(cli_home.config_path),
        "workspace": os.path.realpath(workspace),
    }


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
        child,
        f"Session {session_id} keeps running. "
        f"Resume it with: dex-agents-scripted --resume {session_id}",
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


async def test_resume_with_another_workspace_fails_with_a_hint(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    first = cli_home.spawn()
    session_id = await read_session_id(first)
    await expect(first, "> ")
    first.sendeof()
    assert await wait_for_exit(first) == 0
    cli_home.write_config(config_yaml({**NO_CONNECTORS, "workspace": str(tmp_path / "other")}))
    completed = await asyncio.to_thread(cli_home.run, "--resume", session_id)
    assert completed.returncode == 2
    assert "with the configuration file and workspace, that started it" in completed.stderr


async def test_resume_of_an_unknown_session_fails(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    completed = cli_home.run("--resume", "no-such-session")
    assert completed.returncode == 2
    assert "session no-such-session does not exist" in completed.stderr


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


async def test_a_failed_turn_makes_the_command_exit_1(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    completed = await asyncio.to_thread(cli_home.run, stdin="fail\n")
    assert "[turn failed]" in completed.stdout
    assert "the scripted model was told to fail" in completed.stdout
    assert completed.returncode == 1


async def test_two_commands_with_other_configs_never_run_each_others_tool_steps(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_workspace = tmp_path / "first-workspace"
    second_workspace = tmp_path / "second-workspace"
    first_config = tmp_path / "first.yaml"
    second_config = tmp_path / "second.yaml"
    first_config.write_text(config_yaml({**NO_CONNECTORS, "workspace": str(first_workspace)}))
    second_config.write_text(config_yaml({**NO_CONNECTORS, "workspace": str(second_workspace)}))
    first = cli_home.spawn("--config", str(first_config))
    second = cli_home.spawn("--config", str(second_config))
    await read_session_id(first)
    await read_session_id(second)
    await expect(first, "> ")
    await expect(second, "> ")
    # The second command's Worker polls the same Dex server while the first approves writes.
    for attempt in range(5):
        first.sendline(f'call write_file {{"path": "note-{attempt}.txt", "content": "first"}}')
        await expect(first, "Approve? [y/N, or n <reason>]")
        first.sendline("y")
        await expect(first, f"done: write_file=wrote 5 bytes to note-{attempt}.txt")
    first.sendeof()
    second.sendeof()
    assert await wait_for_exit(first) == 0
    assert await wait_for_exit(second) == 0
    assert sorted(path.name for path in first_workspace.iterdir()) == [
        f"note-{attempt}.txt" for attempt in range(5)
    ]
    assert list(second_workspace.iterdir()) == []


async def test_a_worker_whose_config_switches_shell_off_refuses_a_shell_step_made_elsewhere(
    cli_home: CliHome, tmp_path: Path
) -> None:
    workspace = cli_home.home / "dex-agent-workspace"
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    first = cli_home.spawn()
    session_id = await read_session_id(first)
    await expect(first, "> ")
    first.sendline('call run_shell_command {"command": "touch ran.txt"}')
    await expect(first, "[approval] run_shell_command")
    # The first command's Worker dies while its shell call waits for approval.
    os.kill(first.pid, signal.SIGKILL)
    # The same file and workspace, edited to switch shell commands off, give the same Flow type.
    cli_home.write_config(
        config_yaml({**NO_CONNECTORS, "local_tools": {"shell": {"enabled": False}}})
    )
    second = cli_home.spawn()
    await read_session_id(second)
    await expect(second, "> ")
    flow_type = session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        snapshot = await sessions.get_agent_snapshot(session_id)
        (interrupt,) = snapshot.pending_interrupts
        assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)

        def turn_ended(candidate: AgentSnapshot) -> AgentSnapshot | None:
            state = candidate.state
            is_done = state is not None and state.turn_count >= 1 and not state.active_turn_id
            return candidate if is_done else None

        snapshot = await wait_for_agent_snapshot(
            sessions, session_id, turn_ended, description="the shell turn"
        )
    outputs = [
        message.tool_output or ""
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]
    assert len(outputs) == 1
    assert "tool run_shell_command is switched off for this session" in outputs[0]
    assert not (workspace / "ran.txt").exists()
    second.sendeof()
    assert await wait_for_exit(second) == 0
