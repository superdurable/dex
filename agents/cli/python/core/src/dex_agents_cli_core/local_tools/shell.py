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
from collections.abc import Callable, Coroutine
from pathlib import Path
from typing import IO, Any, Protocol

from dex_agents import ToolExecutionError
from dex_agents_cli_core.process_exit import wait_until_exited

#: Seconds a command may run when the agent sets no timeout.
DEFAULT_SHELL_TIMEOUT_SECONDS = 120
#: The longest timeout the agent may set.
MAX_SHELL_TIMEOUT_SECONDS = 600
_MAX_OUTPUT_BYTES = 64 * 1024
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
    """Run one command with ``/bin/sh -c`` in the workspace and report its status and output.

    The command gets its own process group, so the terminal's Ctrl-C never
    reaches it. When the shell exits, the processes it left in its group, such
    as ``sleep 30 &``, get SIGTERM, then SIGKILL 0.5 s later; only then is the
    shell reaped. A timeout kills the whole group at once. The output is what
    the pipes carried until 1 s after the shell exited, at the latest: a
    process that left the group (``setsid``) and still holds them is not
    stopped and does not hold up the result. A cancelled call still cleans the
    group, reaps the shell, and closes the pipes before the cancellation
    propagates.

    Args:
        command: The command text.
        workspace: The working directory.
        timeout_seconds: Seconds before the command is killed.
        process_groups: Where to register the command's process group, as the
            terminal UI's Worker host does, or ``None`` to keep it here.

    .. versionchanged:: 0.6.0
        Added ``process_groups``. The processes a command leaves in its group
        stop when it ends. The result has the same form as in the TypeScript
        CLI core: a timeout is a result, ``killed: timed out after N
        seconds``, a shell a signal ended reports ``exit code 128+N``, and
        both streams always appear, each cut at 64 KiB.

    Returns:
        ``exit code N`` (the shell's status) or ``killed: timed out after N
        seconds``, then ``--- stdout ---`` and ``--- stderr ---`` with each
        stream's text, cut at 64 KiB with a ``[cut at 65536 bytes]`` line.

    Raises:
        ToolExecutionError: If the shell cannot start.
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
        raise ToolExecutionError(f"the shell could not start: {error.strerror}") from error
    # Synchronous, so no cancellation can come between the spawn and the registration.
    try:
        groups.register(process.pid)
    except BaseException:
        _kill_and_reap(process)
        raise
    run = _ShellRun(process, groups)
    end_reason: str | None = None
    try:
        await asyncio.wait_for(asyncio.shield(run.exited), timeout_seconds)
    except TimeoutError:
        end_reason = f"timed out after {timeout_seconds} seconds"
        groups.signal_group(process.pid, signal.SIGKILL)
    except asyncio.CancelledError:
        groups.signal_group(process.pid, signal.SIGKILL)
        await _complete_despite_cancellation(run.clean_up())
        raise
    returncode = await _complete_despite_cancellation(run.clean_up())
    status = f"killed: {end_reason}" if end_reason is not None else f"exit code {returncode}"
    return f"{status}\n--- stdout ---\n{run.stdout.text()}\n--- stderr ---\n{run.stderr.text()}"


class _ShellRun:
    """One running shell: its output, its exit, and the cleanup that must always finish."""

    def __init__(self, process: subprocess.Popen[bytes], groups: ShellProcessGroups) -> None:
        assert process.stdout is not None and process.stderr is not None
        loop = asyncio.get_running_loop()
        self._loop = loop
        self._process = process
        self._groups = groups
        self.stdout = _PipeOutput(loop, process.stdout)
        self.stderr = _PipeOutput(loop, process.stderr)
        # The shell is never reaped before its group is cleaned, so its pid, which
        # is the group's ID, cannot be reused while anything may signal it.
        self.exited = asyncio.ensure_future(asyncio.to_thread(wait_until_exited, process.pid))

    async def clean_up(self) -> int:
        """Stop the group, collect the output, close the pipes, then reap; return the status."""
        process = self._process
        try:
            await self.exited
            output_deadline = self._loop.time() + _OUTPUT_AFTER_EXIT_SECONDS
            output = asyncio.gather(self.stdout.done, self.stderr.done)
            # The shell is a zombie now, so its group ID stays reserved while the group is cleaned.
            self._groups.signal_group(process.pid, signal.SIGTERM)
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(asyncio.shield(output), _LEFT_BEHIND_GRACE_SECONDS)
            self._groups.signal_group(process.pid, signal.SIGKILL)
            with contextlib.suppress(TimeoutError):
                remaining = max(0.0, output_deadline - self._loop.time())
                await asyncio.wait_for(asyncio.shield(output), remaining)
        finally:
            self.stdout.stop()
            self.stderr.stop()
            for pipe in (process.stdout, process.stderr):
                if pipe is not None:
                    pipe.close()
            returncode = _release(process, self._groups)
        return returncode


async def _complete_despite_cancellation(cleanup: Coroutine[Any, Any, int]) -> int:
    """Run a cleanup to its end even when the caller is cancelled; then re-raise that."""
    task = asyncio.ensure_future(cleanup)
    is_cancelled = False
    while not task.done():
        try:
            await asyncio.shield(task)
        except asyncio.CancelledError:
            is_cancelled = True
    if is_cancelled:
        task.result()
        raise asyncio.CancelledError
    return task.result()


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
    """Read one pipe on the event loop until EOF or ``stop``, keeping at most 64 KiB of it."""

    def __init__(self, loop: asyncio.AbstractEventLoop, pipe: IO[bytes]) -> None:
        self._loop = loop
        self._fd = pipe.fileno()
        self._chunks: list[bytes] = []
        self._kept_bytes = 0
        self._is_cut = False
        self.done: asyncio.Future[None] = loop.create_future()
        os.set_blocking(self._fd, False)
        loop.add_reader(self._fd, self._read)

    def text(self) -> str:
        """Return the kept output as text, with a line that says when it was cut."""
        text = b"".join(self._chunks).decode("utf-8", errors="replace")
        return f"{text}\n[cut at {_MAX_OUTPUT_BYTES} bytes]" if self._is_cut else text

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
        if not chunk:
            self.stop()
            return
        room = _MAX_OUTPUT_BYTES - self._kept_bytes
        if len(chunk) > room:
            self._is_cut = True
            chunk = chunk[: max(0, room)]
        if chunk:
            self._chunks.append(chunk)
            self._kept_bytes += len(chunk)


def _kill_and_reap(process: subprocess.Popen[bytes]) -> None:
    """Kill and reap a shell whose registration failed; nothing else can signal it."""
    with contextlib.suppress(ProcessLookupError, PermissionError):
        os.killpg(process.pid, signal.SIGKILL)
    _, status = os.waitpid(process.pid, 0)
    process.returncode = _shell_status(status)
    for pipe in (process.stdout, process.stderr):
        if pipe is not None:
            pipe.close()


def _release(process: subprocess.Popen[bytes], process_groups: ShellProcessGroups) -> int:
    statuses: list[int] = []

    def reap() -> None:
        _, status = os.waitpid(process.pid, 0)
        statuses.append(_shell_status(status))

    process_groups.release(process.pid, reap)
    process.returncode = statuses[0]
    return statuses[0]


def _shell_status(wait_status: int) -> int:
    """Return a status as a shell reports it: the exit code, or 128 plus a killing signal."""
    returncode = os.waitstatus_to_exitcode(wait_status)
    return 128 - returncode if returncode < 0 else returncode
