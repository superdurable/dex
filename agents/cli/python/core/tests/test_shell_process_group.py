# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The shell tool: what a command leaves behind stops when it ends, and its result's form.

The expected strings are the TypeScript CLI core's, so both CLIs show a model
the same result for the same command.
"""

from __future__ import annotations

import asyncio
import os
import shlex
import signal
import subprocess
import sys
import threading
import time
from collections.abc import Callable
from pathlib import Path
from typing import Any

import pytest

from dex_agents_cli_core.local_tools import shell
from dex_agents_cli_core.local_tools.shell import run_shell_command

# A generous bound: each case would otherwise wait for a 30 s job.
NOT_WAITING_SECONDS = 15
IGNORES_SIGTERM = "sh -c 'trap \"\" TERM; sleep 30'"


def is_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


async def wait_until_gone(pid: int, timeout: float = 10.0) -> None:
    deadline = time.monotonic() + timeout
    while is_alive(pid):
        if time.monotonic() > deadline:
            raise AssertionError(f"process {pid} still runs")
        await asyncio.sleep(0.05)


def stdout_lines(output: str) -> list[str]:
    _, _, rest = output.partition("\n--- stdout ---\n")
    stdout, _, _ = rest.partition("\n--- stderr ---\n")
    return stdout.splitlines()


async def test_a_background_process_stops_when_the_command_ends(tmp_path: Path) -> None:
    started = time.monotonic()
    output = await run_shell_command("sleep 30 & echo $!", tmp_path, 60)
    assert time.monotonic() - started < NOT_WAITING_SECONDS
    assert output.startswith("exit code 0\n--- stdout ---\n")
    (pid,) = stdout_lines(output)
    await wait_until_gone(int(pid))


async def test_a_child_holding_stdout_does_not_keep_the_command_running(tmp_path: Path) -> None:
    started = time.monotonic()
    output = await run_shell_command("(sleep 30; echo late) & echo started", tmp_path, 60)
    assert time.monotonic() - started < NOT_WAITING_SECONDS
    assert output == "exit code 0\n--- stdout ---\nstarted\n\n--- stderr ---\n"


async def test_a_process_that_left_the_group_does_not_hold_up_the_result(
    tmp_path: Path,
) -> None:
    leaver = "import os, time; os.setsid(); print(os.getpid(), flush=True); time.sleep(30)"
    command = f"{shlex.quote(sys.executable)} -c {shlex.quote(leaver)} & sleep 0.5"
    started = time.monotonic()
    output = await run_shell_command(command, tmp_path, 60)
    assert time.monotonic() - started < NOT_WAITING_SECONDS
    (pid,) = stdout_lines(output)
    try:
        # It left the command's process group, so the tool does not stop it.
        assert is_alive(int(pid))
    finally:
        os.kill(int(pid), signal.SIGKILL)


async def test_a_timeout_kills_the_whole_group_and_is_a_result(tmp_path: Path) -> None:
    pid_file = tmp_path / "background.pid"
    output = await run_shell_command(
        f"sleep 30 & echo $! > {pid_file}; echo before; sleep 30", tmp_path, 1
    )
    assert output == "killed: timed out after 1 seconds\n--- stdout ---\nbefore\n\n--- stderr ---\n"
    await wait_until_gone(int(pid_file.read_text()))


@pytest.mark.parametrize(
    ("command", "expected"),
    [
        ("echo bye; exit 143", "exit code 143\n--- stdout ---\nbye\n\n--- stderr ---\n"),
        ("echo oops >&2; kill -TERM $$", "exit code 143\n--- stdout ---\n\n--- stderr ---\noops\n"),
        ("exit 3", "exit code 3\n--- stdout ---\n\n--- stderr ---\n"),
    ],
    ids=["explicit-143", "killed-by-sigterm", "explicit-3"],
)
async def test_the_status_is_the_number_the_shell_reports(
    tmp_path: Path, command: str, expected: str
) -> None:
    assert await run_shell_command(command, tmp_path, 60) == expected


async def test_each_stream_keeps_at_most_64_kib(tmp_path: Path) -> None:
    output = await run_shell_command(
        "head -c 2000000 /dev/zero | tr '\\0' a; echo short >&2", tmp_path, 60
    )
    assert output == (
        "exit code 0\n--- stdout ---\n"
        + "a" * 65536
        + "\n[cut at 65536 bytes]\n--- stderr ---\nshort\n"
    )


class RecordingGroups:
    """A group like the Worker host's registry: a lock guards each signal and the reap."""

    def __init__(self) -> None:
        self.sigterm_sent = threading.Event()
        self.released: list[int] = []
        self.signals_after_release: list[int] = []
        self.reaped_under_lock = False
        self.signal_times: list[tuple[int, float]] = []
        self._lock = threading.Lock()

    def register(self, pid: int) -> None:
        pass

    def signal_group(self, pid: int, signal_number: int) -> bool:
        with self._lock:
            self.signal_times.append((signal_number, time.monotonic()))
            if pid in self.released:
                self.signals_after_release.append(signal_number)
                return False
            if signal_number == signal.SIGTERM:
                self.sigterm_sent.set()
            try:
                os.killpg(pid, signal_number)
            except (ProcessLookupError, PermissionError):
                return False
            return True

    def release(self, pid: int, reap: Callable[[], object]) -> None:
        with self._lock:
            reap()
            self.reaped_under_lock = True
            self.released.append(pid)


@pytest.fixture
def spawned(monkeypatch: pytest.MonkeyPatch) -> list[subprocess.Popen[bytes]]:
    processes: list[subprocess.Popen[bytes]] = []
    real_popen = subprocess.Popen

    def recording_popen(*arguments: Any, **options: Any) -> subprocess.Popen[bytes]:
        process: subprocess.Popen[bytes] = real_popen(*arguments, **options)
        if list(arguments[0])[:2] == ["/bin/sh", "-c"]:
            processes.append(process)
        return process

    # The shell tool looks Popen up on the module at each call.
    monkeypatch.setattr(subprocess, "Popen", recording_popen)
    return processes


def assert_cleaned(
    process: subprocess.Popen[bytes], groups: RecordingGroups, background_pid: int | None
) -> None:
    assert groups.released == [process.pid]
    assert groups.reaped_under_lock
    assert groups.signals_after_release == []
    assert process.returncode is not None
    assert process.stdout is not None and process.stdout.closed
    assert process.stderr is not None and process.stderr.closed
    with pytest.raises(ChildProcessError):
        os.waitpid(process.pid, os.WNOHANG)
    if background_pid is not None:
        assert not is_alive(background_pid)


async def test_a_cancel_in_the_sigterm_grace_still_cleans_up(
    tmp_path: Path, spawned: list[subprocess.Popen[bytes]]
) -> None:
    pid_file = tmp_path / "background.pid"
    groups = RecordingGroups()
    call = asyncio.create_task(
        run_shell_command(
            f"{IGNORES_SIGTERM} & echo $! > {pid_file}", tmp_path, 60, process_groups=groups
        )
    )
    # The shell exited and the group got SIGTERM, which the background process ignores.
    assert await asyncio.to_thread(groups.sigterm_sent.wait, 10)
    call.cancel()
    with pytest.raises(asyncio.CancelledError):
        await call
    (process,) = spawned
    assert_cleaned(process, groups, int(pid_file.read_text()))


async def test_repeated_cancels_while_the_command_runs_still_clean_up(
    tmp_path: Path, spawned: list[subprocess.Popen[bytes]]
) -> None:
    groups = RecordingGroups()
    call = asyncio.create_task(
        run_shell_command("trap '' TERM; sleep 30", tmp_path, 60, process_groups=groups)
    )
    while not spawned:
        await asyncio.sleep(0.01)
    call.cancel()
    await asyncio.sleep(0.05)
    call.cancel()
    with pytest.raises(asyncio.CancelledError):
        await call
    (process,) = spawned
    assert_cleaned(process, groups, None)


def run_in_own_loop(body: Callable[[asyncio.AbstractEventLoop], None]) -> None:
    loop = asyncio.new_event_loop()
    try:
        body(loop)
    finally:
        loop.close()


async def wait_for_release(groups: RecordingGroups, timeout: float = 10.0) -> None:
    deadline = time.monotonic() + timeout
    while not groups.released:
        if time.monotonic() > deadline:
            raise AssertionError("the shell was never released")
        await asyncio.sleep(0.05)


async def test_a_call_cancelled_before_its_first_await_still_cleans_up(
    tmp_path: Path, spawned: list[subprocess.Popen[bytes]]
) -> None:
    pid_file = tmp_path / "background.pid"
    groups = RecordingGroups()

    def cancel_at_once(loop: asyncio.AbstractEventLoop) -> None:
        call = loop.create_task(
            run_shell_command(
                f"{IGNORES_SIGTERM} & echo $! > {pid_file}; sleep 30",
                tmp_path,
                60,
                process_groups=groups,
            )
        )
        # One step runs the call up to its first await, right after the spawn; then cancel it.
        loop.call_soon(call.cancel)
        with pytest.raises(asyncio.CancelledError):
            loop.run_until_complete(call)

    await asyncio.to_thread(run_in_own_loop, cancel_at_once)
    (process,) = spawned
    assert_cleaned(process, groups, int(pid_file.read_text()) if pid_file.exists() else None)


async def test_a_closed_event_loop_still_gets_the_shell_cleaned(
    tmp_path: Path, spawned: list[subprocess.Popen[bytes]]
) -> None:
    pid_file = tmp_path / "background.pid"
    groups = RecordingGroups()

    def close_while_running(loop: asyncio.AbstractEventLoop) -> None:
        loop.create_task(
            run_shell_command(
                f"{IGNORES_SIGTERM} & echo $! > {pid_file}; sleep 30",
                tmp_path,
                2,
                process_groups=groups,
            )
        )
        loop.run_until_complete(asyncio.sleep(0.5))
        # The loop closes with the call still pending: no cancel ever reaches it.

    await asyncio.to_thread(run_in_own_loop, close_while_running)
    # The owner thread enforces the timeout itself and cleans up without the loop.
    await wait_for_release(groups)
    (process,) = spawned
    assert_cleaned(process, groups, int(pid_file.read_text()))


async def test_the_output_waits_the_group_cleanup_and_then_one_second(tmp_path: Path) -> None:
    leaver = "import os, time; os.setsid(); print(os.getpid(), flush=True); time.sleep(30)"
    command = (
        f"{shlex.quote(sys.executable)} -c {shlex.quote(leaver)} & {IGNORES_SIGTERM} & sleep 0.3"
    )
    started = time.monotonic()
    output = await run_shell_command(command, tmp_path, 60)
    elapsed = time.monotonic() - started
    (pid,) = stdout_lines(output)
    os.kill(int(pid), signal.SIGKILL)
    # The SIGTERM grace (0.5 s) passes in full, then the 1 s output wait does.
    assert 0.3 + 0.5 + 1.0 <= elapsed < NOT_WAITING_SECONDS


async def test_pipes_with_high_descriptor_numbers_still_stop_the_group(tmp_path: Path) -> None:
    # Over 1024 open descriptors, so the pipes get numbers select() cannot take.
    held = [os.open(os.devnull, os.O_RDONLY) for _ in range(1100)]
    try:
        output = await run_shell_command("sleep 30 & echo $!", tmp_path, 60)
    finally:
        for fd in held:
            os.close(fd)
    assert output.startswith("exit code 0\n--- stdout ---\n")
    (pid,) = stdout_lines(output)
    await wait_until_gone(int(pid))


async def test_a_failed_read_still_stops_and_reaps_the_group(
    tmp_path: Path, spawned: list[subprocess.Popen[bytes]], monkeypatch: pytest.MonkeyPatch
) -> None:
    def failing_read(self: Any) -> None:
        raise RuntimeError("injected read failure")

    monkeypatch.setattr(shell._CappedOutput, "read_available", failing_read)
    pid_file = tmp_path / "background.pid"
    groups = RecordingGroups()
    output = await run_shell_command(
        f"sleep 30 & echo $! > {pid_file}; sleep 0.2", tmp_path, 60, process_groups=groups
    )
    assert output.startswith("exit code 0\n")
    (process,) = spawned
    assert_cleaned(process, groups, int(pid_file.read_text()))


async def test_the_sigterm_grace_lasts_even_when_the_pipes_ended(tmp_path: Path) -> None:
    groups = RecordingGroups()
    await run_shell_command(
        f"{IGNORES_SIGTERM} >/dev/null 2>&1 &", tmp_path, 60, process_groups=groups
    )
    sent = [signal_number for signal_number, _ in groups.signal_times]
    assert sent == [signal.SIGTERM, signal.SIGKILL]
    (_, sigterm_at), (_, sigkill_at) = groups.signal_times
    assert sigkill_at - sigterm_at >= 0.45


@pytest.mark.parametrize("failing_thread", ["-exit", ""], ids=["exit-thread", "owner-thread"])
async def test_an_owner_that_cannot_start_leaves_nothing_behind(
    tmp_path: Path,
    spawned: list[subprocess.Popen[bytes]],
    monkeypatch: pytest.MonkeyPatch,
    failing_thread: str,
) -> None:
    real_start = threading.Thread.start

    def failing_start(thread: threading.Thread) -> None:
        name = thread.name
        if name.startswith("dex-agents-shell-") and name.endswith("-exit") == bool(failing_thread):
            raise RuntimeError("can't start new thread")
        real_start(thread)

    monkeypatch.setattr(threading.Thread, "start", failing_start)
    groups = RecordingGroups()
    with pytest.raises(RuntimeError, match="can't start new thread"):
        await run_shell_command("sleep 30", tmp_path, 60, process_groups=groups)
    monkeypatch.setattr(threading.Thread, "start", real_start)
    (process,) = spawned
    assert_cleaned(process, groups, None)
    assert shell._OWNERS == set()


@pytest.mark.parametrize("arguments", [[], ["--hold-lock"]], ids=["active-shell", "lock-held"])
def test_a_forked_child_exits_promptly(arguments: list[str]) -> None:
    completed = subprocess.run(
        [sys.executable, "-m", "tests.fork_with_shell_owner", *arguments],
        cwd=Path(__file__).resolve().parents[1],
        capture_output=True,
        text=True,
        timeout=60,
        check=True,
    )
    seconds = float(completed.stdout.split()[3])
    assert seconds < 1.5, completed.stdout


async def test_a_command_that_leaves_nothing_behind_is_not_held_up(tmp_path: Path) -> None:
    groups = RecordingGroups()
    started = time.monotonic()
    output = await run_shell_command("echo hi", tmp_path, 60, process_groups=groups)
    assert time.monotonic() - started < 0.3
    assert output == "exit code 0\n--- stdout ---\nhi\n\n--- stderr ---\n"
    # Nothing was left in the group, so nothing was signaled.
    assert groups.signal_times == []
