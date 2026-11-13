# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Sessions taken over from another execution environment: another configuration and workspace."""

from __future__ import annotations

import asyncio
import contextlib
import io
import json
import os
import signal
import socket
from collections.abc import AsyncIterator
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import pexpect
from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents import AGENT_STATE, SESSION_TERMINAL_LEASE, AgentState, SessionTerminalLease
from dex_agents_cli_core import session_flow_type
from dex_agents_cli_core.session_environments import (
    CANCELLED_TURN_LINE,
    build_other_harness_error,
    build_refused_model_call_error,
    build_refused_tool_call_error,
    build_session_elsewhere_reason,
    build_takeover_line,
    build_turn_elsewhere_reason,
    is_harness_session_flow_type,
)
from dex_agents_cli_core.session_lease import (
    HOLDER_ID_ENVIRONMENT_SEPARATOR,
    SessionLeaseHolder,
    is_session_call_allowed_here,
    lease_holder_environment,
)
from tests.conftest import CliHome, config_yaml, expect, session_client, wait_for_exit
from tests.fake_terminal_ui import FakeTerminalUi, chat_transcript
from tests.scripted_harness import SCRIPTED_FLOW_TYPE, build_session_flow_without_tools
from tests.test_terminal_ui_handoff import start_chat

AGENTS_DIR = Path(__file__).resolve().parents[4]
CONFORMANCE = json.loads(
    (AGENTS_DIR / "conformance" / "cli_session_environments.json").read_text(encoding="utf-8")
)
NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}
# Short enough that an expiry test takes seconds; the command renews every third of it.
TEST_TTL_SECONDS = 3
PROMPT = "\r\n> "


def _render_line(template: str, values: dict[str, str]) -> str:
    match template:
        case "takeover_line":
            return build_takeover_line(
                values["session_id"],
                values["previous_environment"],
                values["this_environment"],
                values["workspace"],
            )
        case "cancelled_turn_line":
            return CANCELLED_TURN_LINE
        case "refused_tool_call":
            return build_refused_tool_call_error(values["tool_name"], values["reason"])
        case "refused_model_call":
            return build_refused_model_call_error(values["reason"])
        case "session_elsewhere_reason":
            return build_session_elsewhere_reason(values["session_id"])
        case "turn_elsewhere_reason":
            return build_turn_elsewhere_reason(values["session_id"])
        case "other_harness_error":
            return build_other_harness_error(
                values["session_id"], values["flow_type"], values["command_name"]
            )
    raise AssertionError(f"no case renders {template}")


def test_the_rules_match_the_conformance_file() -> None:
    holder_id = CONFORMANCE["holder_id"]
    assert holder_id["separator"] == HOLDER_ID_ENVIRONMENT_SEPARATOR
    holder = SessionLeaseHolder.for_this_process(
        15, environment_flow_type="DexAgentsBuiltinCliSession-ab12cd"
    )
    environment, random = holder.holder_id.split(holder_id["separator"])
    assert environment == "DexAgentsBuiltinCliSession-ab12cd"
    assert len(random) == holder_id["random_hex_length"]
    assert all(character in "0123456789abcdef" for character in random)
    for case in holder_id["environment_cases"]:
        assert lease_holder_environment(case["holder_id"]) == case["environment"], case["name"]
    for case in CONFORMANCE["decision_cases"]:
        stored = (
            None
            if case["stored_holder_id"] is None
            else SessionTerminalLease(
                holder_id=case["stored_holder_id"],
                host_name="host-a",
                process_id=4242,
                opened_at="2026-11-13T20:31:00Z",
                expires_at="2026-11-13T20:31:15Z",
            )
        )
        is_allowed = is_session_call_allowed_here(
            session_flow_type=case["session_flow_type"],
            home_flow_type=case["home_flow_type"],
            holder_id=case["holder_id"],
            stored=stored,
        )
        assert is_allowed == case["acts"], case["name"]
    for case in CONFORMANCE["harness_flow_type_cases"]:
        is_session = is_harness_session_flow_type(case["harness_flow_type"], case["flow_type"])
        assert is_session == case["is_harness_session"], case["name"]
    for case in CONFORMANCE["line_cases"]:
        assert _render_line(case["template"], case["values"]) == case["line"], case["name"]


@dataclass(frozen=True)
class _Environment:
    """One execution environment: its own configuration file and workspace in one home."""

    config_path: Path
    workspace: Path

    @property
    def flow_type(self) -> str:
        return session_flow_type(SCRIPTED_FLOW_TYPE, self.config_path, self.workspace)

    @property
    def workspace_shown(self) -> str:
        return str(self.workspace.resolve())


def _new_environment(tmp_path: Path, name: str) -> _Environment:
    workspace = tmp_path / f"{name}-workspace"
    workspace.mkdir()
    config_path = tmp_path / f"{name}.yaml"
    config_path.write_text(config_yaml({**NO_CONNECTORS, "workspace": str(workspace)}))
    return _Environment(config_path, workspace)


def _spawn(cli_home: CliHome, environment: _Environment, *arguments: str) -> pexpect.spawn:
    return cli_home.spawn("--plain", "--config", str(environment.config_path), *arguments)


async def _read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


async def _write_file_through_chat(child: pexpect.spawn, path: str) -> None:
    child.sendline(f'call write_file {{"path": "{path}", "content": "x"}}')
    await expect(child, "Approve? [y/N, or n <reason>]")
    child.sendline("y")
    await expect(child, f"done: write_file=wrote 1 bytes to {path}")
    await expect(child, PROMPT)


async def _start_and_leave_session(cli_home: CliHome, environment: _Environment) -> str:
    child = _spawn(cli_home, environment)
    session_id = await _read_session_id(child)
    await expect(child, PROMPT)
    child.sendeof()
    assert await wait_for_exit(child) == 0
    return session_id


@contextlib.asynccontextmanager
async def _dex_reader(cli_home: CliHome, cache_dir: Path) -> AsyncIterator[AsyncClient]:
    """Open a Client that reads the session Attributes and describes Flows of any type."""
    cache_dir.mkdir(parents=True, exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([build_session_flow_without_tools()], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=cli_home.address),
    )
    try:
        yield client
    finally:
        await client.close()
        blob_cache.close()


async def _read_lease(
    cli_home: CliHome, tmp_path: Path, session_id: str
) -> SessionTerminalLease | None:
    async with _dex_reader(cli_home, tmp_path / "reader-cache") as reader:
        values = await reader.get_attributes(session_id, SESSION_TERMINAL_LEASE)
    lease: SessionTerminalLease | None = values.get(SESSION_TERMINAL_LEASE)
    return lease


async def _read_state(cli_home: CliHome, tmp_path: Path, session_id: str) -> AgentState | None:
    async with _dex_reader(cli_home, tmp_path / "reader-cache") as reader:
        values = await reader.get_attributes(session_id, AGENT_STATE)
    state: AgentState | None = values.get(AGENT_STATE)
    return state


async def _wait_for_failed_turn(
    cli_home: CliHome, tmp_path: Path, session_id: str, error: str
) -> None:
    """Wait until the session's last turn failed with ``error``; the read needs no Worker."""
    loop = asyncio.get_running_loop()
    deadline = loop.time() + 60
    while loop.time() < deadline:
        state = await _read_state(cli_home, tmp_path, session_id)
        if (
            state is not None
            and state.last_turn_error is not None
            and error in state.last_turn_error
        ):
            return
        await asyncio.sleep(0.2)
    raise AssertionError(f"no turn of {session_id} failed with: {error}")


async def test_a_session_continues_in_another_environment_and_runs_new_tool_calls_there(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment_a = _new_environment(tmp_path, "a")
    environment_b = _new_environment(tmp_path, "b")
    first = _spawn(cli_home, environment_a)
    session_id = await _read_session_id(first)
    await expect(first, PROMPT)
    await _write_file_through_chat(first, "from-a.txt")
    first.sendeof()
    assert await wait_for_exit(first) == 0

    second = _spawn(cli_home, environment_b, "--resume", session_id)
    await expect(
        second,
        build_takeover_line(
            session_id,
            environment_a.flow_type,
            environment_b.flow_type,
            environment_b.workspace_shown,
        ),
    )
    assert await _read_session_id(second) == session_id
    await expect(second, PROMPT)
    await _write_file_through_chat(second, "from-b.txt")
    assert (environment_b.workspace / "from-b.txt").exists()
    assert not (environment_a.workspace / "from-b.txt").exists()
    assert not (environment_b.workspace / "from-a.txt").exists()
    held = await _read_lease(cli_home, tmp_path, session_id)
    assert held is not None and held.holder_id.startswith(f"{environment_b.flow_type}/")
    async with _dex_reader(cli_home, tmp_path / "describe-cache") as reader:
        info = await reader.describe_flow(session_id)
    assert info.flow_type == environment_a.flow_type
    second.sendeof()
    assert await wait_for_exit(second) == 0
    left = await _read_lease(cli_home, tmp_path, session_id)
    assert left is not None and left.holder_id.startswith(
        f"{environment_b.flow_type}/"
    ), "handed back by expiry: the stored holder still names environment B"
    listed = await asyncio.to_thread(
        cli_home.run, "--config", str(environment_b.config_path), "sessions"
    )
    assert listed.returncode == 0, listed.stderr
    assert session_id not in listed.stdout

    third = _spawn(cli_home, environment_a, "--resume", session_id)
    await expect(
        third,
        build_takeover_line(
            session_id,
            environment_b.flow_type,
            environment_a.flow_type,
            environment_a.workspace_shown,
        ),
    )
    await expect(third, PROMPT)
    await _write_file_through_chat(third, "from-a-again.txt")
    assert (environment_a.workspace / "from-a-again.txt").exists()
    third.sendeof()
    assert await wait_for_exit(third) == 0
    assert await _read_lease(cli_home, tmp_path, session_id) is None


async def test_a_paused_holder_loses_the_session_to_another_environment_and_stops(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.extra_environment["DEX_AGENTS_SESSION_LEASE_TTL_SECONDS"] = str(TEST_TTL_SECONDS)
    environment_a = _new_environment(tmp_path, "a")
    environment_b = _new_environment(tmp_path, "b")
    first = _spawn(cli_home, environment_a)
    session_id = await _read_session_id(first)
    await expect(first, PROMPT)
    os.kill(first.pid, signal.SIGSTOP)
    second = _spawn(cli_home, environment_b, "--resume", session_id)
    try:
        assert await _read_session_id(second) == session_id
        await expect(second, PROMPT)
    finally:
        os.kill(first.pid, signal.SIGCONT)
    await expect(
        first,
        f"Session {session_id} is now open in another terminal "
        f"({socket.gethostname()}, pid {second.pid}, since ",
        timeout=TEST_TTL_SECONDS + 10,
    )
    assert await wait_for_exit(first) == 1
    await _write_file_through_chat(second, "after-takeover.txt")
    assert (environment_b.workspace / "after-takeover.txt").exists()
    assert not (environment_a.workspace / "after-takeover.txt").exists()
    second.sendeof()
    assert await wait_for_exit(second) == 0


async def test_a_takeover_cancels_the_turn_in_progress_so_nothing_it_asked_for_runs(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.extra_environment["DEX_AGENTS_SESSION_LEASE_TTL_SECONDS"] = str(TEST_TTL_SECONDS)
    environment_a = _new_environment(tmp_path, "a")
    environment_b = _new_environment(tmp_path, "b")
    first = _spawn(cli_home, environment_a)
    session_id = await _read_session_id(first)
    await expect(first, PROMPT)
    first.sendline('call write_file {"path": "pending.txt", "content": "x"}')
    await expect(first, "Approve? [y/N, or n <reason>]")
    os.kill(first.pid, signal.SIGKILL)

    second = _spawn(cli_home, environment_b, "--resume", session_id)
    await expect(second, CANCELLED_TURN_LINE, timeout=TEST_TTL_SECONDS + 30)
    await expect(second, PROMPT)
    state = await _read_state(cli_home, tmp_path, session_id)
    assert state is not None and state.active_turn_id is None
    assert not (environment_a.workspace / "pending.txt").exists()
    assert not (environment_b.workspace / "pending.txt").exists()
    await _write_file_through_chat(second, "next-turn.txt")
    assert (environment_b.workspace / "next-turn.txt").exists()
    second.sendeof()
    assert await wait_for_exit(second) == 0


async def test_a_command_of_the_sessions_own_environment_refuses_its_calls_held_elsewhere(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment_a = _new_environment(tmp_path, "a")
    environment_b = _new_environment(tmp_path, "b")
    session_id = await _start_and_leave_session(cli_home, environment_a)
    second = _spawn(cli_home, environment_b, "--resume", session_id)
    await expect(second, PROMPT)
    second.sendeof()
    assert await wait_for_exit(second) == 0

    # The only Worker of the session's Flow type now is this new command of environment A.
    third = _spawn(cli_home, environment_a)
    await expect(third, PROMPT)
    async with session_client(
        cli_home.address, tmp_path / "client-cache", environment_a.flow_type
    ) as sessions:
        await sessions.send_user_message(
            session_id, 'call write_file {"path": "refused.txt", "content": "x"}'
        )
    await _wait_for_failed_turn(
        cli_home,
        tmp_path,
        session_id,
        build_refused_model_call_error(build_session_elsewhere_reason(session_id)),
    )
    assert not (environment_a.workspace / "refused.txt").exists()
    assert not (environment_b.workspace / "refused.txt").exists()
    third.sendeof()
    assert await wait_for_exit(third) == 0


async def test_a_command_that_took_one_session_over_refuses_that_environments_others(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment_a = _new_environment(tmp_path, "a")
    environment_b = _new_environment(tmp_path, "b")
    taken_session_id = await _start_and_leave_session(cli_home, environment_a)
    other_session_id = await _start_and_leave_session(cli_home, environment_a)
    # The only Worker of environment A's Flow type now is this command of environment B.
    second = _spawn(cli_home, environment_b, "--resume", taken_session_id)
    await expect(second, PROMPT)
    async with session_client(
        cli_home.address, tmp_path / "client-cache", environment_a.flow_type
    ) as sessions:
        await sessions.send_user_message(
            other_session_id, 'call write_file {"path": "other.txt", "content": "x"}'
        )
    await _wait_for_failed_turn(
        cli_home,
        tmp_path,
        other_session_id,
        build_refused_model_call_error(build_session_elsewhere_reason(other_session_id)),
    )
    assert not (environment_b.workspace / "other.txt").exists()
    second.sendeof()
    assert await wait_for_exit(second) == 0


async def test_resume_of_another_harness_session_exits_with_2(
    cli_home: CliHome, tmp_path: Path
) -> None:
    other_flow_type = "DexAgentsCliOtherHarnessSession-ab12cd"
    session_id = "other-harness-session"
    async with session_client(
        cli_home.address, tmp_path / "other-cache", other_flow_type
    ) as sessions:
        await sessions.start_session(session_id)
    environment = _new_environment(tmp_path, "b")
    completed = await asyncio.to_thread(
        cli_home.run, "--config", str(environment.config_path), "--resume", session_id
    )
    assert completed.returncode == 2
    assert (
        build_other_harness_error(session_id, other_flow_type, "dex-agents-scripted")
        in completed.stderr
    )


async def test_the_terminal_ui_of_another_environments_session_offers_no_new_session(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment_a = _new_environment(tmp_path, "a")
    environment_b = _new_environment(tmp_path, "b")
    session_id = await _start_and_leave_session(cli_home, environment_a)
    resumed = chat_transcript([])
    launch = resumed["steps"][0]["message"]
    launch["session"]["is_resumed"] = True
    launch["methods"] = [method for method in launch["methods"] if method != "start_session"]
    fake = FakeTerminalUi(tmp_path / "tui", resumed)
    screen = io.StringIO()
    child = start_chat(
        cli_home,
        fake,
        screen,
        NO_CONNECTORS,
        "--config",
        str(environment_b.config_path),
        "--resume",
        session_id,
    )
    await expect(
        child,
        f"Session {session_id} was last open in execution environment {environment_a.flow_type}",
    )
    assert await wait_for_exit(child) == 0, screen.getvalue()
    assert fake.mismatches() == []
    lease = await _read_lease(cli_home, tmp_path, session_id)
    assert lease is not None and lease.holder_id.startswith(f"{environment_b.flow_type}/")
