# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deadline-bounded polling for durable state."""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from datetime import timedelta
from typing import TypeVar

from dex import DexServiceError

from dex_agents.client import AgentSessionClient
from dex_agents.values import AgentSnapshot

ValueT = TypeVar("ValueT")

DEFAULT_WAIT_TIMEOUT = timedelta(seconds=90)
DEFAULT_POLL_INTERVAL = timedelta(milliseconds=200)


async def wait_until(
    description: str,
    probe: Callable[[], Awaitable[ValueT | None]],
    *,
    timeout: timedelta = DEFAULT_WAIT_TIMEOUT,
    interval: timedelta = DEFAULT_POLL_INTERVAL,
) -> ValueT:
    """Poll ``probe`` until it returns a truthy value, then return that value.

    Use it instead of a fixed sleep whenever a test waits for durable state to
    converge. ``None``, ``False``, and other falsy values mean "not yet".

    Example:
        >>> page = await wait_until("two messages", fetch_page_with_two_messages)

    Args:
        description: What the test waits for, used in the timeout message.
        probe: Coroutine function returning the value, or a falsy value.
        timeout: Maximum total wait. Defaults to 90 seconds.
        interval: Delay between probes. Defaults to 200 milliseconds.

    Returns:
        The first truthy value ``probe`` returned.

    Raises:
        TimeoutError: If no truthy value arrives in time. The message includes
            the last value.
    """
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout.total_seconds()
    while True:
        value = await probe()
        if value:
            return value
        if loop.time() >= deadline:
            raise TimeoutError(
                f"timed out after {timeout} waiting for {description}; last {value!r}"
            )
        await asyncio.sleep(interval.total_seconds())


async def wait_for_agent_snapshot(
    sessions: AgentSessionClient,
    session_id: str,
    select: Callable[[AgentSnapshot], ValueT | None],
    *,
    description: str,
    timeout: timedelta = DEFAULT_WAIT_TIMEOUT,
) -> ValueT:
    """Poll ``get_agent_snapshot`` until ``select`` returns a truthy value.

    A ``DexServiceError`` counts as "not yet", because the RPC fails while no
    Worker serves the session, such as during a Worker replacement.

    Example:
        >>> interrupt = await wait_for_agent_snapshot(
        ...     sessions, session_id,
        ...     lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        ...     description="a pending approval")

    Args:
        sessions: Client for the agent's sessions.
        session_id: Session to poll.
        select: Returns the awaited value from a snapshot, or a falsy value.
        description: What the test waits for, used in the timeout message.
        timeout: Maximum total wait. Defaults to 90 seconds.

    Returns:
        The first truthy value ``select`` returned.

    Raises:
        TimeoutError: If no truthy value arrives in time.
    """

    async def probe() -> ValueT | None:
        try:
            snapshot = await sessions.get_agent_snapshot(session_id)
        except DexServiceError:
            return None
        return select(snapshot)

    return await wait_until(f"session {session_id}: {description}", probe, timeout=timeout)
