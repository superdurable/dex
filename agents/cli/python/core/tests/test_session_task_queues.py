# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Session task queues: sessions move between execution environments and wait for a command."""

from __future__ import annotations

import asyncio
import contextlib
import io
import json
import os
import signal
import socket
from collections.abc import AsyncIterator, Mapping
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
from typing import Any

import pexpect
import pytest
from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents import (
    AGENT_STATE,
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    SESSION_TERMINAL_LEASE,
    AgentSessionClient,
    AgentState,
)
from dex_agents.testing import wait_until
from dex_agents_cli_core import session_flow_type, session_task_queue
from dex_agents_cli_core.session_lease import build_took_over_line
from dex_agents_cli_core.session_task_queues import (
    CANCELLED_TURN_LINE,
    build_takeover_line,
    cli_app_worker_id,
    describe_session_environment,
    is_legacy_session_flow_type,
    is_other_session_place,
    named_task_queue_server_error,
    other_environment_legacy_session_error,
    other_harness_session_error,
    refused_model_call_error,
    refused_tool_call_error,
    taken_over_turn_reason,
)
from tests.conftest import CliHome, config_yaml, expect, session_client, wait_for_exit
from tests.fake_terminal_ui import FakeTerminalUi, chat_transcript
from tests.scripted_harness import SCRIPTED_FLOW_TYPE, build_session_flow_without_tools
from tests.test_terminal_ui_handoff import pause

AGENTS_DIR = Path(__file__).resolve().parents[4]
FIXTURE: dict[str, Any] = json.loads(
    (AGENTS_DIR / "conformance" / "cli_session_task_queues.json").read_text(encoding="utf-8")
)
NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}
# The chat's prompt line; a bare "> " also matches the help line's "<path> attaches".
PROMPT = "\r\n> "
# A short lease, so a killed holder's lease expires within the test.
SHORT_TTL_SECONDS = "3"

_RENDERERS = {
    "takeover_line": lambda values: build_takeover_line(
        values["session_id"], values["previous_environment"], values["environment"]
    ),
    "taken_over_turn_reason": lambda values: taken_over_turn_reason(values["session_id"]),
    "refused_tool_call_error": lambda values: refused_tool_call_error(
        values["tool_name"], values["reason"]
    ),
    "refused_model_call_error": lambda values: refused_model_call_error(values["reason"]),
    "other_environment_legacy_session_error": lambda values: (
        other_environment_legacy_session_error(values["session_id"], values["flow_type"])
    ),
    "other_harness_session_error": lambda values: other_harness_session_error(
        values["session_id"], values["flow_type"], values["command_name"]
    ),
    "named_task_queue_server_error": lambda values: named_task_queue_server_error(
        values["command_name"]
    ),
}


@pytest.mark.parametrize("case", FIXTURE["task_queue_cases"], ids=lambda case: case["name"])
def test_the_session_task_queue_matches_the_shared_cases(case: dict[str, Any]) -> None:
    assert session_task_queue(case["session_id"]) == case["task_queue"]


@pytest.mark.parametrize("case", FIXTURE["legacy_flow_type_cases"], ids=lambda case: case["name"])
def test_legacy_session_flow_types_match_the_shared_cases(case: dict[str, Any]) -> None:
    harness_flow_type = FIXTURE["harness_flow_type"]
    assert is_legacy_session_flow_type(harness_flow_type, case["flow_type"]) is case["is_legacy"]


@pytest.mark.parametrize("case", FIXTURE["environment_cases"], ids=lambda case: case["name"])
def test_the_environment_text_matches_the_shared_cases(case: dict[str, Any]) -> None:
    assert describe_session_environment(case["environment"]) == case["text"]


@pytest.mark.parametrize("case", FIXTURE["place_cases"], ids=lambda case: case["name"])
def test_other_places_match_the_shared_cases(case: dict[str, Any]) -> None:
    assert is_other_session_place(case["previous"], case["current"]) is case["is_other_place"]


def test_every_template_case_renders_its_line() -> None:
    assert FIXTURE["templates"]["cancelled_turn_line"] == CANCELLED_TURN_LINE
    for case in FIXTURE["template_cases"]:
        assert _RENDERERS[case["template"]](case["values"]) == case["line"], case["template"]


@pytest.mark.parametrize("case", FIXTURE["app_worker_id_cases"], ids=lambda case: case["name"])
def test_the_app_worker_id_matches_the_shared_cases(case: dict[str, Any]) -> None:
    app_worker_id = cli_app_worker_id(
        case["harness_name"], case["host_name"], case["process_id"], case["suffix"]
    )
    assert app_worker_id == case["app_worker_id"]
    assert len(app_worker_id) <= 63


@dataclass(frozen=True)
class _Environment:
    """A configuration file and workspace of its own: another execution environment on this host."""

    config_path: Path
    workspace: Path

    @property
    def place(self) -> str:
        return f"{socket.gethostname()}:{os.path.realpath(self.workspace)}"

    @property
    def legacy_flow_type(self) -> str:
        return session_flow_type(SCRIPTED_FLOW_TYPE, self.config_path, self.workspace)

    def arguments(self, *more: str) -> tuple[str, ...]:
        return ("--config", str(self.config_path), *more)


def _new_environment(tmp_path: Path, name: str) -> _Environment:
    workspace = tmp_path / f"{name}-workspace"
    workspace.mkdir()
    config_path = tmp_path / f"{name}.yaml"
    local_tools = {"shell": {"enabled": True, "allow": ["pwd"]}}
    config_path.write_text(
        config_yaml({**NO_CONNECTORS, "workspace": str(workspace), "local_tools": local_tools})
    )
    return _Environment(config_path, workspace)


@dataclass
class _SessionReader:
    """A test-side Client of the harness's Flow type; its Attribute reads need no Worker."""

    client: AsyncClient
    sessions: AgentSessionClient

    async def read(self, session_id: str, definition: Any) -> Any:
        stored = await self.client.get_attributes(session_id, definition)
        return stored.get(definition)

    async def wait_for_state(self, session_id: str, is_wanted: Any, description: str) -> AgentState:
        async def probe() -> AgentState | None:
            state: AgentState | None = await self.read(session_id, AGENT_STATE)
            return state if state is not None and is_wanted(state) else None

        found: AgentState = await wait_until(description, probe, timeout=timedelta(seconds=60))
        return found


@contextlib.asynccontextmanager
async def _session_reader(cli_home: CliHome, tmp_path: Path) -> AsyncIterator[_SessionReader]:
    flow = build_session_flow_without_tools(SCRIPTED_FLOW_TYPE)
    cache_dir = tmp_path / "reader-cache"
    cache_dir.mkdir(exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([flow], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=cli_home.address),
    )
    try:
        yield _SessionReader(client, AgentSessionClient(client, flow))
    finally:
        await client.close()
        blob_cache.close()


async def _read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


async def _open_chat(cli_home: CliHome, *arguments: str) -> tuple[pexpect.spawn, str]:
    child = cli_home.spawn(*arguments)
    session_id = await _read_session_id(child)
    await expect(child, PROMPT)
    return child, session_id


async def _exit_chat(child: pexpect.spawn) -> None:
    child.sendline("/exit")
    assert await wait_for_exit(child) == 0


async def _run_pwd(child: pexpect.spawn, environment: _Environment) -> None:
    child.sendline('call run_shell_command {"command": "pwd"}')
    await expect(child, "done: run_shell_command=exit code 0")
    await expect(child, os.path.realpath(environment.workspace))
    await expect(child, PROMPT)


async def _start_legacy_session(
    cli_home: CliHome, tmp_path: Path, environment: _Environment
) -> str:
    """Start a session as a release before 0.6.7 did: hashed Flow type, default task queue."""
    session_id = f"scripted-legacy-{os.urandom(4).hex()}"
    async with session_client(
        cli_home.address, tmp_path / "legacy-cache", environment.legacy_flow_type
    ) as sessions:
        await sessions.start_session(session_id)
    return session_id


async def test_a_session_started_in_one_environment_continues_in_another_whose_tools_run_there(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    second_environment = _new_environment(tmp_path, "b")
    first, session_id = await _open_chat(cli_home, *first_environment.arguments())
    await _run_pwd(first, first_environment)
    async with _session_reader(cli_home, tmp_path) as reader:
        info = await reader.client.describe_flow(session_id)
        assert info.flow_type == SCRIPTED_FLOW_TYPE, "a new session has the harness's Flow type"
        last = await reader.read(session_id, SESSION_LAST_TERMINAL_ENVIRONMENT)
        assert last["workspace"] == os.path.realpath(first_environment.workspace)
        await _exit_chat(first)

        second = cli_home.spawn(*second_environment.arguments("--resume", session_id))
        await expect(
            second,
            build_takeover_line(session_id, first_environment.place, second_environment.place),
        )
        assert await _read_session_id(second) == session_id
        await expect(second, PROMPT)
        await _run_pwd(second, second_environment)
        last = await reader.read(session_id, SESSION_LAST_TERMINAL_ENVIRONMENT)
        assert last["workspace"] == os.path.realpath(second_environment.workspace)
        await _exit_chat(second)
        assert await reader.read(session_id, SESSION_TERMINAL_LEASE) is None, "exit released it"


async def test_a_second_terminal_from_another_environment_waits_without_pulling_then_takes_over(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    second_environment = _new_environment(tmp_path, "b")
    first, session_id = await _open_chat(cli_home, *first_environment.arguments())
    second = cli_home.spawn(*second_environment.arguments("--resume", session_id))
    await expect(second, f"Session {session_id} is open in another terminal (")

    # The waiting command pulls nothing, so the session's tool Steps keep running in the first.
    await _run_pwd(first, first_environment)
    await _run_pwd(first, first_environment)
    await _exit_chat(first)

    await expect(second, build_took_over_line(session_id))
    await expect(
        second, build_takeover_line(session_id, first_environment.place, second_environment.place)
    )
    assert await _read_session_id(second) == session_id
    await expect(second, PROMPT)
    await _run_pwd(second, second_environment)
    await _exit_chat(second)


async def test_a_takeover_cancels_the_turn_that_was_in_progress_in_the_other_environment(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    second_environment = _new_environment(tmp_path, "b")
    cli_home.extra_environment["DEX_AGENTS_SESSION_LEASE_TTL_SECONDS"] = SHORT_TTL_SECONDS
    first, session_id = await _open_chat(cli_home, *first_environment.arguments())
    async with _session_reader(cli_home, tmp_path) as reader:
        first.sendline("slow")
        await reader.wait_for_state(
            session_id,
            lambda state: bool(state.active_turn_id),
            "the turn to start in the first environment",
        )
        os.kill(first.pid, signal.SIGKILL)
        first.close(force=True)

        second = cli_home.spawn(*second_environment.arguments("--resume", session_id))
        await expect(
            second,
            build_takeover_line(session_id, first_environment.place, second_environment.place),
            timeout=60,
        )
        await expect(second, CANCELLED_TURN_LINE)
        assert await _read_session_id(second) == session_id
        await reader.wait_for_state(
            session_id,
            lambda state: state.turn_count >= 1 and not state.active_turn_id,
            "the taken over turn to end",
        )
        await expect(second, PROMPT)
        second.sendline("after the takeover")
        await expect(second, "echo: after the takeover")
        await _exit_chat(second)


async def test_a_message_sent_while_no_command_has_the_session_open_waits_for_the_command(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    second_environment = _new_environment(tmp_path, "b")
    first, session_id = await _open_chat(cli_home, *first_environment.arguments())
    await _exit_chat(first)
    async with _session_reader(cli_home, tmp_path) as reader:
        # Dex holds the RPC until a Worker pulls the session's task queue, within its dispatch wait.
        second = cli_home.spawn(*second_environment.arguments("--resume", session_id))
        assert await reader.sessions.send_user_message(session_id, "sent from Dex Web")
        assert await _read_session_id(second) == session_id
        state = await reader.wait_for_state(
            session_id,
            lambda state: state.turn_count >= 1 and not state.active_turn_id,
            "the sent message's turn",
        )
        assert state.turn_count == 1
        snapshot = await reader.sessions.get_agent_snapshot(session_id)
        texts = [message.text for message in snapshot.messages]
        assert "echo: sent from Dex Web" in texts, texts
        await _exit_chat(second)


async def test_an_environment_hashed_session_resumes_in_its_own_environment_only(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    second_environment = _new_environment(tmp_path, "b")
    session_id = await _start_legacy_session(cli_home, tmp_path, first_environment)

    first = cli_home.spawn(*first_environment.arguments("--resume", session_id))
    assert await _read_session_id(first) == session_id
    await expect(first, PROMPT)
    first.sendline("still here")
    await expect(first, "echo: still here")
    await _exit_chat(first)

    other = await asyncio.to_thread(
        cli_home.run, *second_environment.arguments("--resume", session_id)
    )
    assert other.returncode == 2, other.stderr
    expected = other_environment_legacy_session_error(
        session_id, first_environment.legacy_flow_type
    )
    assert expected in other.stderr, other.stderr


async def test_sessions_lists_every_environments_sessions_with_where_each_was_last_open(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    second_environment = _new_environment(tmp_path, "b")
    from_first, first_session = await _open_chat(cli_home, *first_environment.arguments())
    await _exit_chat(from_first)
    from_second, second_session = await _open_chat(cli_home, *second_environment.arguments())
    await _exit_chat(from_second)
    legacy_session = await _start_legacy_session(cli_home, tmp_path, first_environment)
    expected_ids = {first_session, second_session, legacy_session}
    query = f'FlowType IN ("{SCRIPTED_FLOW_TYPE}", "{first_environment.legacy_flow_type}")'
    async with _session_reader(cli_home, tmp_path) as reader:

        async def are_all_visible() -> bool:
            page = await reader.client.search_flows(query, 250)
            return expected_ids <= {flow.flow_id for flow in page.flows}

        await wait_until("the sessions to show in search", are_all_visible)

    listed_in_first = await asyncio.to_thread(
        cli_home.run, *first_environment.arguments("sessions")
    )
    assert listed_in_first.returncode == 0, listed_in_first.stderr
    rows = _rows_by_id(listed_in_first.stdout)
    assert f"  on {first_environment.place}" in rows[first_session]
    assert f"  on {second_environment.place}" in rows[second_session]
    # An environment-hashed session runs only where it started.
    assert f"  on {first_environment.place}" in rows[legacy_session]

    listed_in_second = await asyncio.to_thread(
        cli_home.run, *second_environment.arguments("sessions")
    )
    assert listed_in_second.returncode == 0, listed_in_second.stderr
    rows = _rows_by_id(listed_in_second.stdout)
    assert f"  on {first_environment.place}" in rows[first_session]
    assert f"  on {second_environment.place}" in rows[second_session]
    assert legacy_session not in rows, "another environment's hashed sessions stay out"


def _rows_by_id(output: str) -> dict[str, str]:
    return {line.split("  ", 1)[0]: line for line in output.splitlines() if "  started " in line}


class _TwoRunTerminalUi:
    """A fake dexcli that plays one transcript on the first launch and another on the next."""

    def __init__(
        self, directory: Path, first: Mapping[str, Any], second: Mapping[str, Any]
    ) -> None:
        self.first = FakeTerminalUi(directory / "first", first)
        self.second = FakeTerminalUi(directory / "second", second)
        marker = directory / "launched"
        self.dexcli = directory / "dexcli"
        self.dexcli.write_text(
            "#!/bin/sh\n"
            f'case "$*" in *--describe*) exec \'{self.first.dexcli}\' "$@";; esac\n'
            f"if [ -e '{marker}' ]; then exec '{self.second.dexcli}' \"$@\"; fi\n"
            f"touch '{marker}'\n"
            f"exec '{self.first.dexcli}' \"$@\"\n"
        )
        self.dexcli.chmod(0o755)

    def environment(self) -> dict[str, str]:
        return {**self.first.environment(), "DEXCLI": str(self.dexcli)}


async def test_a_terminal_ui_switch_moves_the_session_worker_to_the_chosen_sessions_queue(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    plain, chosen_session_id = await _open_chat(cli_home)
    await _exit_chat(plain)
    switching = chat_transcript(
        [
            {
                "from": "tui",
                "message": {
                    "type": "switch_session",
                    "session_id": chosen_session_id,
                    "is_resumed": True,
                },
            }
        ]
    )
    # The relaunch names the chosen session with its own Flow type and no previous_session_id.
    relaunch = chat_transcript([pause(20_000)])
    launch_step = relaunch["steps"][0]
    launch_step["message"]["session"] = {
        "flow_type": SCRIPTED_FLOW_TYPE,
        "session_id": chosen_session_id,
        "is_resumed": True,
    }
    launch_step["ignore"] = [
        path
        for path in launch_step["ignore"]
        if path not in ("session.session_id", "session.flow_type")
    ]
    fake = _TwoRunTerminalUi(tmp_path / "tui", switching, relaunch)
    cli_home.extra_environment.update(fake.environment())
    screen = io.StringIO()
    child = cli_home.spawn()
    child.logfile_read = screen
    left_session_id = await _read_session_id(child)
    assert left_session_id != chosen_session_id
    await fake.second.wait_for("received", type="launch")
    async with _session_reader(cli_home, tmp_path) as reader:

        async def is_chosen_held() -> bool:
            return await reader.read(chosen_session_id, SESSION_TERMINAL_LEASE) is not None

        await wait_until("the relaunch on the chosen session", is_chosen_held)
        assert fake.first.mismatches() == [], screen.getvalue()
        assert fake.second.mismatches() == [], screen.getvalue()
        assert await reader.read(left_session_id, SESSION_TERMINAL_LEASE) is None, "released"

        assert await reader.sessions.send_user_message(chosen_session_id, "after the switch")
        await reader.wait_for_state(
            chosen_session_id,
            lambda state: state.turn_count >= 1 and not state.active_turn_id,
            "the chosen session's turn",
        )
        with pytest.raises(TimeoutError):
            # No Worker pulls the left session's task queue, so Dex holds the RPC.
            await asyncio.wait_for(
                reader.sessions.send_user_message(left_session_id, "nobody pulls this"), 3
            )
    assert session_task_queue(chosen_session_id) != session_task_queue(left_session_id)
