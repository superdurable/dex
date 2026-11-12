# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The shell tool: one command in the workspace."""

from __future__ import annotations

import asyncio
import contextlib
import os
import signal
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import IO, Any, Protocol

from dex_agents import ToolExecutionError
from dex_agents_cli_core.process_exit import wait_until_exited

#: Seconds a command may run when the agent sets no timeout.
DEFAULT_SHELL_TIMEOUT_SECONDS = 120
#: The longest timeout the agent may set.
MAX_SHELL_TIMEOUT_SECONDS = 600
_MAX_OUTPUT_CHARACTERS = 50_000
# How long background processes a finished shell left may take to end after SIGTERM.
_LEFT_BEHIND_GRACE_SECONDS = 0.5
# How long after the shell exits its output may still arrive.
_OUTPUT_AFTER_EXIT_SECONDS = 1.0
_READ_BYTES = 65536


class ShellProcessGroups(Protocol):
    """Where a shell command registers its process group while its leader is unreaped.

    .. versionadded:: 0.6.0
    """

    def register(self, pid: int) -> None:
        """Register the leader of a new process group right after its spawn."""
        ...

    def signal_group(self, pid: int, signal_number: int) -> bool:
        """Signal a registered group; ``False`` when its leader was already released."""
        ...

    def release(self, pid: int, reap: Callable[[], object]) -> None:
        """Reap the leader with ``reap`` and unregister its group, so no signal comes between."""
        ...


async def run_shell_command(
    command: str,
    workspace: Path,
    timeout_seconds: int,
    *,
    process_groups: ShellProcessGroups | None = None,
) -> str:
    """Run one command with ``/bin/sh -c`` in the workspace and report its output.

    The command gets its own process group, so the terminal's Ctrl-C never
    reaches it, and a timeout kills the whole group. When the shell exits, the
    processes it left in its group, such as ``sleep 30 &``, get SIGTERM, then
    SIGKILL 0.5 s later; only then is the shell reaped. The output is what the
    pipes held 1 s after the shell exited, at the latest: a process that left
    the group (``setsid``) and still holds them is not stopped and does not
    hold up the result.

    Args:
        command: The command text.
        workspace: The working directory.
        timeout_seconds: Seconds before the command is killed.
        process_groups: Where to register the command's process group, as the
            terminal UI's Worker host does, or ``None`` to keep it here.

    .. versionchanged:: 0.6.0
        Added ``process_groups``. The processes a command leaves in its group
        stop when it ends, and a process holding its output after it ends no
        longer delays the result by more than 1 s.

    Returns:
        The exit code (the shell's own number, or the negative signal number
        when a signal killed it), standard output, and standard error.

    Raises:
        ToolExecutionError: If the command times out or cannot start.
    """
    groups = process_groups if process_groups is not None else _OwnedProcessGroups()
    try:
        process = subprocess.Popen(
            ["/bin/sh", "-c", command],
            cwd=workspace,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            start_new_session=True,
        )
    except OSError as error:
        raise ToolExecutionError(f"cannot start the command: {error.strerror}") from error
    # Synchronous, so no cancellation can come between the spawn and the registration.
    try:
        groups.register(process.pid)
    except BaseException:
        _kill_and_reap(process)
        raise
    assert process.stdout is not None and process.stderr is not None
    loop = asyncio.get_running_loop()
    outputs = (_PipeOutput(loop, process.stdout), _PipeOutput(loop, process.stderr))
    output = asyncio.ensure_future(asyncio.gather(*(pipe.done for pipe in outputs)))
    # The shell is never reaped before its group is cleaned, so its pid, which
    # is the group's ID, cannot be reused while anything may signal it.
    exited = asyncio.ensure_future(asyncio.to_thread(wait_until_exited, process.pid))
    interruption: BaseException | None = None
    try:
        await asyncio.wait_for(asyncio.shield(exited), timeout_seconds)
    except (TimeoutError, asyncio.CancelledError) as stopped:
        interruption = stopped
        groups.signal_group(process.pid, signal.SIGKILL)
    finally:
        # The shell is a zombie now, so its group ID stays reserved while the group is cleaned.
        await asyncio.shield(exited)
        output_deadline = loop.time() + _OUTPUT_AFTER_EXIT_SECONDS
        await _stop_group_left_behind(process.pid, output, groups)
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(asyncio.shield(output), max(0.0, output_deadline - loop.time()))
        for pipe in outputs:
            pipe.stop()
        process.stdout.close()
        process.stderr.close()
        returncode = _release(process, groups)
    if isinstance(interruption, asyncio.CancelledError):
        raise interruption
    if interruption is not None:
        raise ToolExecutionError(
            f"the command ran longer than {timeout_seconds} seconds"
        ) from interruption
    return _report(returncode, outputs[0].data(), outputs[1].data())


class _OwnedProcessGroups:
    """The process group of one command that nothing outside this call signals."""

    def __init__(self) -> None:
        self._released: set[int] = set()

    def register(self, pid: int) -> None:
        """Nothing to record: the group lives only as long as this call."""

    def signal_group(self, pid: int, signal_number: int) -> bool:
        """Signal the group while its leader is unreaped."""
        if pid in self._released:
            return False
        try:
            os.killpg(pid, signal_number)
        except (ProcessLookupError, PermissionError):
            return False
        return True

    def release(self, pid: int, reap: Callable[[], object]) -> None:
        """Reap the leader; no signal follows."""
        reap()
        self._released.add(pid)


class _PipeOutput:
    """Collect one pipe on the event loop until EOF, or until ``stop``."""

    def __init__(self, loop: asyncio.AbstractEventLoop, pipe: IO[bytes]) -> None:
        self._loop = loop
        self._fd = pipe.fileno()
        self._chunks: list[bytes] = []
        self.done: asyncio.Future[None] = loop.create_future()
        os.set_blocking(self._fd, False)
        loop.add_reader(self._fd, self._read)

    def data(self) -> bytes:
        """Return what the pipe held so far."""
        return b"".join(self._chunks)

    def stop(self) -> None:
        """Stop reading, whether or not the pipe reached EOF."""
        if not self.done.done():
            self._loop.remove_reader(self._fd)
            self.done.set_result(None)

    def _read(self) -> None:
        try:
            chunk = os.read(self._fd, _READ_BYTES)
        except BlockingIOError:
            return
        except OSError:
            chunk = b""
        if chunk:
            self._chunks.append(chunk)
        else:
            self.stop()


async def _stop_group_left_behind(
    pid: int, output: asyncio.Future[Any], process_groups: ShellProcessGroups
) -> None:
    """End the background processes a finished shell left in its group."""
    process_groups.signal_group(pid, signal.SIGTERM)
    with contextlib.suppress(TimeoutError):
        await asyncio.wait_for(asyncio.shield(output), _LEFT_BEHIND_GRACE_SECONDS)
    process_groups.signal_group(pid, signal.SIGKILL)


def _kill_and_reap(process: subprocess.Popen[bytes]) -> None:
    """Kill and reap a shell whose registration failed; nothing else can signal it."""
    with contextlib.suppress(ProcessLookupError, PermissionError):
        os.killpg(process.pid, signal.SIGKILL)
    _, status = os.waitpid(process.pid, 0)
    process.returncode = os.waitstatus_to_exitcode(status)
    for pipe in (process.stdout, process.stderr):
        if pipe is not None:
            pipe.close()


def _release(process: subprocess.Popen[bytes], process_groups: ShellProcessGroups) -> int:
    statuses: list[int] = []

    def reap() -> None:
        _, status = os.waitpid(process.pid, 0)
        statuses.append(os.waitstatus_to_exitcode(status))

    process_groups.release(process.pid, reap)
    process.returncode = statuses[0]
    return statuses[0]


def _report(returncode: int | None, stdout: bytes, stderr: bytes) -> str:
    sections = [f"exit code {returncode}"]
    if stdout:
        sections.append("stdout:\n" + _truncate(stdout.decode("utf-8", errors="replace")))
    if stderr:
        sections.append("stderr:\n" + _truncate(stderr.decode("utf-8", errors="replace")))
    return "\n".join(sections)


def _truncate(text: str) -> str:
    if len(text) <= _MAX_OUTPUT_CHARACTERS:
        return text
    return f"{text[:_MAX_OUTPUT_CHARACTERS]}\n... ({len(text) - _MAX_OUTPUT_CHARACTERS} more)"
