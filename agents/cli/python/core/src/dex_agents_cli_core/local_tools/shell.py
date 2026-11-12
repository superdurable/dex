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
from pathlib import Path
from typing import Protocol

from dex_agents import ToolExecutionError

#: Seconds a command may run when the agent sets no timeout.
DEFAULT_SHELL_TIMEOUT_SECONDS = 120
#: The longest timeout the agent may set.
MAX_SHELL_TIMEOUT_SECONDS = 600
_MAX_OUTPUT_CHARACTERS = 50_000


class ShellProcessGroups(Protocol):
    """Where a shell command registers its process group while its leader is unreaped.

    .. versionadded:: 0.6.0
    """

    def register(self, pid: int) -> None:
        """Register the leader of a new process group right after its spawn."""
        ...

    def unregister(self, pid: int) -> None:
        """Unregister the leader right after it was reaped."""
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
    reaches it, and a timeout kills the whole group.

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
    if process_groups is not None:
        # Synchronous, so no cancellation can come between the spawn and the registration.
        process_groups.register(process.pid)
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
    finally:
        if process_groups is not None and process.returncode is not None:
            process_groups.unregister(process.pid)
    sections = [f"exit code {process.returncode}"]
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
