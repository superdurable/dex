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
import atexit
import contextlib
import logging
import os
import selectors
import signal
import subprocess
import threading
import time
from collections.abc import Callable
from pathlib import Path
from typing import IO, Protocol

from dex_agents import ToolExecutionError
from dex_agents_cli_core.process_exit import wait_until_exited

#: Seconds a command may run when the agent sets no timeout.
DEFAULT_SHELL_TIMEOUT_SECONDS = 120
#: The longest timeout the agent may set.
MAX_SHELL_TIMEOUT_SECONDS = 600
_MAX_OUTPUT_BYTES = 64 * 1024
# How long background processes a finished shell left may take to end after SIGTERM.
_LEFT_BEHIND_GRACE_SECONDS = 0.5
# How long, after the group is cleaned, the output may still arrive.
_OUTPUT_AFTER_GROUP_SECONDS = 1.0
_READ_BYTES = 65536
_POLL_SECONDS = 0.05
# How often the stop sequence looks for what the shell left in its group.
_MEMBER_POLL_SECONDS = 0.025
_PGREP_TIMEOUT_SECONDS = 2.0
# How long an exiting interpreter waits for each shell it kills.
_EXIT_CLEANUP_SECONDS = 3.0
_LOGGER = logging.getLogger(__name__)


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
    as ``sleep 30 &``, get SIGTERM, then SIGKILL unless they are gone within
    0.5 s (``pgrep -g`` checks every 25 ms; a command that left nothing gets
    no signal and no wait); only then is the shell reaped. A timeout kills the
    whole group at once. After the group is
    cleaned, the output may still arrive for 1 s: a process that left the
    group (``setsid``) and still holds the pipes is not stopped and does not
    hold up the result longer.

    One thread per call owns the shell: it alone waits for it, signals its
    group, reads its output, and reaps it, so a cancelled call, a closing
    event loop, or an exiting interpreter never leaves the group signaled
    after its leader was reaped. A cancelled call kills the group and waits
    for that thread before the cancellation propagates.

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
        ToolExecutionError: If the shell cannot start, as ``the shell could not
            start: <reason>``; the reason is the operating system's own text.
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
    # Synchronous, so no cancellation can come between the spawn and the owner thread.
    try:
        groups.register(process.pid)
    except BaseException:
        _kill_and_reap(process)
        raise
    try:
        owner = _ShellOwner(process, groups, timeout_seconds)
        owner.start()
    except BaseException:
        _abandon(process, groups)
        raise
    try:
        await asyncio.shield(owner.finished)
    except asyncio.CancelledError:
        owner.kill()
        while not owner.finished.done():
            with contextlib.suppress(asyncio.CancelledError):
                await asyncio.shield(owner.finished)
        raise
    return owner.finished.result()


class _ShellOwner(threading.Thread):
    """The one thread that waits for a shell, signals its group, reads its output, and reaps it."""

    def __init__(
        self, process: subprocess.Popen[bytes], groups: ShellProcessGroups, timeout_seconds: int
    ) -> None:
        super().__init__(name=f"dex-agents-shell-{process.pid}", daemon=True)
        assert process.stdout is not None and process.stderr is not None
        self._loop = asyncio.get_running_loop()
        self._process = process
        self._groups = groups
        self._timeout_seconds = timeout_seconds
        self._stdout = _CappedOutput(process.stdout)
        self._stderr = _CappedOutput(process.stderr)
        self._exited = threading.Event()
        self._kill_requested = threading.Event()
        self._done = threading.Event()
        self._selector: selectors.BaseSelector | None = None
        self.finished: asyncio.Future[str] = self._loop.create_future()

    def start(self) -> None:
        """Start owning the shell; an exiting interpreter still gets it cleaned first.

        Raises:
            RuntimeError: If a thread cannot start; the caller then abandons the shell.
        """
        with _OWNERS_LOCK:
            _OWNERS.add(self)
        try:
            threading.Thread(
                target=self._wait_for_exit, name=f"{self.name}-exit", daemon=True
            ).start()
            super().start()
        except BaseException:
            with _OWNERS_LOCK:
                _OWNERS.discard(self)
            raise

    def kill(self) -> None:
        """Kill the command's group now, as for a cancelled call."""
        self._kill_requested.set()

    def wait_done(self, timeout: float) -> None:
        """Wait until the shell is reaped and released, or the timeout passes."""
        self._done.wait(timeout)

    def run(self) -> None:
        outcome: str | BaseException
        try:
            outcome = self._own()
        except BaseException as error:  # The caller gets it; the shell is released below.
            outcome = error
        finally:
            with _OWNERS_LOCK:
                _OWNERS.discard(self)
            self._done.set()
        with contextlib.suppress(RuntimeError):
            self._loop.call_soon_threadsafe(self._report, outcome)

    def _own(self) -> str:
        pid = self._process.pid
        end_reason: str | None = None
        try:
            self._open_selector()
            deadline = time.monotonic() + self._timeout_seconds
            while not self._exited.is_set():
                if self._kill_requested.is_set():
                    end_reason = "cancelled"
                    break
                if time.monotonic() >= deadline:
                    end_reason = f"timed out after {self._timeout_seconds} seconds"
                    break
                self._read_output(min(_POLL_SECONDS, max(0.0, deadline - time.monotonic())))
        finally:
            # Whatever ended the wait, even a failed read, the whole stop sequence runs.
            if not self._exited.is_set():
                self._groups.signal_group(pid, signal.SIGKILL)
                while not self._exited.wait(_POLL_SECONDS):
                    self._read_output(0.0)
            # The shell is a zombie now, so its group ID stays reserved while the group is cleaned.
            if _has_other_group_members(pid):
                self._groups.signal_group(pid, signal.SIGTERM)
                # EOF does not end the grace: a member may close its output and ignore SIGTERM.
                sigkill_at = time.monotonic() + _LEFT_BEHIND_GRACE_SECONDS
                while time.monotonic() < sigkill_at and _has_other_group_members(pid):
                    remaining = max(0.0, sigkill_at - time.monotonic())
                    self._read_output(min(_MEMBER_POLL_SECONDS, remaining))
                self._groups.signal_group(pid, signal.SIGKILL)
            self._read_output_until(time.monotonic() + _OUTPUT_AFTER_GROUP_SECONDS)
            if self._selector is not None:
                self._selector.close()
            self._stdout.close()
            self._stderr.close()
            returncode = _release(self._process, self._groups)
        status = f"killed: {end_reason}" if end_reason is not None else f"exit code {returncode}"
        stdout, stderr = self._stdout.text(), self._stderr.text()
        return f"{status}\n--- stdout ---\n{stdout}\n--- stderr ---\n{stderr}"

    def _open_selector(self) -> None:
        """Watch both pipes; without a selector the output is not read, but the stop still runs."""
        try:
            selector = selectors.DefaultSelector()
        except Exception:  # Such as EMFILE; reading is best effort.
            _LOGGER.exception("the shell's output cannot be watched")
            self._stdout.is_ended = True
            self._stderr.is_ended = True
            return
        try:
            for pipe in (self._stdout, self._stderr):
                selector.register(pipe, selectors.EVENT_READ)
        except Exception:
            _LOGGER.exception("the shell's output cannot be watched")
            selector.close()
            self._stdout.is_ended = True
            self._stderr.is_ended = True
            return
        self._selector = selector

    def _wait_for_exit(self) -> None:
        wait_until_exited(self._process.pid)
        self._exited.set()

    def _read_output_until(self, deadline: float) -> None:
        while not (self._stdout.is_ended and self._stderr.is_ended):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return
            self._read_output(min(_POLL_SECONDS, remaining))

    def _read_output(self, timeout: float) -> None:
        """Read what the pipes hold within ``timeout``; a failure ends reading, not the stop."""
        selector = self._selector
        if selector is None or (self._stdout.is_ended and self._stderr.is_ended):
            time.sleep(max(0.0, timeout))
            return
        try:
            for key, _ in selector.select(timeout):
                pipe: _CappedOutput = key.fileobj  # type: ignore[assignment]
                pipe.read_available()
                if pipe.is_ended:
                    selector.unregister(pipe)
        except Exception:  # Reading is best effort; the group must still be stopped and reaped.
            _LOGGER.exception("the shell's output could not be read")
            self._stdout.is_ended = True
            self._stderr.is_ended = True

    def _report(self, outcome: str | BaseException) -> None:
        if self.finished.done():
            return
        if isinstance(outcome, BaseException):
            self.finished.set_exception(outcome)
        else:
            self.finished.set_result(outcome)


_OWNERS: set[_ShellOwner] = set()
_OWNERS_LOCK = threading.Lock()


def _forget_owners_after_fork() -> None:
    """A forked child runs none of its parent's owner threads, and may inherit a held lock."""
    global _OWNERS_LOCK
    _OWNERS_LOCK = threading.Lock()
    _OWNERS.clear()


if hasattr(os, "register_at_fork"):
    os.register_at_fork(after_in_child=_forget_owners_after_fork)


@atexit.register
def _clean_up_owned_shells() -> None:
    """Kill and reap the shells still running when the interpreter exits, each within a bound."""
    with _OWNERS_LOCK:
        owners = list(_OWNERS)
    for owner in owners:
        owner.kill()
    for owner in owners:
        owner.wait_done(_EXIT_CLEANUP_SECONDS)


class _OwnedProcessGroups:
    """The process group of one command that only its owner thread signals."""

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


class _CappedOutput:
    """One pipe, read to EOF without blocking, keeping at most 64 KiB of it."""

    def __init__(self, pipe: IO[bytes]) -> None:
        self._pipe = pipe
        self._chunks: list[bytes] = []
        self._kept_bytes = 0
        self._is_cut = False
        self.is_ended = False
        os.set_blocking(pipe.fileno(), False)

    def fileno(self) -> int:
        """Return the pipe's descriptor, for ``select``."""
        return self._pipe.fileno()

    def read_available(self) -> None:
        """Read what the pipe holds now; at EOF, mark it ended."""
        try:
            chunk = os.read(self._pipe.fileno(), _READ_BYTES)
        except BlockingIOError:
            return
        except OSError:
            chunk = b""
        if not chunk:
            self.is_ended = True
            return
        room = _MAX_OUTPUT_BYTES - self._kept_bytes
        if len(chunk) > room:
            self._is_cut = True
            chunk = chunk[: max(0, room)]
        if chunk:
            self._chunks.append(chunk)
            self._kept_bytes += len(chunk)

    def close(self) -> None:
        """Stop reading and close the pipe."""
        self.is_ended = True
        self._pipe.close()

    def text(self) -> str:
        """Return the kept output as text, with a line that says when it was cut."""
        text = b"".join(self._chunks).decode("utf-8", errors="replace")
        return f"{text}\n[cut at {_MAX_OUTPUT_BYTES} bytes]" if self._is_cut else text


def _has_other_group_members(pgid: int) -> bool:
    """Return whether a process other than the group's leader is in it; ``True`` when unknown."""
    try:
        completed = subprocess.run(
            ["pgrep", "-g", str(pgid)],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            timeout=_PGREP_TIMEOUT_SECONDS,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        return True
    if completed.returncode == 1:
        # pgrep exits 1 when nothing matches: the group has no process left at all.
        return False
    if completed.returncode != 0:
        return True
    return any(pid.isdigit() and int(pid) != pgid for pid in completed.stdout.split())


def _abandon(process: subprocess.Popen[bytes], groups: ShellProcessGroups) -> None:
    """Stop and reap a registered shell that no owner thread took over."""
    groups.signal_group(process.pid, signal.SIGKILL)
    for pipe in (process.stdout, process.stderr):
        if pipe is not None:
            pipe.close()
    _release(process, groups)


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
