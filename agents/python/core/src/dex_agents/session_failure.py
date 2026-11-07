# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Read a failed session's state from its Flow close, which Dex always records.

A session at the state limit may have no room left to store ``AgentState``. The
commit that would store it fails on the same limit, and the Flow closes without
it. So a reader takes a failed Flow's status and reason from the close record.
"""

from __future__ import annotations

from dataclasses import replace
from typing import Any

from dex import AsyncClient, Client, FlowNotActiveOrNotFoundError, FlowNotFoundError, FlowStatus

from dex_agents.values import AgentSessionStatus, AgentSnapshot

_UNKNOWN_FAILURE = "the session's Flow failed"


class AgentSessionFailedError(FlowNotActiveOrNotFoundError):
    """Report a message sent to a session that failed.

    ``AgentSessionClient`` raises it in place of the Dex error for a closed
    Flow, and it stays a ``FlowNotActiveOrNotFoundError``. The message did not
    reach the session. Messages queued earlier stay readable in
    ``AgentSnapshot.queued_messages`` and never run.

    Attributes:
        reason: Why the session failed, such as ``run state of 67492 bytes
            exceeds the maximum of 65536 bytes``.
    """

    def __init__(self, session_id: str, reason: str, cause: FlowNotActiveOrNotFoundError) -> None:
        """Create the error from the Dex error it replaces.

        Args:
            session_id: The failed session.
            reason: Why the session failed.
            cause: The Dex error for the closed Flow.
        """
        super().__init__(
            cause.code,
            cause.sub_status,
            f"session {session_id} failed: {reason}",
            cause.operation,
            session_id,
        )
        self.reason = reason


def with_session_failure(snapshot: AgentSnapshot, reason: str) -> AgentSnapshot:
    """Return the snapshot of a session whose Flow failed for ``reason``.

    Args:
        snapshot: The stored snapshot.
        reason: The Flow's failure reason.

    Returns:
        The same snapshot with what the session stores when it has room:
        status ``FAILED``, the reason, no active turn, and no pending
        interrupts. ``tool_calls`` lists the active turn's calls, so it is
        empty too; a stale turn's calls will never finish. A snapshot with no
        state, or with status ``FAILED``, is returned unchanged.
    """
    state = snapshot.state
    if not _may_hide_session_failure(snapshot) or state is None:
        return snapshot
    return replace(
        snapshot,
        state=replace(
            state,
            status=AgentSessionStatus.FAILED,
            turn_count=state.turn_count + (0 if state.active_turn_id is None else 1),
            active_turn_id=None,
            accepts_turn_control=False,
            last_turn_error=reason,
        ),
        pending_interrupts=[],
        tool_calls=[],
    )


async def read_session_failure(client: AsyncClient, session_id: str) -> str | None:
    """Return why a session's Flow failed.

    Args:
        client: An open Client.
        session_id: Running or retained closed session.

    Returns:
        The Flow's failure reason, or ``None`` when the Flow did not fail or
        does not exist.
    """
    try:
        if (await client.describe_flow(session_id)).status is not FlowStatus.FAILED:
            return None
    except FlowNotFoundError:
        return None
    return (await client.wait_for_flow(session_id)).error_message or _UNKNOWN_FAILURE


def read_session_failure_sync(client: Client, session_id: str) -> str | None:
    """Return why a session's Flow failed, through a synchronous Client.

    Args:
        client: An open Client.
        session_id: Running or retained closed session.

    Returns:
        The reason. See ``read_session_failure``.
    """
    try:
        if client.describe_flow(session_id).status is not FlowStatus.FAILED:
            return None
    except FlowNotFoundError:
        return None
    return client.wait_for_flow(session_id).error_message or _UNKNOWN_FAILURE


async def read_agent_snapshot(client: AsyncClient, flow: Any, session_id: str) -> AgentSnapshot:
    """Return the session snapshot, with a failed Flow's status and reason.

    Prefer this to calling ``get_agent_snapshot`` directly: that RPC reads only
    what the session stored. It costs one more call to Dex.

    Args:
        client: An open Client whose Registry contains ``flow``.
        flow: The registered ``AgentSessionFlow``.
        session_id: Running or retained closed session.

    Returns:
        The snapshot. See ``with_session_failure``.
    """
    snapshot: AgentSnapshot = await client.invoke_rpc(flow.get_agent_snapshot, session_id)
    if not _may_hide_session_failure(snapshot):
        return snapshot
    reason = await read_session_failure(client, session_id)
    return snapshot if reason is None else with_session_failure(snapshot, reason)


def read_agent_snapshot_sync(client: Client, flow: Any, session_id: str) -> AgentSnapshot:
    """Return the session snapshot through a synchronous Client.

    Args:
        client: An open Client whose Registry contains ``flow``.
        flow: The registered ``AgentSessionFlow``.
        session_id: Running or retained closed session.

    Returns:
        The snapshot. See ``read_agent_snapshot``.
    """
    snapshot: AgentSnapshot = client.invoke_rpc(flow.get_agent_snapshot, session_id)
    if not _may_hide_session_failure(snapshot):
        return snapshot
    reason = read_session_failure_sync(client, session_id)
    return snapshot if reason is None else with_session_failure(snapshot, reason)


def _may_hide_session_failure(snapshot: AgentSnapshot) -> bool:
    """Report whether the Flow's close could add to this snapshot."""
    state = snapshot.state
    return state is not None and state.status is not AgentSessionStatus.FAILED
