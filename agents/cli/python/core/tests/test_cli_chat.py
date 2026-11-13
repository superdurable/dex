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
import base64
import json
import os
import re
import signal
import socket
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import pexpect
import pytest
from dex import AsyncClient, BlobCacheConfig, ClientOptions, FlowStatus, Registry, open_blob_cache

from dex_agents import (
    AgentSessionSummary,
    AgentSnapshot,
    MessageRole,
    list_agent_sessions,
    render_session_flow_graph,
    sort_agent_sessions_by_last_activity,
)
from dex_agents.session_graph import SESSION_FLOW_GRAPH_GENERATED_AT
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    read_step_history,
    resolve_dexcli_path,
    wait_for_agent_snapshot,
    wait_until,
)
from dex_agents_cli_core import SESSION_ACTION_PERMISSION, session_flow_type
from tests.conftest import CliHome, config_yaml, expect, session_client, wait_for_exit
from tests.reasoning_effort_cli import (
    REASONING_EFFORTS_ENVIRONMENT_VARIABLE,
    SUGGESTED_MODELS_ENVIRONMENT_VARIABLE,
)
from tests.scripted_harness import FORCED_SUMMARY_ENVIRONMENT_VARIABLE, SCRIPTED_FLOW_TYPE

NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}


async def read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


def assert_session_id_names_the_harness_and_utc_start(
    session_id: str, spawned_at: datetime
) -> None:
    # Same shape as the TypeScript CLI core: <harness>-<UTC yyyymmdd-HHMMSS>-<6 hex>.
    match = re.fullmatch(r"scripted-(\d{8}-\d{6})-[0-9a-f]{6}", session_id)
    assert match is not None, session_id
    started_at = datetime.strptime(match.group(1), "%Y%m%d-%H%M%S").replace(tzinfo=UTC)
    assert spawned_at <= started_at <= datetime.now(UTC), (session_id, spawned_at)


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
    spawned_at = datetime.now(UTC).replace(microsecond=0)
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    assert_session_id_names_the_harness_and_utc_start(session_id, spawned_at)
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


async def read_flow_definitions(cli_home: CliHome, flow_type: str) -> list[dict[str, Any]]:
    process = await asyncio.create_subprocess_exec(
        str(resolve_dexcli_path()),
        "api",
        "call",
        "GetFlowDefinitions",
        "--server",
        cli_home.address,
        "--data",
        json.dumps({"flowTypes": [flow_type]}),
        stdout=asyncio.subprocess.PIPE,
    )
    stdout, _ = await process.communicate()
    assert process.returncode == 0
    definitions: list[dict[str, Any]] = json.loads(stdout).get("definitions", [])
    return definitions


async def test_the_worker_uploads_the_session_graph_for_dex_web(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    workspace = cli_home.home / "dex-agent-workspace"
    child = cli_home.spawn()
    await read_session_id(child)
    await expect(child, "> ")
    flow_type = session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    definitions = await read_flow_definitions(cli_home, flow_type)
    for _ in range(50):
        if definitions:
            break
        await asyncio.sleep(0.1)
        definitions = await read_flow_definitions(cli_home, flow_type)
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert len(definitions) == 1, definitions
    (definition,) = definitions
    expected = render_session_flow_graph(flow_type, SESSION_ACTION_PERMISSION)
    assert base64.b64decode(definition["content"]).decode() == expected
    assert definition["info"]["flowType"] == flow_type
    generated_at = datetime.fromisoformat(definition["info"]["generatedAt"])
    assert generated_at == SESSION_FLOW_GRAPH_GENERATED_AT
    log_text = (cli_home.home / ".dex" / "agents" / "scripted.log").read_text(encoding="utf-8")
    assert re.search(
        f"session Flow definition (created|unchanged): flowType={flow_type} ", log_text
    )


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


async def test_resume_with_another_workspace_takes_the_session_over(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    first = cli_home.spawn()
    session_id = await read_session_id(first)
    await expect(first, "> ")
    first.sendeof()
    assert await wait_for_exit(first) == 0
    # Another workspace is another environment; see test_session_environments.
    cli_home.write_config(config_yaml({**NO_CONNECTORS, "workspace": str(tmp_path / "other")}))
    completed = await asyncio.to_thread(cli_home.run, "--resume", session_id)
    assert completed.returncode == 0, completed.stderr
    assert f"Session {session_id} was last open in execution environment " in completed.stderr


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
    child.sendline("hello   the  list")
    await expect(child, "echo: hello")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    completed = cli_home.run("sessions")
    assert completed.returncode == 0, completed.stderr
    minute = r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}"
    assert re.search(
        rf"^{re.escape(session_id)}  running  started {minute}  updated {minute}  1 turn  "
        r"hello the list$",
        completed.stdout,
        re.MULTILINE,
    ), completed.stdout


@pytest.mark.parametrize(
    ("server", "summary", "is_indexed"),
    [
        ("auto", None, True),
        ("url", None, False),
        ("url", "on", True),
        ("auto", "off", False),
    ],
)
async def test_sessions_summary_follows_the_config_and_the_server(
    cli_home: CliHome, server: str, summary: str | None, is_indexed: bool
) -> None:
    values: dict[str, Any] = dict(NO_CONNECTORS)
    if server == "url":
        # A Dex server URL may be shared, so auto leaves its search index alone.
        values["dex"] = {"server": f"http://{cli_home.address}"}
    if summary is not None:
        values["sessions"] = {"summary": summary}
    cli_home.write_config(config_yaml(values))
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline("summary check")
    await expect(child, "echo: summary check")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0

    completed = cli_home.run("sessions")
    assert completed.returncode == 0, completed.stderr
    (line,) = [line for line in completed.stdout.splitlines() if line.startswith(session_id)]
    if is_indexed:
        assert line.endswith("  1 turn  summary check"), line
    else:
        assert re.fullmatch(rf"{re.escape(session_id)}  running  started [0-9: -]+", line), line


@pytest.mark.parametrize(
    ("forced", "server", "summary"),
    [
        ("off", "auto", None),
        ("on", "auto", "off"),
        # The options report off, but the schema declares the summary.
        ("misreported-on", "url", None),
        ("misreported-on", "auto", "off"),
        # The options report on, but the schema declares none of it.
        ("misreported-off", "url", "on"),
    ],
)
async def test_a_harness_that_ignores_sessions_summary_stops_the_command(
    cli_home: CliHome, forced: str, server: str, summary: str | None
) -> None:
    values: dict[str, Any] = dict(NO_CONNECTORS)
    if server == "url":
        values["dex"] = {"server": f"http://{cli_home.address}"}
    if summary is not None:
        values["sessions"] = {"summary": summary}
    cli_home.write_config(config_yaml(values))
    cli_home.extra_environment[FORCED_SUMMARY_ENVIRONMENT_VARIABLE] = forced

    completed = await asyncio.to_thread(cli_home.run, stdin="hello\n")

    assert completed.returncode != 0
    assert "sessions.summary says the session Flow must" in completed.stderr
    assert "HarnessContext.is_session_summary_indexed" in completed.stderr
    assert "Session " not in completed.stdout


async def test_sessions_sorts_several_pages_and_says_when_more_exist(
    cli_home: CliHome, tmp_path: Path
) -> None:
    workspace = cli_home.home / "dex-agent-workspace"
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn()
    oldest = await read_session_id(child)
    await expect(child, "> ")
    flow_type = session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        for index in range(60):
            await sessions.start_session(f"later-{index:03d}")
        await _wait_for_the_next_second()
        # The oldest session, last in Dex's order, becomes the most recently active one.
        child.sendline("after the others")
        await expect(child, "echo: after the others")
        await expect(child, "> ")
        child.sendeof()
        assert await wait_for_exit(child) == 0

        await _wait_for_visible_running_sessions(cli_home, tmp_path, flow_type, 61, oldest)
        listed = cli_home.run("sessions")
        assert listed.returncode == 0, listed.stderr
        lines = [line for line in listed.stdout.splitlines() if "  started " in line]
        assert len(lines) == 61
        assert lines[0].startswith(f"{oldest}  running  "), listed.stdout
        assert all(line.endswith("  0 turns") for line in lines[1:]), listed.stdout
        assert "More sessions exist" not in listed.stdout
        assert listed.stdout.splitlines()[-1].startswith("Resume one with: "), listed.stdout

        for index in range(200):
            await sessions.start_session(f"extra-{index:03d}")
    await _wait_for_visible_running_sessions(cli_home, tmp_path, flow_type, 261)
    capped = cli_home.run("sessions")
    assert capped.returncode == 0, capped.stderr
    assert len([line for line in capped.stdout.splitlines() if "  started " in line]) == 250
    assert "More sessions exist; this list sorts the first 250 that Dex returns" in capped.stdout
    assert capped.stdout.splitlines()[-1].startswith("Resume one with: "), capped.stdout


async def _wait_for_visible_running_sessions(
    cli_home: CliHome,
    tmp_path: Path,
    flow_type: str,
    count: int,
    most_recent_session_id: str | None = None,
) -> None:
    """Wait until search shows every running session, since Flow search is eventually consistent."""
    cache_dir = tmp_path / "search-cache"
    cache_dir.mkdir(parents=True, exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=cli_home.address),
    )

    async def are_all_visible() -> bool:
        running: list[AgentSessionSummary] = []
        next_page_token = ""
        while True:
            page = await list_agent_sessions(client, flow_type, next_page_token=next_page_token)
            running.extend(s for s in page.sessions if s.status == FlowStatus.RUNNING)
            next_page_token = page.next_page_token
            if not next_page_token:
                break
        if len(running) != count:
            return False
        if most_recent_session_id is None:
            return True
        ordered = sort_agent_sessions_by_last_activity(running)
        return ordered[0].session_id == most_recent_session_id

    try:
        await wait_until(
            f"search to show {count} running sessions",
            are_all_visible,
            timeout=timedelta(seconds=30),
        )
    finally:
        await client.close()
        blob_cache.close()


async def _wait_for_the_next_second() -> None:
    """Wait for a later second, since session activity times have one-second precision."""
    started = datetime.now(UTC).replace(microsecond=0)
    while datetime.now(UTC) < started + timedelta(seconds=1):
        await asyncio.sleep(0.05)


async def test_a_failed_turn_makes_the_command_exit_1(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    completed = await asyncio.to_thread(cli_home.run, stdin="fail\n")
    assert "[turn failed]" in completed.stdout
    assert "the scripted model was told to fail" in completed.stdout
    assert completed.returncode == 1


async def test_a_declared_reasoning_effort_reaches_the_next_turn_and_another_fails_it(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn(command_module="tests.reasoning_effort_cli")
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline("effort")
    await expect(child, "effort: default")
    workspace = cli_home.home / "dex-agent-workspace"
    flow_type = session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        assert await sessions.update_session_settings(session_id, {"reasoning_effort": "high"})
        child.sendline("effort")
        await expect(child, "effort: high")
        assert await sessions.update_session_settings(session_id, {"reasoning_effort": "max"})
        child.sendline("effort")
        await expect(child, "[turn failed]", timeout=120)
        await expect(child, 'unsupported_reasoning_effort at "/reasoning_effort"')
        await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 1


async def test_a_harness_without_reasoning_efforts_fails_a_turn_that_sets_one(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    workspace = cli_home.home / "dex-agent-workspace"
    flow_type = session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        assert await sessions.update_session_settings(session_id, {"reasoning_effort": "low"})
        child.sendline("effort")
        await expect(child, "[turn failed]", timeout=120)
        await expect(child, 'unsupported_reasoning_effort at "/reasoning_effort"')
        await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 1


@pytest.mark.parametrize(
    ("variable", "declared", "message"),
    [
        (
            REASONING_EFFORTS_ENVIRONMENT_VARIABLE,
            "low,High",
            "supported_reasoning_efforts value 'High' is not a lowercase name",
        ),
        (
            REASONING_EFFORTS_ENVIRONMENT_VARIABLE,
            "low,default",
            "supported_reasoning_efforts value 'default' is reserved",
        ),
        (
            SUGGESTED_MODELS_ENVIRONMENT_VARIABLE,
            "scripted model",
            "suggested_models value 'scripted model' is not a non-empty model ID",
        ),
        (
            SUGGESTED_MODELS_ENVIRONMENT_VARIABLE,
            "scripted\tmodel",
            "suggested_models value 'scripted\\tmodel' is not a non-empty model ID",
        ),
    ],
    ids=["uppercase effort", "reserved default", "model with a space", "model with a tab"],
)
def test_a_bad_harness_declaration_stops_the_command_before_it_starts(
    cli_home: CliHome, variable: str, declared: str, message: str
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    cli_home.extra_environment[variable] = declared
    completed = cli_home.run(command_module="tests.reasoning_effort_cli", stdin="effort\n")
    assert completed.returncode == 1
    assert message in completed.stderr
    assert "effort:" not in completed.stdout


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
