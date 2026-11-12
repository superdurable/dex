# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A dexcli dev that a command started stops when the command exits; a reused one keeps running."""

from __future__ import annotations

import asyncio
import json
import os
import signal
from collections.abc import Iterator
from pathlib import Path

import httpx2
import pexpect
import pytest

from dex_agents.testing import DEXCLI_ENVIRONMENT_VARIABLE, PrivateDexEngine, find_free_ports
from dex_agents_cli_core.cli import _CommandDexServer
from dex_agents_cli_core.environment import CommandEnvironment
from tests.conftest import CliHome, config_yaml, expect, wait_for_exit

_NO_CONNECTORS = {"connectors": {"enabled": []}, "local_tools": {"browser": False}}
_STOPPED_LINE = "Stopped the dexcli dev this command started."
_STOP_SECONDS = 30.0


@pytest.fixture
def private_ports_home(tmp_path: Path) -> Iterator[CliHome]:
    """Give a test a private home on free ports, where no dexcli dev runs yet."""
    dex_port, web_port = find_free_ports(2)
    home = CliHome(tmp_path / "home", dex_port, web_port, tmp_path / "opened-urls.txt")
    home.home.mkdir()
    home.write_config(config_yaml(_NO_CONNECTORS))
    yield home
    home.close()


def _record_path(home: CliHome) -> Path:
    return home.home / ".dex" / "agents" / "dexcli-dev.json"


async def _start_session(home: CliHome, *arguments: str) -> tuple[pexpect.spawn, int | None]:
    """Spawn the command, wait for its prompt, and return the pid of a dexcli dev it started."""
    child = home.spawn(*arguments)
    index = await expect(child, ["dexcli dev is ready (pid ", "Using the running dexcli dev"])
    started_pid = None
    if index == 0:
        await asyncio.to_thread(child.expect, r"(\d+)\); it stops when this command exits\.")
        started_pid = int(child.match.group(1))
        await expect(
            child, "To share one dexcli dev across sessions, run dexcli dev yourself first."
        )
    await expect(child, "> ")
    return child, started_pid


async def _read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+) keeps running\.")
    return str(child.match.group(1))


async def _assert_stopped(home: CliHome, pid: int) -> None:
    """Wait until the process group and Dex Web of a started dexcli dev are gone."""
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _STOP_SECONDS
    while _is_process_group_alive(pid):
        assert loop.time() < deadline, f"dexcli dev group {pid} still runs"
        await asyncio.sleep(0.1)
    assert not await _is_answering(home.web_port)
    assert not _record_path(home).exists()


def _is_process_group_alive(pid: int) -> bool:
    try:
        os.killpg(pid, 0)
    except ProcessLookupError:
        return False
    return True


async def _is_answering(web_port: int) -> bool:
    try:
        async with httpx2.AsyncClient(timeout=2.0) as http:
            await http.get(f"http://127.0.0.1:{web_port}/readyz")
    except httpx2.HTTPError:
        return False
    return True


async def test_exit_stops_the_dexcli_dev_the_command_started(private_ports_home: CliHome) -> None:
    child, started_pid = await _start_session(private_ports_home)
    assert started_pid is not None
    assert json.loads(_record_path(private_ports_home).read_text())["pid"] == started_pid
    child.sendline("remember the apple")
    await expect(child, "echo: remember the apple")
    await expect(child, "> ")
    child.sendline("/exit")
    session_id = await _read_session_id(child)
    await expect(child, _STOPPED_LINE)
    assert await wait_for_exit(child) == 0
    await _assert_stopped(private_ports_home, started_pid)

    # The next dexcli dev on these ports keeps the state, so the session resumes.
    resumed, resumed_started_pid = await _start_session(private_ports_home, "--resume", session_id)
    assert resumed_started_pid is not None
    resumed.sendline("history")
    await expect(resumed, "history: remember the apple")
    resumed.sendeof()
    await expect(resumed, _STOPPED_LINE)
    assert await wait_for_exit(resumed) == 0
    await _assert_stopped(private_ports_home, resumed_started_pid)


async def test_ctrl_c_twice_stops_the_dexcli_dev_the_command_started(
    private_ports_home: CliHome,
) -> None:
    child, started_pid = await _start_session(private_ports_home)
    assert started_pid is not None
    child.sendline("slow")
    await asyncio.sleep(2)
    child.sendintr()
    await expect(child, "[cancelling the turn; Ctrl-C again exits]")
    child.sendintr()
    await expect(child, _STOPPED_LINE)
    assert await wait_for_exit(child) == 0
    await _assert_stopped(private_ports_home, started_pid)


@pytest.mark.parametrize(
    "signal_number", [signal.SIGTERM, signal.SIGHUP], ids=["SIGTERM", "SIGHUP"]
)
async def test_a_termination_signal_stops_the_dexcli_dev_the_command_started(
    private_ports_home: CliHome, signal_number: signal.Signals
) -> None:
    child, started_pid = await _start_session(private_ports_home)
    assert started_pid is not None
    child.kill(signal_number)
    await expect(child, _STOPPED_LINE)
    assert await wait_for_exit(child) == 128 + signal_number
    await _assert_stopped(private_ports_home, started_pid)


async def test_sessions_subcommand_stops_the_dexcli_dev_it_started(
    private_ports_home: CliHome,
) -> None:
    completed = await asyncio.to_thread(private_ports_home.run, "sessions")
    assert completed.returncode == 0, completed.stdout + completed.stderr
    assert "No sessions yet." in completed.stdout
    assert _STOPPED_LINE in completed.stdout
    assert not await _is_answering(private_ports_home.web_port)
    assert not _record_path(private_ports_home).exists()


async def test_a_reused_dexcli_dev_keeps_running(
    cli_home: CliHome, shared_engine: PrivateDexEngine
) -> None:
    cli_home.write_config(config_yaml(_NO_CONNECTORS))
    child, started_pid = await _start_session(cli_home)
    assert started_pid is None
    child.sendline("/exit")
    assert await wait_for_exit(child) == 0
    assert _STOPPED_LINE not in (child.before or "")
    assert await _is_answering(shared_engine.web_port)


async def test_a_second_session_reuses_the_first_and_fails_short_when_it_exits(
    private_ports_home: CliHome,
) -> None:
    first, started_pid = await _start_session(private_ports_home)
    assert started_pid is not None
    second, second_started_pid = await _start_session(private_ports_home)
    assert second_started_pid is None
    first.sendline("/exit")
    await expect(first, _STOPPED_LINE)
    assert await wait_for_exit(first) == 0
    await _assert_stopped(private_ports_home, started_pid)
    second.sendline("hello")
    await expect(
        second,
        f"dex-agents-scripted: the dexcli dev at http://127.0.0.1:{private_ports_home.web_port} "
        "stopped. A dexcli dev that another command started stops when that command exits. "
        "To share one dexcli dev across sessions, run dexcli dev yourself first.",
        timeout=90,
    )
    assert await wait_for_exit(second) == 1
    assert "Traceback" not in (second.before or "")


_SIGTERM_IGNORING_DEXCLI = """#!/bin/sh
if [ "$1" != "dev" ]; then exec "$DEX_TEST_REAL_DEXCLI" "$@"; fi
trap 'touch "$DEX_TEST_TERM_MARKER"' TERM
"$DEX_TEST_REAL_DEXCLI" "$@" &
while :; do sleep 1; done
"""


async def test_a_signal_while_the_stop_waits_kills_the_dexcli_dev_at_once(
    private_ports_home: CliHome, tmp_path: Path
) -> None:
    # The wrapper records SIGTERM and keeps running, as a stuck dexcli dev would.
    wrapper = tmp_path / "dexcli-wrapper.sh"
    wrapper.write_text(_SIGTERM_IGNORING_DEXCLI)
    wrapper.chmod(0o755)
    term_marker = tmp_path / "dexcli-wrapper.terminated"
    private_ports_home.extra_environment.update(
        DEXCLI=str(wrapper),
        DEX_TEST_REAL_DEXCLI=os.environ[DEXCLI_ENVIRONMENT_VARIABLE],
        DEX_TEST_TERM_MARKER=str(term_marker),
    )
    child, started_pid = await _start_session(private_ports_home)
    assert started_pid is not None
    child.sendline("/exit")
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _STOP_SECONDS
    while not term_marker.exists():
        assert loop.time() < deadline, "the dexcli dev wrapper never received SIGTERM"
        await asyncio.sleep(0.1)
    signalled_at = loop.time()
    child.kill(signal.SIGTERM)
    assert await wait_for_exit(child) == 128 + signal.SIGTERM
    assert loop.time() - signalled_at < 10, "the command must not wait out the grace period"
    await _assert_stopped(private_ports_home, started_pid)


def test_termination_signal_setup_is_skipped_where_the_event_loop_has_none(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    probe_loop = asyncio.new_event_loop()
    loop_class = type(probe_loop)
    probe_loop.close()

    def unsupported(*arguments: object) -> None:
        raise NotImplementedError("as on Windows")

    removed: list[int] = []
    monkeypatch.setattr(loop_class, "add_signal_handler", unsupported)
    monkeypatch.setattr(
        loop_class, "remove_signal_handler", lambda _loop, number: removed.append(number)
    )
    server = _CommandDexServer(CommandEnvironment.from_environment({"HOME": str(tmp_path)}))

    async def command() -> int:
        return 7

    assert server.run(command()) == 7
    assert removed == []
