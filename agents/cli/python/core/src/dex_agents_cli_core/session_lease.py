# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""One terminal per session: the session terminal lease, its waiting line, and its renewal.

A command holds the lease of the session it shows. A second command that
opens the same session waits, with one status line that updates in place,
until the first releases the lease or stops renewing it. The lines are fixed by
``agents/conformance/cli_session_lease.json``.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import os
import signal
import socket
import uuid
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from datetime import datetime, tzinfo
from typing import TextIO

from dex import DexServiceError

from dex_agents import (
    AcquireSessionTerminalLeaseRequest,
    AgentSessionClient,
    SessionTerminalLease,
    SessionTerminalLeaseResult,
)

#: The spinner of the waiting line, one frame each ``FRAME_INTERVAL_SECONDS``.
SPINNER_FRAMES = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
#: How often the waiting line is redrawn.
FRAME_INTERVAL_SECONDS = 0.1
#: How often a waiting command asks for the lease again.
RETRY_INTERVAL_SECONDS = 1.0
#: What the animated line writes before each redraw, and to clear it.
REDRAW_PREFIX = "\r\x1b[2K"
#: The terminal width when it cannot be read.
DEFAULT_COLUMNS = 80
#: How long one lease call may take before it counts as failed.
LEASE_CALL_TIMEOUT_SECONDS = 5.0
#: How long the release at exit may take; the lease expires anyway.
LEASE_RELEASE_TIMEOUT_SECONDS = 2.0
_HOST_NAME_LIMIT = 255
_LOGGER = logging.getLogger(__name__)

#: One try to acquire or renew the lease; raises for a failed call.
LeaseAttempt = Callable[[], Awaitable[SessionTerminalLeaseResult]]


class LeaseCallError(Exception):
    """A lease call through another process failed; a waiting command tries again."""


class SessionLeaseWaitInterrupted(Exception):
    """Ctrl-C while the command waited for a session; the command exits 130."""


@dataclass(frozen=True)
class SessionLeaseHolder:
    """Who holds a lease: one random ID per process, and what a waiting terminal shows.

    Attributes:
        holder_id: Random 128-bit lowercase hex.
        host_name: This host's name, cut to 255 code points.
        process_id: The process ID of the command the user ran.
        ttl_seconds: How long the lease lasts without a renewal.
    """

    holder_id: str
    host_name: str
    process_id: int
    ttl_seconds: int

    @classmethod
    def for_this_process(
        cls, ttl_seconds: int, process_id: int | None = None
    ) -> SessionLeaseHolder:
        """Return a new holder for this process.

        Args:
            ttl_seconds: How long the lease lasts without a renewal.
            process_id: The pid to show; ``None`` uses this process's.
        """
        return cls(
            holder_id=uuid.uuid4().hex,
            host_name=socket.gethostname()[:_HOST_NAME_LIMIT],
            process_id=os.getpid() if process_id is None else process_id,
            ttl_seconds=ttl_seconds,
        )

    @property
    def renew_interval_seconds(self) -> float:
        """Return how often the holder renews: a third of the lease."""
        return self.ttl_seconds / 3

    def acquire_request(self) -> AcquireSessionTerminalLeaseRequest:
        """Return the RPC request that acquires or renews this holder's lease."""
        return AcquireSessionTerminalLeaseRequest(
            holder_id=self.holder_id,
            host_name=self.host_name,
            process_id=self.process_id,
            ttl_seconds=self.ttl_seconds,
        )


class HeldSessionLease:
    """The lease one process holds on the session it shows, renewed in the background.

    It holds at most one session's lease. ``lost`` completes with the other
    holder's lease when a renewal finds that another terminal took it.
    """

    def __init__(self, sessions: AgentSessionClient, holder: SessionLeaseHolder) -> None:
        """Hold leases through a session client.

        Args:
            sessions: The session client.
            holder: This process's holder.
        """
        self._sessions = sessions
        self._holder = holder
        self._session_id: str | None = None
        self._renewing: asyncio.Task[None] | None = None
        self.lost: asyncio.Future[tuple[str, SessionTerminalLease]] = (
            asyncio.get_running_loop().create_future()
        )

    @property
    def session_id(self) -> str | None:
        """Return the session whose lease it holds, or ``None``."""
        return self._session_id

    async def try_acquire(self, session_id: str) -> SessionTerminalLeaseResult:
        """Ask once for the session's lease; on success, renew it from now on.

        Raises:
            DexServiceError: If the call fails.
            TimeoutError: If the call takes longer than ``LEASE_CALL_TIMEOUT_SECONDS``.
        """
        result = await self._call_acquire(session_id)
        if result.is_acquired:
            self._stop_renewing()
            self._session_id = session_id
            self._renewing = asyncio.create_task(self._renew(session_id))
        return result

    async def release(self) -> None:
        """Release the held lease, if any, within ``LEASE_RELEASE_TIMEOUT_SECONDS``.

        A failed release is only logged: the lease expires anyway.
        """
        self._stop_renewing()
        session_id, self._session_id = self._session_id, None
        if session_id is None:
            return
        try:
            await asyncio.wait_for(
                self._sessions.release_session_terminal_lease(session_id, self._holder.holder_id),
                LEASE_RELEASE_TIMEOUT_SECONDS,
            )
        except (DexServiceError, TimeoutError) as error:
            _LOGGER.warning("cannot release the session lease: session=%s: %s", session_id, error)

    async def _call_acquire(self, session_id: str) -> SessionTerminalLeaseResult:
        return await asyncio.wait_for(
            self._sessions.acquire_session_terminal_lease(
                session_id, self._holder.acquire_request()
            ),
            LEASE_CALL_TIMEOUT_SECONDS,
        )

    async def _renew(self, session_id: str) -> None:
        while True:
            await asyncio.sleep(self._holder.renew_interval_seconds)
            try:
                result = await self._call_acquire(session_id)
            except (DexServiceError, TimeoutError) as error:
                _LOGGER.warning("cannot renew the session lease: session=%s: %s", session_id, error)
                continue
            if result.is_acquired or result.lease is None:
                continue
            if self._session_id == session_id:
                self._session_id = None
            if not self.lost.done():
                self.lost.set_result((session_id, result.lease))
            return

    def _stop_renewing(self) -> None:
        if self._renewing is not None:
            self._renewing.cancel()
            self._renewing = None


async def wait_for_session_lease(
    session_id: str,
    attempt: LeaseAttempt,
    output: TextIO,
    *,
    is_animated: bool,
    columns: Callable[[], int],
) -> None:
    """Return once ``attempt`` acquires the lease, showing who has the session meanwhile.

    The first try that succeeds prints nothing. Otherwise the command waits
    with the conformance file's lines on ``output``, and tries again every
    second. A failed try is retried silently.

    Args:
        session_id: The session.
        attempt: One try to acquire the lease.
        output: Standard error.
        is_animated: Whether ``output`` is a terminal that redraws one line.
        columns: Returns the terminal width.

    Raises:
        SessionLeaseWaitInterrupted: On Ctrl-C while waiting.
    """
    interrupted = asyncio.Event()
    loop = asyncio.get_running_loop()
    is_sigint_handled = True
    try:
        loop.add_signal_handler(signal.SIGINT, interrupted.set)
    except (NotImplementedError, RuntimeError):
        is_sigint_handled = False
    display = _WaitingDisplay(session_id, output, is_animated, columns)
    try:
        # The first try can be slow, such as on a paused Worker; Ctrl-C ends it too.
        result = await _until_interrupted(_attempt_quietly(attempt), interrupted)
        if result is not None and result.is_acquired:
            return
        await _wait_until_acquired(result, attempt, display, interrupted)
    except (KeyboardInterrupt, SessionLeaseWaitInterrupted):
        display.clear()
        raise SessionLeaseWaitInterrupted() from None
    except BaseException:
        display.clear()
        raise
    finally:
        if is_sigint_handled:
            loop.remove_signal_handler(signal.SIGINT)
    display.finish()


async def _until_interrupted(
    attempting: Awaitable[SessionTerminalLeaseResult | None], interrupted: asyncio.Event
) -> SessionTerminalLeaseResult | None:
    """Return one try's result, or raise ``SessionLeaseWaitInterrupted`` on Ctrl-C first."""
    work = asyncio.ensure_future(attempting)
    interrupt = asyncio.create_task(interrupted.wait())
    try:
        await asyncio.wait({work, interrupt}, return_when=asyncio.FIRST_COMPLETED)
    finally:
        interrupt.cancel()
        if not work.done():
            work.cancel()
    if not work.done() or work.cancelled():
        raise SessionLeaseWaitInterrupted()
    return work.result()


async def _wait_until_acquired(
    result: SessionTerminalLeaseResult | None,
    attempt: LeaseAttempt,
    display: _WaitingDisplay,
    interrupted: asyncio.Event,
) -> None:
    if result is not None:
        display.show_holder(result.lease)
    loop = asyncio.get_running_loop()
    started_at = loop.time()
    next_attempt_at = started_at + RETRY_INTERVAL_SECONDS
    pending: asyncio.Task[SessionTerminalLeaseResult | None] | None = None
    frame_index = 0
    try:
        while True:
            display.draw(frame_index, loop.time() - started_at)
            if pending is None and loop.time() >= next_attempt_at:
                pending = asyncio.create_task(_attempt_quietly(attempt))
            if pending is not None and pending.done():
                result = pending.result()
                pending = None
                next_attempt_at = loop.time() + RETRY_INTERVAL_SECONDS
                if result is not None and result.is_acquired:
                    return
                if result is not None:
                    display.show_holder(result.lease)
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(interrupted.wait(), FRAME_INTERVAL_SECONDS)
            if interrupted.is_set():
                raise SessionLeaseWaitInterrupted()
            frame_index += 1
    finally:
        if pending is not None:
            pending.cancel()


async def _attempt_quietly(attempt: LeaseAttempt) -> SessionTerminalLeaseResult | None:
    """Return one try's result, or ``None`` for a failed call or an invalid request."""
    try:
        result = await attempt()
    except (DexServiceError, TimeoutError, LeaseCallError) as error:
        _LOGGER.warning("cannot acquire the session lease: %s", error)
        return None
    if not result.is_acquired and result.lease is None:
        _LOGGER.warning("the session refused the lease request as not valid")
        return None
    return result


class _WaitingDisplay:
    """The waiting line: redrawn in place on a terminal, or one plain line otherwise.

    Nothing shows until a refusal names the holder, so a failed first call
    alone never claims that another terminal has the session.
    """

    def __init__(
        self, session_id: str, output: TextIO, is_animated: bool, columns: Callable[[], int]
    ) -> None:
        self._session_id = session_id
        self._output = output
        self._is_animated = is_animated
        self._columns = columns
        self._holder_text: str | None = None

    def show_holder(self, lease: SessionTerminalLease | None) -> None:
        if lease is None:
            return
        is_first = self._holder_text is None
        self._holder_text = format_lease_holder(lease)
        if not self._is_animated and is_first:
            self._write(build_plain_waiting_line(self._session_id, self._holder_text) + "\n")

    def draw(self, frame_index: int, elapsed_seconds: float) -> None:
        if not self._is_animated or self._holder_text is None:
            return
        line = build_animated_waiting_line(
            self._session_id, self._holder_text, int(elapsed_seconds), frame_index, self._columns()
        )
        self._write(REDRAW_PREFIX + line)

    def clear(self) -> None:
        if self._is_animated and self._holder_text is not None:
            self._write(REDRAW_PREFIX)

    def finish(self) -> None:
        if self._holder_text is None:
            return
        self.clear()
        self._write(build_took_over_line(self._session_id) + "\n")

    def _write(self, text: str) -> None:
        with contextlib.suppress(OSError, ValueError):
            self._output.write(text)
            self._output.flush()


def is_waiting_line_animated(output: TextIO, terminal_type: str | None) -> bool:
    """Return whether the waiting line redraws: a terminal whose ``TERM`` is set and not dumb."""
    try:
        is_terminal = output.isatty()
    except (OSError, ValueError):
        return False
    return is_terminal and terminal_type not in (None, "", "dumb")


def terminal_columns(output: TextIO) -> int:
    """Return the width of the terminal ``output`` writes to, or ``DEFAULT_COLUMNS``."""
    try:
        return os.get_terminal_size(output.fileno()).columns or DEFAULT_COLUMNS
    except (OSError, ValueError):
        return DEFAULT_COLUMNS


def format_lease_since(opened_at: str, time_zone: tzinfo | None = None) -> str:
    """Return the local ``HH:MM`` of a lease's ``opened_at``, or ``"?"`` when it does not parse.

    Args:
        opened_at: RFC 3339 time.
        time_zone: The zone to show; ``None`` uses the local one.
    """
    try:
        moment = datetime.fromisoformat(opened_at)
    except ValueError:
        return "?"
    return moment.astimezone(time_zone).strftime("%H:%M")


def format_lease_holder(lease: SessionTerminalLease, time_zone: tzinfo | None = None) -> str:
    """Return ``host, pid N, since HH:MM`` for a lease."""
    return (
        f"{lease.host_name}, pid {lease.process_id}, "
        f"since {format_lease_since(lease.opened_at, time_zone)}"
    )


def format_waiting_elapsed(seconds: int) -> str:
    """Return whole minutes, a colon, and two-digit seconds."""
    return f"{seconds // 60}:{seconds % 60:02d}"


def build_animated_waiting_line(
    session_id: str, holder_text: str, elapsed_seconds: int, frame_index: int, columns: int
) -> str:
    """Return one frame of the waiting line, cut to the terminal width."""
    frame = SPINNER_FRAMES[frame_index % len(SPINNER_FRAMES)]
    line = (
        f"{frame} Session {session_id} is open in another terminal ({holder_text}) "
        f"· waiting {format_waiting_elapsed(elapsed_seconds)} · Ctrl-C to quit"
    )
    width = max(columns, 2) - 1
    return line if len(line) <= width else line[: width - 1] + "…"


def build_plain_waiting_line(session_id: str, holder_text: str) -> str:
    """Return the one waiting line written when the output is not a terminal."""
    return (
        f"Session {session_id} is open in another terminal ({holder_text}) "
        "· waiting · Ctrl-C to quit"
    )


def build_took_over_line(session_id: str) -> str:
    """Return the line written once a waiting command gets the session."""
    return f"Took over session {session_id}."


def build_lease_lost_line(session_id: str, holder_text: str) -> str:
    """Return the line written when another terminal took the session from this chat."""
    return (
        f"Session {session_id} is now open in another terminal ({holder_text}); this chat stopped."
    )
