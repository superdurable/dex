# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fork an agent session between turns into a new session.

The rules match the TypeScript core; see ``agents/conformance/session_fork.json``.
"""

from __future__ import annotations

import json
import logging
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any, cast

from dex import (
    AsyncClient,
    CloneForkFlowOptions,
    DexServiceError,
    Flow,
    IdReusePolicy,
    PersistenceSchema,
    StopFlowOptions,
    StopType,
    Stream,
    TimeTravelStepMethod,
    TimeTravelType,
)

from dex_agents.events import AgentEventType
from dex_agents.persistence import (
    AGENT_STATE,
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    SESSION_TERMINAL_LEASE,
)
from dex_agents.session_flow import AgentSessionFlow
from dex_agents.values import AgentEvent, AgentSessionStatus, AgentState

#: The Step type whose WaitFor starts each wait for the next user message.
#:
#: .. versionadded:: 0.6.13
SESSION_FORK_TURN_START_STEP_TYPE = "AwaitUserMessage"
_EVENT_PAGE_SIZE = 200
# Bounds the backward read of AgentEvents; the Stream's capacity bounds it too.
_MAXIMUM_EVENT_PAGES = 100
_LOGGER = logging.getLogger(__name__)


class AgentSessionForkError(Exception):
    """A session fork was refused; nothing was created, or the created fork was stopped.

    ``code`` names the rule, as ``agents/conformance/session_fork.json`` does:
    ``not_started``, ``turn_in_progress``, ``no_messages``,
    ``message_out_of_range``, ``inside_turn``, ``steered_turn``,
    ``point_unavailable``, ``point_refused``, or ``session_changed``. The message
    is one sentence for the user.

    .. versionadded:: 0.6.13

    Attributes:
        code: The rule that refused the fork.
    """

    def __init__(self, code: str, message: str) -> None:
        """Name the rule and the sentence the user sees."""
        super().__init__(message)
        self.code = code


@dataclass(frozen=True)
class AgentSessionTurnStart:
    """Where one turn started, as its ``turn_started`` event on ``AgentEvents`` says.

    .. versionadded:: 0.6.13

    Attributes:
        sequence: The sequence of the turn's first user message.
        step_execution_id: The Step execution that started the turn, such as
            ``AwaitUserMessage-3``.
    """

    sequence: int
    step_execution_id: str


@dataclass(frozen=True)
class AgentSessionForkPoint:
    """The point of the source session that a fork copies.

    .. versionadded:: 0.6.13

    Attributes:
        after_message: The last message sequence the fork keeps; ``0`` keeps none.
        step_execution_id: The ``AwaitUserMessage`` execution whose WaitFor
            starts right after that message's turn, or ``None`` for the
            source's current state.
    """

    after_message: int
    step_execution_id: str | None


@dataclass(frozen=True)
class AgentSessionFork:
    """A session that ``fork_agent_session`` created.

    .. versionadded:: 0.6.13

    Attributes:
        session_id: The new session's Flow ID.
        run_id: The new session's run ID.
        after_message: The last message sequence the new session has.
        source_run_id: The run of the source session that was copied.
        source_event_id: The last history event of the source run that the copy includes.
    """

    session_id: str
    run_id: str
    after_message: int
    source_run_id: str
    source_event_id: int


class AgentSessionForkingFlow(Flow[None]):
    """A Flow with no Steps that declares what a fork of one session Flow type reads and writes.

    Register it in a Client to fork sessions of ``flow_type`` without building
    that type's ``AgentSessionFlow``, whose runner may need a model API key.
    It declares ``AgentState``, ``SessionTerminalLease``,
    ``SessionLastTerminalEnvironment``, and the ``AgentEvents`` Stream. It never
    runs: a Worker never registers it.

    .. versionadded:: 0.6.13

    Example:
        >>> forking = AgentSessionForkingFlow("support-agent")
        >>> client = AsyncClient(Registry([forking]), options=client_options)
        >>> fork = await fork_agent_session(client, forking, "session-42", "session-42-b")
    """

    def __init__(
        self, flow_type: str, event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES
    ) -> None:
        """Declare the definitions of one session Flow type.

        Args:
            flow_type: The session Flow type, the same as the sessions' Flow type.
            event_stream_capacity_bytes: The ``AgentEvents`` capacity that the
                session Flow declares. It only has to match for a Worker; the
                default suits a Client.
        """
        self._flow_type = flow_type
        self.agent_events: Stream[AgentEvent] = Stream(
            "AgentEvents", AgentEvent, event_stream_capacity_bytes
        )

    def get_flow_type(self) -> str:
        """Return the session Flow type."""
        return self._flow_type

    def get_persistence_schema(self) -> PersistenceSchema:
        """Return the Attributes and the Stream a fork uses."""
        return PersistenceSchema.of(
            AGENT_STATE,
            SESSION_TERMINAL_LEASE,
            SESSION_LAST_TERMINAL_ENVIRONMENT,
            self.agent_events,
        )


async def fork_agent_session(
    client: AsyncClient,
    flow: AgentSessionFlow | AgentSessionForkingFlow,
    source_session_id: str,
    session_id: str,
    *,
    after_message: int | None = None,
    task_queue: str = "",
    request_id: str | None = None,
) -> AgentSessionFork:
    """Create a new session from another session's conversation, between two turns.

    The new session copies the source session's state at one point between
    turns: its messages, settings, usage, and plan. It has no Flow history and
    no ``AgentEvents`` of the source. The source session never changes and
    keeps running. Dex writes two Attributes in the same change that creates
    the new session: it deletes ``SessionTerminalLease``, so no terminal holds
    the new session, and ``SessionLastTerminalEnvironment``, so opening it is
    no takeover.

    The point is the source's current state when ``after_message`` is
    ``None``, or when it names the last message. A turn must not be running
    then. For an earlier message, it is the point right after the turn that
    the message ends: the WaitFor of the ``AwaitUserMessage`` execution that
    started the next turn, read from that turn's ``turn_started`` event. That
    event must still be on ``AgentEvents``, and the point must still be in the
    source run's history window. A message inside a turn, or one whose next
    turn steering started, has no such point.

    After the copy, the new session's ``AgentState`` must show no running
    turn and exactly the kept messages. Otherwise the new session is stopped
    and the call raises ``session_changed``.

    Messages queued before the point and not yet taken by a turn are copied
    too, so the new session runs them as soon as a Worker serves it. A point
    between turns has no SubFlow wait, so the SubFlow reset policy does not
    apply; the server default is kept.

    Example:
        >>> fork = await fork_agent_session(
        ...     client, flow, "session-42", "session-42-b",
        ...     after_message=6, task_queue="session-42-b")
        >>> await sessions.send_user_message(fork.session_id, "Try the other plan")

    .. versionadded:: 0.6.13

    Args:
        client: An open Client whose Registry contains ``flow``.
        flow: The source session's Flow type: its ``AgentSessionFlow``, or an
            ``AgentSessionForkingFlow`` of that type.
        source_session_id: The session to fork.
        session_id: The new session's Flow ID. It must differ from the source.
        after_message: The last message sequence the new session keeps, or
            ``None`` for the source's current state.
        task_queue: The new session's task queue. ``""``, the default, keeps the
            source's task queue. Workers pull a named task queue in pull mode.
        request_id: The clone fork request ID, or ``None`` to generate one.

    Returns:
        The new session and the copied point.

    Raises:
        AgentSessionForkError: If the point is not between turns, cannot be
            found, or Dex refuses it, or the session changed during the fork.
        CloneForkFlowUnsupportedError: If the server is older than server-zd
            0.1.33. Nothing was created.
        FlowAlreadyStartedError: If ``session_id`` is in use.
        FlowNotFoundError: If the source session does not exist.
    """
    stored = await client.get_attributes(source_session_id, AGENT_STATE)
    state: AgentState | None = stored.get(AGENT_STATE)
    if state is None:
        raise AgentSessionForkError(
            "not_started",
            f"session {source_session_id} has not started yet, so it cannot be forked",
        )
    is_earlier_message = after_message is not None and 1 <= after_message < (
        state.next_message_sequence - 1
    )
    turn_starts = (
        await read_agent_session_turn_starts(
            client, flow.agent_events, source_session_id, cast(int, after_message) + 1
        )
        if is_earlier_message
        else []
    )
    point = plan_agent_session_fork_point(
        source_session_id,
        after_message,
        state.next_message_sequence,
        state.active_turn_id,
        state.status,
        turn_starts,
    )
    result = await _clone_fork(
        client, flow, source_session_id, session_id, point, task_queue, request_id
    )
    await _require_fork_between_turns(client, source_session_id, session_id, point)
    return AgentSessionFork(
        session_id=session_id,
        run_id=result.run_id,
        after_message=point.after_message,
        source_run_id=result.source_run_id,
        source_event_id=result.source_event_id,
    )


def plan_agent_session_fork_point(
    source_session_id: str,
    after_message: int | None,
    next_message_sequence: int,
    active_turn_id: str | None,
    status: AgentSessionStatus,
    turn_starts: Sequence[AgentSessionTurnStart],
) -> AgentSessionForkPoint:
    """Return the point a fork copies, from the source's state and its turn starts.

    .. versionadded:: 0.6.13

    Args:
        source_session_id: The source session, for the error messages.
        after_message: The last message the fork keeps, or ``None`` for the current state.
        next_message_sequence: ``AgentState.next_message_sequence`` of the source.
        active_turn_id: ``AgentState.active_turn_id`` of the source.
        status: ``AgentState.status`` of the source.
        turn_starts: The source's retained turn starts, in any order.

    Returns:
        The point.

    Raises:
        AgentSessionForkError: If no point between turns follows the message.
    """
    if status is AgentSessionStatus.FAILED:
        raise AgentSessionForkError(
            "session_failed", f"session {source_session_id} failed, so it cannot be forked"
        )
    last_message = next_message_sequence - 1
    if after_message is None or (after_message == last_message and last_message > 0):
        if active_turn_id or status is not AgentSessionStatus.WAITING_FOR_USER_MESSAGE:
            raise AgentSessionForkError(
                "turn_in_progress",
                f"session {source_session_id} is running a turn; fork it once the turn ends, "
                "or after an earlier message",
            )
        return AgentSessionForkPoint(last_message, None)
    if last_message == 0:
        raise AgentSessionForkError(
            "no_messages",
            f"session {source_session_id} has no messages yet; fork it without a message number",
        )
    if not 1 <= after_message < last_message:
        raise AgentSessionForkError(
            "message_out_of_range",
            f"session {source_session_id} has messages 1 to {last_message}; "
            "fork after one of them",
        )
    following = after_message + 1
    started = next((start for start in turn_starts if start.sequence == following), None)
    if started is not None:
        step_type = started.step_execution_id.rpartition("-")[0]
        if step_type == SESSION_FORK_TURN_START_STEP_TYPE:
            return AgentSessionForkPoint(after_message, started.step_execution_id)
        raise AgentSessionForkError(
            "steered_turn",
            f"steering ended the turn of message {after_message} of session "
            f"{source_session_id}, so no point between turns follows it; "
            "fork after another message",
        )
    if any(start.sequence <= after_message for start in turn_starts):
        raise AgentSessionForkError(
            "inside_turn",
            f"message {after_message} of session {source_session_id} is inside a turn; "
            "fork after the last message of a turn",
        )
    raise AgentSessionForkError(
        "point_unavailable",
        f"the events of session {source_session_id} no longer show where the turn after "
        f"message {after_message} started; fork after a later message",
    )


async def read_agent_session_turn_starts(
    client: AsyncClient,
    agent_events: Stream[AgentEvent],
    session_id: str,
    through_sequence: int,
) -> list[AgentSessionTurnStart]:
    """Read the retained ``turn_started`` events, newest first, back to a message sequence.

    It stops at the first turn start at or before ``through_sequence``, at the
    oldest retained event, or after 100 pages of 200 events.

    .. versionadded:: 0.6.13

    Args:
        client: An open Client whose Registry declares ``agent_events``.
        agent_events: The session Flow type's ``AgentEvents`` Stream.
        session_id: The session.
        through_sequence: The message sequence the read reaches back to.

    Returns:
        The turn starts that carry a Step execution ID, newest first.
    """
    starts: list[AgentSessionTurnStart] = []
    page_token = ""
    for _ in range(_MAXIMUM_EVENT_PAGES):
        page = await client.list_stream_messages(
            session_id, agent_events, _EVENT_PAGE_SIZE, page_token
        )
        for message in page.messages:
            start = _turn_start_of(message.value)
            if start is None:
                continue
            starts.append(start)
            if start.sequence <= through_sequence:
                return starts
        if not page.next_page_token:
            return starts
        page_token = page.next_page_token
    return starts


def _turn_start_of(event: AgentEvent) -> AgentSessionTurnStart | None:
    if event.type != AgentEventType.TURN_STARTED or not event.step_execution_id:
        return None
    try:
        payload: Any = json.loads(event.payload)
    except ValueError:
        return None
    sequence = payload.get("sequence") if isinstance(payload, dict) else None
    if not isinstance(sequence, int) or isinstance(sequence, bool):
        return None
    return AgentSessionTurnStart(sequence, event.step_execution_id)


async def _clone_fork(
    client: AsyncClient,
    flow: AgentSessionFlow | AgentSessionForkingFlow,
    source_session_id: str,
    session_id: str,
    point: AgentSessionForkPoint,
    task_queue: str,
    request_id: str | None,
) -> Any:
    reason = f"fork of agent session {source_session_id} after message {point.after_message}"
    if point.step_execution_id is None:
        options = CloneForkFlowOptions(
            id_reuse_policy=IdReusePolicy.DISALLOW,
            task_queue=task_queue,
            request_id=request_id,
            reason=reason,
        )
    else:
        options = CloneForkFlowOptions(
            type=TimeTravelType.STEP_EXECUTION_ID,
            step_execution_id=point.step_execution_id,
            step_method=TimeTravelStepMethod.WAIT_FOR,
            id_reuse_policy=IdReusePolicy.DISALLOW,
            task_queue=task_queue,
            request_id=request_id,
            reason=reason,
        )
    # A null write deletes each Attribute in the change that creates the fork.
    options = options.with_attribute(SESSION_TERMINAL_LEASE, cast(Any, None)).with_attribute(
        SESSION_LAST_TERMINAL_ENVIRONMENT, cast(Any, None)
    )
    try:
        return await client.clone_fork_flow(flow, source_session_id, session_id, options)
    except DexServiceError as error:
        if error.code.name != "FAILED_PRECONDITION":
            raise
        raise AgentSessionForkError(
            "point_refused",
            f"Dex cannot fork session {source_session_id} after message "
            f"{point.after_message}: {error.detail}",
        ) from error


async def _require_fork_between_turns(
    client: AsyncClient, source_session_id: str, session_id: str, point: AgentSessionForkPoint
) -> None:
    """Stop the fork unless it shows no running turn and exactly the kept messages."""
    stored = await client.get_attributes(session_id, AGENT_STATE)
    state: AgentState | None = stored.get(AGENT_STATE)
    if (
        state is not None
        and not state.active_turn_id
        and state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
        and state.next_message_sequence == point.after_message + 1
    ):
        return
    try:
        await client.stop_flow(
            session_id,
            StopFlowOptions(type=StopType.TERMINATE, reason="the fork did not land between turns"),
        )
    except DexServiceError as error:
        _LOGGER.warning("cannot stop the fork: session=%s: %s", session_id, error)
    raise AgentSessionForkError(
        "session_changed",
        f"session {source_session_id} changed while it was forked, so the fork was stopped; "
        "try again",
    )
