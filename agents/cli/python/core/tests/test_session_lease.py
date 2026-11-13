# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""One terminal per session: a second command waits for the session, then takes it over."""

from __future__ import annotations

import asyncio
import io
import json
import os
import signal
import socket
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo

import pexpect

from dex_agents_cli_core.session_lease import (
    DEFAULT_COLUMNS,
    FRAME_INTERVAL_SECONDS,
    REDRAW_PREFIX,
    RETRY_INTERVAL_SECONDS,
    SPINNER_FRAMES,
    build_animated_waiting_line,
    build_lease_lost_line,
    build_plain_waiting_line,
    build_took_over_line,
    format_lease_since,
)
from tests.conftest import CliHome, config_yaml, expect, wait_for_exit
from tests.fake_terminal_ui import FakeTerminalUi, chat_transcript, worker_host_pid
from tests.test_terminal_ui_handoff import pause, start_chat

AGENTS_DIR = Path(__file__).resolve().parents[4]
CONFORMANCE = json.loads(
    (AGENTS_DIR / "conformance" / "cli_session_lease.json").read_text(encoding="utf-8")
)
NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}
# Short enough that an expiry test takes seconds; the command renews every third of it.
TEST_TTL_SECONDS = 3
WAITING_TEXT = "is open in another terminal ("
# The chat's prompt line; a bare "> " also matches the help line's "<path> attaches".
PROMPT = "\r\n> "


def test_the_lines_match_the_conformance_file() -> None:
    assert list(SPINNER_FRAMES) == CONFORMANCE["spinner_frames"]
    assert FRAME_INTERVAL_SECONDS * 1000 == CONFORMANCE["frame_interval_ms"]
    assert RETRY_INTERVAL_SECONDS * 1000 == CONFORMANCE["retry_interval_ms"]
    assert REDRAW_PREFIX == CONFORMANCE["redraw_prefix"]
    assert DEFAULT_COLUMNS == CONFORMANCE["default_columns"]
    for case in CONFORMANCE["animated_waiting_cases"]:
        holder = f"{case['host_name']}, pid {case['process_id']}, since {case['since']}"
        line = build_animated_waiting_line(
            case["session_id"],
            holder,
            case["elapsed_seconds"],
            case["frame_index"],
            case["columns"],
        )
        assert line == case["line"], case["name"]
    for case in CONFORMANCE["plain_waiting_cases"]:
        holder = f"{case['host_name']}, pid {case['process_id']}, since {case['since']}"
        assert build_plain_waiting_line(case["session_id"], holder) == case["line"]
    for case in CONFORMANCE["took_over_cases"]:
        assert build_took_over_line(case["session_id"]) == case["line"]
    for case in CONFORMANCE["lease_lost_cases"]:
        holder = f"{case['host_name']}, pid {case['process_id']}, since {case['since']}"
        assert build_lease_lost_line(case["session_id"], holder) == case["line"]
    for case in CONFORMANCE["since_cases"]:
        since = format_lease_since(case["opened_at"], ZoneInfo(case["time_zone"]))
        assert since == case["since"], case["name"]


def lease_home(cli_home: CliHome, *, is_terminal_animated: bool = False) -> CliHome:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    cli_home.extra_environment["DEX_AGENTS_SESSION_LEASE_TTL_SECONDS"] = str(TEST_TTL_SECONDS)
    if is_terminal_animated:
        cli_home.extra_environment["TERM"] = "xterm-256color"
    return cli_home


async def read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


async def open_first_chat(cli_home: CliHome) -> tuple[pexpect.spawn, str]:
    first = cli_home.spawn("--plain")
    session_id = await read_session_id(first)
    await expect(first, PROMPT)
    return first, session_id


def holder_prefix(process_id: int) -> str:
    return f"{WAITING_TEXT}{socket.gethostname()}, pid {process_id}, since "


async def test_a_second_command_waits_and_takes_over_when_the_first_exits(
    cli_home: CliHome,
) -> None:
    lease_home(cli_home, is_terminal_animated=True)
    first, session_id = await open_first_chat(cli_home)

    second = cli_home.spawn("--plain", "--resume", session_id)
    await expect(second, f"Session {session_id} {holder_prefix(first.pid)}")
    await expect(second, "· waiting 0:01 · Ctrl-C to quit")
    await asyncio.sleep(1)
    first.sendline("/exit")
    assert await wait_for_exit(first) == 0
    await expect(second, f"\r\x1b[2KTook over session {session_id}.", timeout=TEST_TTL_SECONDS)
    assert await read_session_id(second) == session_id
    await expect(second, PROMPT)
    second.sendline("hello")
    await expect(second, "echo: hello")
    second.sendeof()
    assert await wait_for_exit(second) == 0
    assert "Traceback" not in str(second.before)


async def test_a_killed_first_command_frees_the_session_once_its_lease_expires(
    cli_home: CliHome,
) -> None:
    lease_home(cli_home)
    first, session_id = await open_first_chat(cli_home)
    second = cli_home.spawn("--plain", "--resume", session_id)
    await expect(second, holder_prefix(first.pid))

    os.kill(first.pid, signal.SIGKILL)
    await expect(second, f"Took over session {session_id}.", timeout=TEST_TTL_SECONDS + 5)
    await expect(second, PROMPT)
    second.sendline("hello")
    await expect(second, "echo: hello")
    second.sendeof()
    assert await wait_for_exit(second) == 0


async def test_ctrl_c_while_waiting_exits_130_without_a_traceback(cli_home: CliHome) -> None:
    lease_home(cli_home, is_terminal_animated=True)
    first, session_id = await open_first_chat(cli_home)
    second = cli_home.spawn("--plain", "--resume", session_id)
    await expect(second, "· Ctrl-C to quit")

    second.sendintr()
    assert await wait_for_exit(second) == 130
    assert "Traceback" not in str(second.before)
    first.sendline("still mine")
    await expect(first, "echo: still mine")
    first.sendeof()
    assert await wait_for_exit(first) == 0


async def test_without_a_terminal_the_wait_writes_one_line_and_one_line_when_it_ends(
    cli_home: CliHome,
) -> None:
    lease_home(cli_home)
    first, session_id = await open_first_chat(cli_home)

    async def exit_first_later() -> None:
        await asyncio.sleep(2.5)
        first.sendline("/exit")

    exiting = asyncio.create_task(exit_first_later())
    completed = await asyncio.to_thread(cli_home.run, "--resume", session_id, stdin="/exit\n")
    await exiting
    assert await wait_for_exit(first) == 0

    assert completed.returncode == 0, completed.stderr
    lines = completed.stderr.splitlines()
    waiting = [line for line in lines if WAITING_TEXT in line]
    assert waiting == [
        f"Session {session_id} is open in another terminal "
        f"({socket.gethostname()}, pid {first.pid}, since {waiting[0].split('since ')[1][:5]}) "
        "· waiting · Ctrl-C to quit"
    ]
    assert [line for line in lines if line.startswith("Took over")] == [
        f"Took over session {session_id}."
    ]
    assert "\x1b" not in completed.stderr
    assert "Traceback" not in completed.stderr


async def test_a_chat_whose_session_another_terminal_took_stops_with_one_line(
    cli_home: CliHome,
) -> None:
    lease_home(cli_home)
    first, session_id = await open_first_chat(cli_home)
    # Paused past its lease, as a suspended laptop would be.
    os.kill(first.pid, signal.SIGSTOP)
    second = cli_home.spawn("--plain", "--resume", session_id)
    try:
        # Its lease expired while it started, unless it still waits: either way it gets the session.
        assert await read_session_id(second) == session_id
        await expect(second, PROMPT)
    finally:
        os.kill(first.pid, signal.SIGCONT)

    await expect(
        first,
        f"Session {session_id} is now open in another terminal "
        f"({socket.gethostname()}, pid {second.pid}, since ",
        timeout=TEST_TTL_SECONDS + 10,
    )
    await expect(first, "this chat stopped.")
    assert await wait_for_exit(first) == 1
    assert "Traceback" not in str(first.before)
    second.sendline("hello")
    await expect(second, "echo: hello")
    second.sendeof()
    assert await wait_for_exit(second) == 0


SWITCH_MESSAGE: dict[str, Any] = {"type": "switch_session", "is_resumed": True}


class SwitchingTerminalUi:
    """A fake dexcli that hands a switch to one session back, then shows that session."""

    def __init__(self, directory: Path, switch_to: str) -> None:
        switching = chat_transcript(
            [{"from": "tui", "message": {**SWITCH_MESSAGE, "session_id": switch_to}}]
        )
        self.first = FakeTerminalUi(directory / "first", switching)
        resumed = chat_transcript([])
        resumed["steps"][0]["message"]["session"]["is_resumed"] = True
        self.second = FakeTerminalUi(directory / "second", resumed)
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


async def test_the_terminal_ui_waits_before_the_handoff_and_takes_over(
    cli_home: CliHome, tmp_path: Path
) -> None:
    lease_home(cli_home)
    first, session_id = await open_first_chat(cli_home)
    resumed = chat_transcript([pause(300_000)])
    resumed["steps"][0]["message"]["session"]["is_resumed"] = True
    fake = FakeTerminalUi(tmp_path / "tui", resumed)
    second = start_chat(cli_home, fake, io.StringIO(), NO_CONNECTORS, "--resume", session_id)

    await expect(second, f"Session {session_id} {holder_prefix(first.pid)}")
    await expect(second, "· Ctrl-C to quit")
    assert not fake.was_run()
    first.sendline("/exit")
    assert await wait_for_exit(first) == 0
    await expect(second, f"\r\x1b[2KTook over session {session_id}.", timeout=TEST_TTL_SECONDS)
    await fake.wait_for("sent", type="ready")
    second.kill(signal.SIGTERM)
    assert await wait_for_exit(second) == 143
    assert fake.mismatches() == []


async def test_a_terminal_ui_whose_session_another_terminal_took_stops_with_one_line(
    cli_home: CliHome, tmp_path: Path
) -> None:
    lease_home(cli_home)
    screen = io.StringIO()
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(300_000)]))
    first = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(first)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    os.kill(host_pid, signal.SIGSTOP)
    try:
        second = cli_home.spawn("--plain", "--resume", session_id)
        assert await read_session_id(second) == session_id
        await expect(second, PROMPT)
    finally:
        os.kill(host_pid, signal.SIGCONT)

    await expect(
        first,
        f"Session {session_id} is now open in another terminal "
        f"({socket.gethostname()}, pid {second.pid}, since ",
        timeout=TEST_TTL_SECONDS + 10,
    )
    assert await wait_for_exit(first) == 1
    assert "Traceback" not in screen.getvalue()
    second.sendeof()
    assert await wait_for_exit(second) == 0


async def test_a_session_switch_in_the_terminal_ui_waits_for_the_session_and_relaunches(
    cli_home: CliHome, tmp_path: Path
) -> None:
    lease_home(cli_home)
    other, other_session_id = await open_first_chat(cli_home)
    fake = SwitchingTerminalUi(tmp_path / "tui", other_session_id)
    cli_home.extra_environment.update(fake.environment())
    switching = cli_home.spawn()
    first_session_id = await read_session_id(switching)
    assert first_session_id != other_session_id

    await expect(switching, f"Session {other_session_id} {holder_prefix(other.pid)}")
    other.sendline("/exit")
    assert await wait_for_exit(other) == 0
    await expect(switching, f"Took over session {other_session_id}.", timeout=TEST_TTL_SECONDS)
    assert await read_session_id(switching) == other_session_id
    assert await wait_for_exit(switching) == 0
    assert fake.first.mismatches() == []
    assert fake.second.mismatches() == []
    assert fake.second.was_run()
    # The first session's lease was released, so a plain chat opens it at once.
    reopened = cli_home.spawn("--plain", "--resume", first_session_id)
    assert await read_session_id(reopened) == first_session_id
    await expect(reopened, PROMPT)
    assert WAITING_TEXT not in str(reopened.before)
    reopened.sendeof()
    assert await wait_for_exit(reopened) == 0
