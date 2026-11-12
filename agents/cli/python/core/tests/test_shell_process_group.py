# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The shell tool in the plain chat: what a command leaves behind stops when it ends."""

from __future__ import annotations

import asyncio
import os
import shlex
import signal
import sys
import time
from pathlib import Path

import pytest

from dex_agents import ToolExecutionError
from dex_agents_cli_core.local_tools.shell import run_shell_command


def is_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


async def wait_until_gone(pid: int, timeout: float = 5.0) -> None:
    deadline = time.monotonic() + timeout
    while is_alive(pid):
        if time.monotonic() > deadline:
            raise AssertionError(f"process {pid} still runs")
        await asyncio.sleep(0.05)


def stdout_lines(output: str) -> list[str]:
    _, _, stdout = output.partition("stdout:\n")
    stdout, _, _ = stdout.partition("\nstderr:\n")
    return stdout.splitlines()


async def test_a_background_process_stops_when_the_command_ends(tmp_path: Path) -> None:
    started = time.monotonic()
    output = await run_shell_command("sleep 30 & echo $!", tmp_path, 10)
    assert time.monotonic() - started < 2
    assert output.startswith("exit code 0\n")
    (pid,) = stdout_lines(output)
    await wait_until_gone(int(pid))


async def test_a_child_holding_stdout_does_not_keep_the_command_running(tmp_path: Path) -> None:
    started = time.monotonic()
    output = await run_shell_command("(sleep 30; echo late) & echo started", tmp_path, 10)
    assert time.monotonic() - started < 2
    assert stdout_lines(output) == ["started"]


async def test_a_process_that_left_the_group_holds_up_the_result_at_most_a_second(
    tmp_path: Path,
) -> None:
    leaver = "import os, time; os.setsid(); print(os.getpid(), flush=True); time.sleep(30)"
    command = f"{shlex.quote(sys.executable)} -c {shlex.quote(leaver)} & sleep 0.5"
    started = time.monotonic()
    output = await run_shell_command(command, tmp_path, 10)
    assert time.monotonic() - started < 3
    (pid,) = stdout_lines(output)
    try:
        # It left the command's process group, so the tool does not stop it.
        assert is_alive(int(pid))
    finally:
        os.kill(int(pid), signal.SIGKILL)


async def test_a_timeout_kills_the_whole_group(tmp_path: Path) -> None:
    pid_file = tmp_path / "background.pid"
    with pytest.raises(ToolExecutionError, match="longer than 1 seconds"):
        await run_shell_command(f"sleep 30 & echo $! > {pid_file}; sleep 30", tmp_path, 1)
    await wait_until_gone(int(pid_file.read_text()))


async def test_an_explicit_exit_code_is_the_shells_number(tmp_path: Path) -> None:
    output = await run_shell_command("echo bye; exit 143", tmp_path, 10)
    assert output == "exit code 143\nstdout:\nbye\n"
