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
from typing import Any, Protocol

from dex_agents import ToolExecutionError
from dex_agents_cli_core.process_exit import wait_until_exited

#: Seconds a command may run when the agent sets no timeout.
DEFAULT_SHELL_TIMEOUT_SECONDS = 120
#: The longest timeout the agent may set.
MAX_SHELL_TIMEOUT_SECONDS = 600
_MAX_OUTPUT_CHARACTERS = 50_000
# How long background processes a finished shell left may take to end after SIGTERM.
_LEFT_BEHIND_GRACE_SECONDS = 0.5


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
    reaches it, and a timeout kills the whole group. With ``process_groups``,
    as in the terminal UI's Worker host, the group is registered until it is
    gone: when the shell exits, the processes it left in the background get
    SIGTERM, then SIGKILL, and only then is the shell reaped and released.

    Args:
        command: The command text.
        workspace: The working directory.
        timeout_seconds: Seconds before the command is killed.
        process_groups: Where to register the command's process group, or ``None``.

    .. versionchanged:: 0.6.0
        Added ``process_groups``.

    Returns:
        The exit code, standard output, and standard error.

    Raises:
        ToolExecutionError: If the command times out or cannot start.
    """
    if process_groups is not None:
        return await _run_registered_shell_command(
            command, workspace, timeout_seconds, process_groups
        )
    try:
        process = await asyncio.create_subprocess_exec(
            "/bin/sh",
            "-c",
            command,
            cwd=workspace,
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            start_new_session=True,
        )
    except OSError as error:
        raise ToolExecutionError(f"cannot start the command: {error.strerror}") from error
    try:
        stdout, stderr = await asyncio.wait_for(process.communicate(), timeout_seconds)
    except (TimeoutError, asyncio.CancelledError) as error:
        _kill_process_group(process.pid)
        await process.wait()
        if isinstance(error, asyncio.CancelledError):
            raise
        raise ToolExecutionError(
            f"the command ran longer than {timeout_seconds} seconds"
        ) from error
    return _report(process.returncode, stdout, stderr)


async def _run_registered_shell_command(
    command: str, workspace: Path, timeout_seconds: int, process_groups: ShellProcessGroups
) -> str:
    """Run a command whose process group stays registered until it is gone.

    The shell is never reaped before its group is cleaned, so its pid, which
    is the group's ID, cannot be reused while anything may signal it.
    """
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
        process_groups.register(process.pid)
    except BaseException:
        _kill_and_reap(process)
        raise
    assert process.stdout is not None and process.stderr is not None
    output = asyncio.ensure_future(
        asyncio.gather(
            asyncio.to_thread(process.stdout.read), asyncio.to_thread(process.stderr.read)
        )
    )
    exited = asyncio.ensure_future(asyncio.to_thread(wait_until_exited, process.pid))
    interruption: BaseException | None = None
    try:
        await asyncio.wait_for(asyncio.shield(exited), timeout_seconds)
    except (TimeoutError, asyncio.CancelledError) as stopped:
        interruption = stopped
        process_groups.signal_group(process.pid, signal.SIGKILL)
    finally:
        # The shell is a zombie now, so its group ID stays reserved while the group is cleaned.
        await asyncio.shield(exited)
        await _stop_group_left_behind(process.pid, output, process_groups)
        stdout, stderr = await asyncio.shield(output)
        process.stdout.close()
        process.stderr.close()
        returncode = _release(process, process_groups)
    if isinstance(interruption, asyncio.CancelledError):
        raise interruption
    if interruption is not None:
        raise ToolExecutionError(
            f"the command ran longer than {timeout_seconds} seconds"
        ) from interruption
    return _report(returncode, stdout, stderr)


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


def _kill_process_group(pid: int) -> None:
    # The group may be gone already when the command exits during the timeout.
    with contextlib.suppress(ProcessLookupError):
        os.killpg(pid, signal.SIGKILL)


def _truncate(text: str) -> str:
    if len(text) <= _MAX_OUTPUT_CHARACTERS:
        return text
    return f"{text[:_MAX_OUTPUT_CHARACTERS]}\n... ({len(text) - _MAX_OUTPUT_CHARACTERS} more)"
