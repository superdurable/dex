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
import time
from collections.abc import AsyncIterator, Callable, Mapping
from dataclasses import dataclass
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any, cast

import grpc
import pexpect
import pytest
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    ErrorSubStatus,
    PullQueueRegistrationState,
    PullRegistrationState,
    Registry,
    RpcLockConflictError,
    open_blob_cache,
)

from dex_agents import (
    AGENT_STATE,
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    SESSION_TERMINAL_LEASE,
    AgentSessionClient,
    AgentState,
)
from dex_agents.testing import wait_until
from dex_agents_cli_core import session_flow_type, session_opener, session_task_queue
from dex_agents_cli_core.harness import (
    current_session_environment_identity,
    session_start_environment,
)
from dex_agents_cli_core.session_lease import (
    PullPermitExpiredError,
    SessionLeaseHolder,
    build_lease_unconfirmed_line,
    build_took_over_line,
)
from dex_agents_cli_core.session_opener import (
    MonotonicPermitClock,
    SessionOpener,
    SessionPullPermit,
    SessionQueueWorker,
    TakenOverTurnRefusal,
    _PendingTakeover,
)
from dex_agents_cli_core.session_task_queues import (
    CANCELLED_TURN_LINE,
    TURN_NOT_CANCELLED_LINE,
    build_takeover_line,
    cli_app_worker_id,
    describe_session_environment,
    describe_session_environment_identity,
    foreign_task_queue_session_error,
    is_legacy_session_flow_type,
    is_other_session_environment,
    named_task_queue_server_error,
    other_environment_legacy_session_error,
    other_harness_session_error,
    refused_model_call_error,
    refused_tool_call_error,
    taken_over_turn_reason,
)
from tests.conftest import CliHome, config_yaml, expect, session_client, wait_for_exit
from tests.fake_terminal_ui import FakeTerminalUi, chat_transcript
from tests.scripted_cli import (
    ACQUIRE_ANSWERS_FILE,
    FAIL_ACQUIRE_MARKER,
    HANG_LEASE_READ_MARKER,
    LEASE_FAULT_ENVIRONMENT_VARIABLE,
    LOSE_NEXT_ACQUIRE_MARKER,
    SLOW_NEXT_ACQUIRE_MARKER,
    THEN_HANG_LEASE_READS,
)
from tests.scripted_harness import SCRIPTED_FLOW_TYPE, build_session_flow_without_tools
from tests.test_terminal_ui_handoff import pause, start_chat

AGENTS_DIR = Path(__file__).resolve().parents[4]
FIXTURE: dict[str, Any] = json.loads(
    (AGENTS_DIR / "conformance" / "cli_session_task_queues.json").read_text(encoding="utf-8")
)
NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}
# The chat's prompt line; a bare "> " also matches the help line's "<path> attaches".
PROMPT = "\r\n> "
# Only for a command a test lets expire on purpose, by killing it or with a fault; every other
# command keeps the default lease, which no slow runner lets lapse by accident.
EXPIRING_TTL_SECONDS = "6"
TTL_ENVIRONMENT_VARIABLE = "DEX_AGENTS_SESSION_LEASE_TTL_SECONDS"

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
    "foreign_task_queue_session_error": lambda values: foreign_task_queue_session_error(
        values["session_id"], values["command_name"]
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


@pytest.mark.parametrize("case", FIXTURE["other_environment_cases"], ids=lambda case: case["name"])
def test_other_environments_match_the_shared_cases(case: dict[str, Any]) -> None:
    is_other = is_other_session_environment(case["previous"], case["current"])
    assert is_other is case["is_other_environment"]


@pytest.mark.parametrize("case", FIXTURE["identity_cases"], ids=lambda case: case["name"])
def test_the_identity_text_matches_the_shared_cases(case: dict[str, Any]) -> None:
    assert describe_session_environment_identity(case["environment"]) == case["text"]


def test_every_template_case_renders_its_line() -> None:
    assert FIXTURE["templates"]["cancelled_turn_line"] == CANCELLED_TURN_LINE
    assert FIXTURE["templates"]["turn_not_cancelled_line"] == TURN_NOT_CANCELLED_LINE
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
    def identity(self) -> str:
        environment = session_start_environment(
            current_session_environment_identity(self.config_path, self.workspace)
        )
        return describe_session_environment_identity(environment)

    @property
    def legacy_flow_type(self) -> str:
        return session_flow_type(SCRIPTED_FLOW_TYPE, self.config_path, self.workspace)

    def arguments(self, *more: str) -> tuple[str, ...]:
        return ("--config", str(self.config_path), *more)


def _new_environment(tmp_path: Path, name: str, workspace: Path | None = None) -> _Environment:
    """Write a configuration file of its own, with its own workspace unless one is given."""
    if workspace is None:
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
    """Start a session as a release before 0.6.8 did: hashed Flow type, default task queue."""
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
            build_takeover_line(
                session_id, first_environment.identity, second_environment.identity
            ),
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
        second,
        build_takeover_line(session_id, first_environment.identity, second_environment.identity),
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
    await _check_takeover_cancels_the_turn(
        cli_home, tmp_path, first_environment, second_environment
    )


async def test_another_configuration_file_in_the_same_workspace_also_cancels_the_turn(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    # The same host and workspace with another file is another environment too.
    second_environment = _new_environment(tmp_path, "b", workspace=first_environment.workspace)
    await _check_takeover_cancels_the_turn(
        cli_home, tmp_path, first_environment, second_environment
    )


async def _check_takeover_cancels_the_turn(
    cli_home: CliHome,
    tmp_path: Path,
    first_environment: _Environment,
    second_environment: _Environment,
) -> None:
    cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE] = EXPIRING_TTL_SECONDS
    first, session_id = await _open_chat(cli_home, *first_environment.arguments())
    del cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE]
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
            build_takeover_line(
                session_id, first_environment.identity, second_environment.identity
            ),
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


async def test_a_session_of_the_harness_flow_type_another_client_started_fails_at_once(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment = _new_environment(tmp_path, "a")
    session_id = f"scripted-foreign-{os.urandom(4).hex()}"
    async with _session_reader(cli_home, tmp_path) as reader:
        # Started as another client may: the harness's Flow type on the default task queue.
        await reader.sessions.start_session(session_id)
        started_at = time.monotonic()
        resumed = await asyncio.to_thread(
            cli_home.run, *environment.arguments("--resume", session_id)
        )
        assert resumed.returncode == 2, resumed.stderr
        assert time.monotonic() - started_at < 15, "no lease wait"
        expected = foreign_task_queue_session_error(session_id, "dex-agents-scripted")
        assert expected in resumed.stderr, resumed.stderr
        lease = await reader.read(session_id, SESSION_TERMINAL_LEASE)
        assert lease is None, "the command never tried the lease"


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


async def test_a_lease_no_renewal_confirms_ends_the_chat_before_another_terminal_takes_it(
    cli_home: CliHome, tmp_path: Path
) -> None:
    first_environment = _new_environment(tmp_path, "a")
    second_environment = _new_environment(tmp_path, "b")
    first_faults = tmp_path / "first-faults"
    second_faults = tmp_path / "second-faults"
    first_faults.mkdir()
    second_faults.mkdir()
    cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE] = EXPIRING_TTL_SECONDS
    cli_home.extra_environment[LEASE_FAULT_ENVIRONMENT_VARIABLE] = str(first_faults)
    first, session_id = await _open_chat(cli_home, *first_environment.arguments())
    del cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE]
    cli_home.extra_environment[LEASE_FAULT_ENVIRONMENT_VARIABLE] = str(second_faults)
    second = cli_home.spawn(*second_environment.arguments("--resume", session_id))
    await expect(second, f"Session {session_id} is open in another terminal (")

    # Every renewal fails from now on, as on a broken network.
    (first_faults / FAIL_ACQUIRE_MARKER).touch()
    await expect(first, build_lease_unconfirmed_line(session_id), timeout=20)
    assert await wait_for_exit(first) == 1
    async with _session_reader(cli_home, tmp_path) as reader:
        left_lease = await reader.read(session_id, SESSION_TERMINAL_LEASE)
        assert left_lease is not None, "the unconfirmed chat released nothing"
        # It stopped pulling before the lease expired; its exit may come later, after cleanup.
        stopped_at = _logged_permit_expiry_time(cli_home, session_id)
        assert stopped_at < datetime.fromisoformat(left_lease.expires_at)
        await expect(second, build_took_over_line(session_id), timeout=30)
        assert await _read_session_id(second) == session_id
        taken_lease = await reader.read(session_id, SESSION_TERMINAL_LEASE)
        assert taken_lease.holder_id != left_lease.holder_id
        opened_at = datetime.fromisoformat(taken_lease.opened_at)
        assert opened_at >= datetime.fromisoformat(left_lease.expires_at)
    await expect(second, PROMPT)
    await _run_pwd(second, second_environment)
    await _exit_chat(second)


#: The log line of a pull permit that expired, after its session Worker stopped pulling.
PERMIT_EXPIRED_MARKER = "session pull permit expired; stopped pulling its task queue: session="


def _logged_permit_expiry_time(cli_home: CliHome, session_id: str) -> datetime:
    """Return the local time of the log line that says the command stopped pulling the session."""
    log = (cli_home.home / ".dex" / "agents" / "scripted.log").read_text(encoding="utf-8")
    marker = f"{PERMIT_EXPIRED_MARKER}{session_id}"
    (line,) = [line for line in log.splitlines() if line.endswith(marker)]
    return _logged_time(line)


async def _wait_for_permit_expiry(cli_home: CliHome) -> tuple[str, datetime]:
    """Wait for the first pull permit expiry in the log; return its session and local time."""
    log = cli_home.home / ".dex" / "agents" / "scripted.log"

    async def expired_line() -> str | None:
        if not log.exists():
            return None
        lines = log.read_text(encoding="utf-8").splitlines()
        return next((line for line in lines if PERMIT_EXPIRED_MARKER in line), None)

    line = await wait_until("the pull permit to expire", expired_line)
    return line.split(PERMIT_EXPIRED_MARKER, 1)[1], _logged_time(line)


async def test_an_acquire_whose_answer_was_lost_still_opens_the_session(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment = _new_environment(tmp_path, "a")
    faults = tmp_path / "faults"
    faults.mkdir()
    (faults / LOSE_NEXT_ACQUIRE_MARKER).touch()
    cli_home.extra_environment[LEASE_FAULT_ENVIRONMENT_VARIABLE] = str(faults)
    child, session_id = await _open_chat(cli_home, *environment.arguments())
    assert not (faults / LOSE_NEXT_ACQUIRE_MARKER).exists(), "the first acquire's answer was lost"
    async with _session_reader(cli_home, tmp_path) as reader:
        lease = await reader.read(session_id, SESSION_TERMINAL_LEASE)
        assert lease is not None
        child.sendline("hello")
        await expect(child, "echo: hello")
        await expect(child, PROMPT)
        assert (await reader.read(session_id, SESSION_TERMINAL_LEASE)).holder_id == lease.holder_id
        await _exit_chat(child)
        assert await reader.read(session_id, SESSION_TERMINAL_LEASE) is None, "the exit released it"


@dataclass
class _ActiveTurn:
    active_turn_id: str | None


class _CancelProbeClient:
    """A Client stand-in: the turn stays active unless an accepted cancel ends it."""

    def __init__(self, cancel_outcomes: list[bool | Exception], *, ends_after_cancel: bool) -> None:
        self._cancel_outcomes = cancel_outcomes
        self._ends_after_cancel = ends_after_cancel
        self.cancel_calls = 0
        self._active_turn_id: str | None = "turn-1"

    async def get_attributes(self, session_id: str, *definitions: Any) -> dict[Any, Any]:
        return {AGENT_STATE: _ActiveTurn(self._active_turn_id)}

    async def invoke_rpc(self, rpc: Any, session_id: str, request: Any) -> bool:
        self.cancel_calls += 1
        outcome = self._cancel_outcomes[min(self.cancel_calls, len(self._cancel_outcomes)) - 1]
        if isinstance(outcome, Exception):
            raise outcome
        if outcome and self._ends_after_cancel:
            self._active_turn_id = None
        return outcome


def _lock_conflict() -> RpcLockConflictError:
    return RpcLockConflictError(
        grpc.StatusCode.ABORTED,
        ErrorSubStatus.UNCATEGORIZED,
        "the lock is held",
        "InvokeRPC",
        "session-1",
    )


def _opener_for(client: _CancelProbeClient) -> SessionOpener:
    flow = build_session_flow_without_tools(SCRIPTED_FLOW_TYPE)
    legacy_flow = build_session_flow_without_tools(f"{SCRIPTED_FLOW_TYPE}-abcdef")
    opener = SessionOpener(
        client=cast(AsyncClient, client),
        harness_flow_type=SCRIPTED_FLOW_TYPE,
        session_flow=flow,
        legacy_session_flow=legacy_flow,
        command_name="dex-agents-scripted",
        holder=SessionLeaseHolder.for_this_process(15),
        environment={"host_name": "host-b", "user_id": "501", "workspace": "/b"},
        session_worker=cast(SessionQueueWorker, None),
        turn_refusal=TakenOverTurnRefusal(),
    )
    previous = {"host_name": "host-a", "user_id": "501", "workspace": "/a"}
    opener._pending_takeovers["session-1"] = _PendingTakeover(previous, "turn-1")
    return opener


@pytest.mark.parametrize(
    ("outcomes", "ends_after_cancel", "is_cancelled"),
    [
        ([True], True, True),
        ([_lock_conflict()], False, False),
        ([False], False, False),
        ([ConnectionError("the RPC failed")], False, False),
        ([True], False, False),
        ([_lock_conflict(), True], True, True),
    ],
    ids=[
        "confirmed",
        "lock-conflicts-until-the-deadline",
        "false-while-still-active",
        "rpc-error",
        "still-active-after-the-wait",
        "a-lock-conflict-then-confirmed",
    ],
)
async def test_a_takeover_reports_the_turn_cancelled_only_once_it_is_no_longer_active(
    monkeypatch: pytest.MonkeyPatch,
    outcomes: list[bool | Exception],
    ends_after_cancel: bool,
    is_cancelled: bool,
) -> None:
    monkeypatch.setattr(session_opener, "_TURN_CANCELLATION_TIMEOUT_SECONDS", 0.3)
    monkeypatch.setattr(session_opener, "_TURN_END_TIMEOUT_SECONDS", 0.3)
    client = _CancelProbeClient(outcomes, ends_after_cancel=ends_after_cancel)
    lines = await _opener_for(client).complete_takeover("session-1")
    assert lines[0].startswith("Session session-1 was last open on 501@host-a:/a ()")
    assert lines[1:] == [CANCELLED_TURN_LINE if is_cancelled else TURN_NOT_CANCELLED_LINE]
    assert client.cancel_calls >= 1


async def test_a_terminal_ui_whose_lease_no_renewal_confirms_stops_with_one_line(
    cli_home: CliHome, tmp_path: Path
) -> None:
    faults = tmp_path / "faults"
    faults.mkdir()
    cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE] = EXPIRING_TTL_SECONDS
    cli_home.extra_environment[LEASE_FAULT_ENVIRONMENT_VARIABLE] = str(faults)
    screen = io.StringIO()
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(300_000)]))
    child = start_chat(cli_home, fake, screen)
    session_id = await _read_session_id(child)
    await fake.wait_for("sent", type="ready")
    # The Worker host inherits the fault directory, so its renewals fail from now on.
    (faults / FAIL_ACQUIRE_MARKER).touch()
    await expect(child, build_lease_unconfirmed_line(session_id), timeout=30)
    assert await wait_for_exit(child) == 1
    assert "Traceback" not in screen.getvalue()


async def test_an_acquire_answer_slower_than_the_lease_stops_pulling_and_opens_nothing_late(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment = _new_environment(tmp_path, "a")
    faults = tmp_path / "faults"
    faults.mkdir()
    # Dex grants the lease at once; its answer arrives after the whole 6 s lease.
    (faults / SLOW_NEXT_ACQUIRE_MARKER).write_text("7000")
    cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE] = EXPIRING_TTL_SECONDS
    cli_home.extra_environment[LEASE_FAULT_ENVIRONMENT_VARIABLE] = str(faults)
    child = cli_home.spawn(*environment.arguments())
    session_id, stopped_at = await _wait_for_permit_expiry(cli_home)
    async with _session_reader(cli_home, tmp_path) as reader:
        granted_lease = await reader.read(session_id, SESSION_TERMINAL_LEASE)
    assert granted_lease is not None, "Dex granted the lease before the slow answer"
    assert stopped_at < datetime.fromisoformat(granted_lease.expires_at)

    # The command waits, pulls again, and opens the session only on a newer, confirmed acquire.
    assert await _read_session_id(child) == session_id
    answers = (faults / ACQUIRE_ANSWERS_FILE).read_text(encoding="utf-8").split()
    assert any(datetime.fromisoformat(answer) > stopped_at for answer in answers), answers
    await expect(child, PROMPT)
    child.sendline("hello after the late grant")
    await expect(child, "echo: hello after the late grant")
    await expect(child, PROMPT)
    await _exit_chat(child)


async def test_a_hanging_lease_read_after_a_lost_acquire_answer_cannot_keep_the_worker_pulling(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment = _new_environment(tmp_path, "a")
    faults = tmp_path / "faults"
    faults.mkdir()
    # The first acquire's answer is lost, then every lease read waits.
    (faults / LOSE_NEXT_ACQUIRE_MARKER).write_text(THEN_HANG_LEASE_READS)
    cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE] = EXPIRING_TTL_SECONDS
    cli_home.extra_environment[LEASE_FAULT_ENVIRONMENT_VARIABLE] = str(faults)
    child = cli_home.spawn(*environment.arguments())
    session_id, stopped_at = await _wait_for_permit_expiry(cli_home)
    async with _session_reader(cli_home, tmp_path) as reader:
        lease = await reader.read(session_id, SESSION_TERMINAL_LEASE)
        assert lease is not None, "the acquire whose answer was lost took the lease"
        assert stopped_at < datetime.fromisoformat(lease.expires_at)

        # Once the lease reads answer again, the command opens the session.
        (faults / HANG_LEASE_READ_MARKER).unlink()
        assert await _read_session_id(child) == session_id
        await expect(child, PROMPT)
        await _exit_chat(child)
        assert await reader.read(session_id, SESSION_TERMINAL_LEASE) is None


async def test_unanswered_lease_reads_under_a_long_permit_keep_the_attempts_coming(
    cli_home: CliHome, tmp_path: Path
) -> None:
    environment = _new_environment(tmp_path, "a")
    faults = tmp_path / "faults"
    faults.mkdir()
    # The default 600 s lease: each read must give up after seconds, not when the permit ends.
    (faults / LOSE_NEXT_ACQUIRE_MARKER).write_text(THEN_HANG_LEASE_READS)
    cli_home.extra_environment[LEASE_FAULT_ENVIRONMENT_VARIABLE] = str(faults)
    child = cli_home.spawn(*environment.arguments())
    log = cli_home.home / ".dex" / "agents" / "scripted.log"

    async def failed_attempts() -> list[str] | None:
        lines = log.read_text(encoding="utf-8").splitlines() if log.exists() else []
        failures = [line for line in lines if "cannot acquire the session lease" in line]
        return failures if len(failures) >= 3 else None

    failures = await wait_until(
        "three failed attempts while lease reads hang",
        failed_attempts,
        timeout=timedelta(seconds=40),
    )
    first, third = _logged_time(failures[0]), _logged_time(failures[2])
    assert third - first < timedelta(seconds=20), failures
    assert PERMIT_EXPIRED_MARKER not in log.read_text(encoding="utf-8")

    (faults / HANG_LEASE_READ_MARKER).unlink()
    await _read_session_id(child)
    await expect(child, PROMPT)
    await _exit_chat(child)


def _logged_time(line: str) -> datetime:
    """Return the local time at the start of a log line."""
    return datetime.strptime(line[:23], "%Y-%m-%d %H:%M:%S,%f").astimezone()


@dataclass
class _ManualTimer:
    due: float
    callback: Callable[[], None]
    is_cancelled: bool = False

    def cancel(self) -> None:
        self.is_cancelled = True


class _ManualPermitClock:
    """A clock the test moves by hand; the timers that come due fire in order as it moves."""

    def __init__(self, now: float = 0.0) -> None:
        self._now = now
        self._timers: list[_ManualTimer] = []

    def now(self) -> float:
        return self._now

    def call_after(self, delay: float, callback: Callable[[], None]) -> Callable[[], None]:
        timer = _ManualTimer(self._now + delay, callback)
        self._timers.append(timer)
        return timer.cancel

    def advance_to(self, target: float) -> None:
        """Move the clock to ``target``, firing each timer due by then at its own time."""
        while True:
            due = [
                timer for timer in self._timers if not timer.is_cancelled and timer.due <= target
            ]
            if not due:
                break
            timer = min(due, key=lambda candidate: candidate.due)
            timer.is_cancelled = True
            self._now = max(self._now, timer.due)
            timer.callback()
        self._now = target


async def _run_ready_callbacks() -> None:
    """Let the callbacks already scheduled on the event loop run, such as an expiry notice."""
    for _ in range(3):
        await asyncio.sleep(0)


async def test_a_pull_permit_expires_a_fifth_of_the_ttl_early_and_stops_pulling_first() -> None:
    clock = _ManualPermitClock()
    events: list[str] = []
    permit = SessionPullPermit(1, clock.now(), lambda: events.append("stopped pulling"), clock)
    permit.when_expired(lambda: events.append("told the chat"))
    clock.advance_to(0.79)
    assert not permit.is_expired
    assert permit.remaining_seconds() == pytest.approx(0.01)
    clock.advance_to(0.8)
    assert events == ["stopped pulling"]
    assert permit.is_expired
    assert permit.remaining_seconds() == 0
    await _run_ready_callbacks()
    assert events == ["stopped pulling", "told the chat"]


async def test_only_a_confirmation_extends_a_pull_permit_from_the_confirmed_send() -> None:
    # Pulling started 0.7 s ago, so 0.1 s remain.
    clock = _ManualPermitClock(0.7)
    permit = SessionPullPermit(1, 0.0, lambda: None, clock)
    assert permit.confirm(0.7)
    # A confirmation of an older request never shortens the permit.
    assert permit.confirm(0.2)
    clock.advance_to(1.49)
    assert not permit.is_expired
    clock.advance_to(1.51)
    assert permit.is_expired


async def test_an_answer_after_a_pull_permit_expired_grants_nothing() -> None:
    clock = _ManualPermitClock()
    permit = SessionPullPermit(1, 0.0, lambda: None, clock)
    told: list[str] = []
    permit.when_expired(lambda: told.append("before"))
    clock.advance_to(0.8)
    await _run_ready_callbacks()
    assert permit.is_expired
    assert not permit.confirm(0.8)
    assert permit.is_expired
    permit.when_expired(lambda: told.append("after"))
    assert told == ["before", "after"], "a listener of an expired permit hears at once"


async def test_a_confirmation_after_the_deadline_expires_the_pull_permit_before_its_timer() -> None:
    # The deadline passed 0.2 s ago; the timer that is due now has not fired.
    clock = _ManualPermitClock(1.0)
    events: list[str] = []
    permit = SessionPullPermit(1, 0.0, lambda: events.append("stopped pulling"), clock)
    permit.when_expired(lambda: events.append("told the chat"))
    assert not permit.confirm(1.0)
    assert permit.is_expired
    assert events == ["stopped pulling"]
    await _run_ready_callbacks()
    assert events == ["stopped pulling", "told the chat"]
    assert not permit.confirm(1.0)


async def test_an_expired_permits_listeners_hear_though_it_is_cancelled_before_the_notice() -> None:
    clock = _ManualPermitClock(1.0)
    events: list[str] = []
    permit = SessionPullPermit(1, 0.0, lambda: events.append("stopped pulling"), clock)
    permit.when_expired(lambda: events.append("told the chat"))
    assert not permit.confirm(1.0)
    # Registered between the expiry and its notice.
    permit.when_expired(lambda: events.append("told the launcher"))
    # Rebuilding the session's permit cancels this one before the notice runs.
    permit.cancel()
    await _run_ready_callbacks()
    assert events == ["stopped pulling", "told the chat", "told the launcher"]


def _permit_opener(session_worker: SessionQueueWorker) -> SessionOpener:
    return SessionOpener(
        client=cast(AsyncClient, None),
        harness_flow_type=SCRIPTED_FLOW_TYPE,
        session_flow=build_session_flow_without_tools(SCRIPTED_FLOW_TYPE),
        legacy_session_flow=build_session_flow_without_tools(f"{SCRIPTED_FLOW_TYPE}-abcdef"),
        command_name="dex-agents-scripted",
        holder=SessionLeaseHolder.for_this_process(15),
        environment={"host_name": "host-b", "user_id": "501", "workspace": "/b"},
        session_worker=session_worker,
        turn_refusal=TakenOverTurnRefusal(),
    )


async def test_a_permit_rebuilt_before_its_expiry_notice_still_ends_the_chat() -> None:
    events: list[str] = []
    opener = _permit_opener(_recording_queue_worker(events))
    # Its deadline passed already.
    expired = opener._arm_permit("session-1", time.monotonic() - 15)
    opener.when_permit_expires("session-1", lambda: events.append("told the chat"))
    # A late answer expires it; the notice waits for the next loop turn.
    assert not expired.confirm(time.monotonic())
    # A concurrent attempt rebuilds the session's permit in that window, then fails.
    opener._arm_permit("session-1", time.monotonic())
    opener.leave("session-1")
    await asyncio.sleep(0)
    assert events == ["told the chat"]


async def test_a_cancelled_pull_permit_takes_no_confirmation_and_never_stops_pulling() -> None:
    clock = _ManualPermitClock(0.7)
    events: list[str] = []
    permit = SessionPullPermit(1, 0.0, lambda: events.append("stopped pulling"), clock)
    permit.when_expired(lambda: events.append("told the chat"))
    permit.cancel()
    # A renewal in flight when the session was left answers now.
    assert not permit.confirm(0.7)
    clock.advance_to(10.0)
    await _run_ready_callbacks()
    assert events == []
    assert not permit.is_expired


class _RecordingWorker:
    """A session Worker that only records when it closes."""

    def __init__(self, events: list[str], name: str) -> None:
        self._events = events
        self._name = name
        self._closed = asyncio.Event()

    async def start(self) -> None:
        await self._closed.wait()

    async def wait_pull_registered(self, timeout: float | None = None) -> None:
        return None

    def pull_registration_state(self) -> PullRegistrationState:
        return _registration_state(has_registered_stream=True)

    async def close(self) -> None:
        # Like AsyncWorker.close, it stops new tasks before it first waits.
        self._events.append(f"closed {self._name}")
        self._closed.set()


def _registration_state(*, has_registered_stream: bool) -> PullRegistrationState:
    return PullRegistrationState(
        is_ready=False,
        queues=(PullQueueRegistrationState("STEP_AND_RPC", has_registered_stream, None),),
    )


def _recording_queue_worker(events: list[str]) -> SessionQueueWorker:
    return SessionQueueWorker(
        lambda task_queue, app_worker_id: cast(
            AsyncWorker, _RecordingWorker(events, f"{app_worker_id} of {task_queue}")
        ),
        lambda incarnation: f"worker-{incarnation}",
        str,
    )


async def test_an_older_pull_permits_expiry_never_stops_a_newer_worker_of_the_same_queue() -> None:
    clock = _ManualPermitClock(0.7)
    events: list[str] = []
    session_worker = _recording_queue_worker(events)
    # 0.1 s remain; the Worker stops without cancelling the permit, to isolate the Worker check.
    older_permit = SessionPullPermit(
        1, 0.0, lambda: session_worker.stop_if_pulling_under(older_permit), clock
    )
    await session_worker.pull("s-queue", "session", older_permit)
    session_worker.stop()
    newer_permit = SessionPullPermit(
        60, 0.7, lambda: session_worker.stop_if_pulling_under(newer_permit), clock
    )
    try:
        await session_worker.pull("s-queue", "session", newer_permit)
        clock.advance_to(0.8)
        await _run_ready_callbacks()
        assert older_permit.is_expired
        assert session_worker.task_queue == "s-queue", "the newer Worker still pulls"
        assert events == ["closed worker-1 of s-queue"]
    finally:
        newer_permit.cancel()
        await session_worker.close()


async def test_a_new_pull_permit_gets_a_new_worker_even_for_the_same_queue() -> None:
    clock = _ManualPermitClock()
    events: list[str] = []
    session_worker = _recording_queue_worker(events)
    first_permit = SessionPullPermit(60, 0.0, lambda: None, clock)
    second_permit = SessionPullPermit(60, 0.0, lambda: None, clock)
    await session_worker.pull("s-queue", "session", first_permit)
    await session_worker.pull("s-queue", "session", first_permit)
    await _run_ready_callbacks()
    assert events == [], "the same permit keeps its Worker"
    await session_worker.pull("s-queue", "session", second_permit)
    await _run_ready_callbacks()
    assert events == ["closed worker-1 of s-queue"]
    session_worker.stop_if_pulling_under(first_permit)
    assert session_worker.task_queue == "s-queue"
    session_worker.stop_if_pulling_under(second_permit)
    assert session_worker.task_queue is None
    await _run_ready_callbacks()
    assert events == ["closed worker-1 of s-queue", "closed worker-2 of s-queue"]
    await session_worker.close()


class _SlowStartWorker:
    """A session Worker whose start pulls once its Server answers, even after a close."""

    def __init__(self, events: list[str], server_answered: asyncio.Event) -> None:
        self._events = events
        self._server_answered = server_answered
        self._pulling = asyncio.Event()
        self._closed = asyncio.Event()

    async def start(self) -> None:
        await self._server_answered.wait()
        self._events.append("pulling")
        self._pulling.set()
        await self._closed.wait()

    async def wait_pull_registered(self, timeout: float | None = None) -> None:
        await self._pulling.wait()

    def pull_registration_state(self) -> PullRegistrationState:
        return _registration_state(has_registered_stream=self._pulling.is_set())

    async def close(self) -> None:
        self._events.append("closed")
        self._closed.set()


async def test_a_worker_stopped_while_it_starts_never_pulls_once_its_server_answers() -> None:
    events: list[str] = []
    server_answered = asyncio.Event()
    session_worker = SessionQueueWorker(
        lambda task_queue, app_worker_id: cast(
            AsyncWorker, _SlowStartWorker(events, server_answered)
        ),
        lambda incarnation: f"worker-{incarnation}",
        str,
    )
    permit = SessionPullPermit(60, 0.0, lambda: None, _ManualPermitClock())
    pulling = asyncio.create_task(session_worker.pull("s-queue", "session", permit))
    await asyncio.sleep(0)
    # The permit expires while the Worker still waits for its Server.
    session_worker.stop()
    with pytest.raises(PullPermitExpiredError):
        await pulling
    server_answered.set()
    await session_worker.close()
    assert events == ["closed"]


class _PartlyRegisteredWorker:
    """A session Worker with one registered stream running a task, before every slot registered."""

    def __init__(self, events: list[str]) -> None:
        self._events = events
        self._has_registered_stream = False
        self._closed = asyncio.Event()

    async def start(self) -> None:
        self._has_registered_stream = True
        self._events.append("took a task")
        await self._closed.wait()
        # The stop's drain lets the task send its output.
        self._events.append("sent the task output")

    async def wait_pull_registered(self, timeout: float | None = None) -> None:
        await self._closed.wait()

    def pull_registration_state(self) -> PullRegistrationState:
        return _registration_state(has_registered_stream=self._has_registered_stream)

    async def close(self) -> None:
        self._events.append("closed")
        self._closed.set()


async def test_a_worker_stopped_once_a_stream_registered_drains_its_task_without_a_cancel() -> None:
    events: list[str] = []
    session_worker = SessionQueueWorker(
        lambda task_queue, app_worker_id: cast(AsyncWorker, _PartlyRegisteredWorker(events)),
        lambda incarnation: f"worker-{incarnation}",
        str,
    )
    permit = SessionPullPermit(60, 0.0, lambda: None, _ManualPermitClock())
    pulling = asyncio.create_task(session_worker.pull("s-queue", "session", permit))
    await _run_ready_callbacks()
    assert events == ["took a task"]
    # The permit expires before the Worker is ready.
    session_worker.stop()
    with pytest.raises(PullPermitExpiredError):
        await pulling
    await session_worker.close()
    assert events == ["took a task", "closed", "sent the task output"]


async def test_a_pull_permits_expiry_stops_new_tasks_before_it_tells_listeners() -> None:
    clock = _ManualPermitClock(0.7)
    events: list[str] = []
    session_worker = _recording_queue_worker(events)
    permit = SessionPullPermit(1, 0.0, lambda: session_worker.stop_if_pulling_under(permit), clock)
    permit.when_expired(lambda: events.append("told the chat"))
    await session_worker.pull("s-queue", "session", permit)
    clock.advance_to(0.8)
    await _run_ready_callbacks()
    assert events == ["closed worker-1 of s-queue", "told the chat"]
    await session_worker.close()


async def test_a_pull_permits_default_clock_is_monotonic_so_a_wall_clock_jump_never_moves_it(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    wall_time = time.time()
    clock = MonotonicPermitClock()
    before = clock.now()
    monkeypatch.setattr(time, "time", lambda: wall_time + 3600)
    after_forward_jump = clock.now()
    monkeypatch.setattr(time, "time", lambda: wall_time - 3600)
    after_backward_jump = clock.now()
    assert after_forward_jump - before < 60, "a forward jump does not move it"
    assert after_backward_jump >= after_forward_jump, "a backward jump does not move it back"
