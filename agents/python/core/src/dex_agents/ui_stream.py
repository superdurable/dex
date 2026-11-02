# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Server-Sent Events and snapshot JSON that let a browser render a live agent session.

A browser renders a session from two endpoints that an application mounts in its
own web framework:

* A snapshot endpoint returns ``read_agent_event_stream_snapshot(...).to_json()``:
  the ``get_agent_snapshot`` result plus the Dex resume token to open the event
  stream from.
* An event endpoint returns ``stream_agent_event_frames`` as a
  ``text/event-stream`` response. Each frame's ``id`` is the event's Dex resume
  token, so a reconnecting ``EventSource`` sends it back as ``Last-Event-ID``
  and continues with the next event.

The functions depend on no web framework. ``stream_agent_event_frames`` and
``read_agent_event_stream_snapshot`` use an ``AsyncClient`` for ASGI servers;
the ``_sync`` variants use a ``Client`` for WSGI servers. The package README
shows FastAPI, Starlette, Quart, and Flask routes.
"""

from __future__ import annotations

import asyncio
import json
import time
from collections.abc import AsyncGenerator, Generator, Mapping
from dataclasses import dataclass, fields, is_dataclass
from datetime import timedelta
from enum import Enum
from types import MappingProxyType
from typing import Any

from dex import (
    AsyncClient,
    Client,
    DexServiceError,
    LongPollTimeoutError,
    StreamMessage,
    StreamMessagesPage,
)

from dex_agents.session_flow import AgentSessionFlow
from dex_agents.values import AgentEvent, AgentSnapshot

#: Response headers of the ``text/event-stream`` event endpoint.
AGENT_EVENT_STREAM_HEADERS: Mapping[str, str] = MappingProxyType(
    {
        "content-type": "text/event-stream",
        "cache-control": "no-cache",
        "x-accel-buffering": "no",
    }
)

#: Default long-poll duration of one ``AgentEvents`` read: 10 seconds.
DEFAULT_EVENT_POLL_TIMEOUT = timedelta(seconds=10)

#: SSE comment sent after an empty long poll; it keeps proxies from closing an idle response.
KEEP_ALIVE_FRAME = ": keep-alive\n\n"

_RESUME_TOKEN_PROBE_TIMEOUT = timedelta(seconds=1)
_TURN_START_SEARCH_PAGE_SIZE = 500
_TURN_START_SEARCH_PAGE_LIMIT = 20


class InvalidAgentEventResumeTokenError(ValueError):
    """Report that Dex rejected a resume token, such as one of another session.

    ``stream_agent_event_frames`` and ``stream_agent_event_frames_sync`` raise
    it before the response starts. Answer 400, so an ``EventSource`` stops
    reconnecting with the token, and let the browser load the snapshot again.

    Attributes:
        session_id: The session whose stream was requested.
    """

    def __init__(self, session_id: str) -> None:
        """Create the error.

        Args:
            session_id: The session whose stream was requested.
        """
        super().__init__(f"the resume token does not belong to session {session_id}")
        self.session_id = session_id


@dataclass(frozen=True, kw_only=True)
class AgentEventStreamSnapshot:
    """Hold a session snapshot and the Dex resume token to open its event stream from.

    ``read_agent_event_stream_snapshot`` returns it. Serve ``to_json()`` from the
    snapshot endpoint, then open the event endpoint with ``resume_token``.

    Attributes:
        snapshot: The ``get_agent_snapshot`` result.
        resume_token: Dex resume token for the event stream. While a turn is
            active it replays that turn's events from its first retained event;
            otherwise it starts after the newest event read before the snapshot.
    """

    snapshot: AgentSnapshot
    resume_token: str

    def to_json(self) -> dict[str, Any]:
        """Return the snapshot and token as snake_case wire JSON.

        Returns:
            ``{"snapshot": ..., "resume_token": ...}``, with the snapshot in its
            ``AgentSnapshot`` wire format, the same JSON as the TypeScript core.
        """
        return {
            "snapshot": agent_snapshot_to_json(self.snapshot),
            "resume_token": self.resume_token,
        }


def agent_event_to_json(event: AgentEvent) -> dict[str, Any]:
    """Return an ``AgentEvent`` as its snake_case wire JSON.

    Args:
        event: Event read from the ``AgentEvents`` Stream.

    Returns:
        A new dict with ``type``, ``turn_id``, ``attempt``, ``step_execution_id``,
        and ``payload``. ``payload`` stays JSON object text, as on the Stream.
    """
    return {
        "type": event.type,
        "turn_id": event.turn_id,
        "attempt": event.attempt,
        "step_execution_id": event.step_execution_id,
        "payload": event.payload,
    }


def agent_snapshot_to_json(snapshot: AgentSnapshot) -> dict[str, Any]:
    """Return an ``AgentSnapshot`` as its snake_case wire JSON.

    Use it for a snapshot endpoint that serves the snapshot without a resume
    token, or inside another response.

    Args:
        snapshot: The ``get_agent_snapshot`` result.

    Returns:
        A new dict of plain JSON values: enums become their string values, and
        the field names are the wire names.
    """
    converted = _convert_wire_value(snapshot)
    if not isinstance(converted, dict):
        raise TypeError("AgentSnapshot must convert to a JSON object")
    return converted


def format_agent_event_frame(message: StreamMessage[AgentEvent]) -> str:
    """Format one ``AgentEvents`` message as a Server-Sent Event.

    The frame has no ``event:`` field, so ``EventSource.onmessage`` receives every
    event; the browser switches on ``type`` in the data.

    Args:
        message: Message read from the ``AgentEvents`` Stream.

    Returns:
        ``id: <resume token>``, then ``data: <AgentEvent wire JSON>``, then a
        blank line.
    """
    data = json.dumps(agent_event_to_json(message.value), separators=(",", ":"))
    return f"id: {message.resume_token}\ndata: {data}\n\n"


async def stream_agent_event_frames(
    client: AsyncClient,
    flow: AgentSessionFlow,
    session_id: str,
    *,
    resume_token: str = "",
    poll_timeout: timedelta = DEFAULT_EVENT_POLL_TIMEOUT,
) -> AsyncGenerator[str, None]:
    """Check a resume token, then return a generator of a session's ``AgentEvents`` frames.

    Await it before the response starts. It reads once, with a long poll of up
    to one second, so a token that Dex rejects, such as one of another session,
    raises ``InvalidAgentEventResumeTokenError`` while the route can still
    answer 400; an ``EventSource`` stops reconnecting on a 400, and the browser
    loads the snapshot again. A token from before a Stream Store restart is
    accepted and continues at the oldest retained event.

    Each event becomes one ``format_agent_event_frame`` frame whose ``id`` is its
    Dex resume token. Pass the ``Last-Event-ID`` request header as
    ``resume_token`` to continue after the last event the browser received, with
    no repeated or missing event while the Stream retains them. After each empty
    long poll the generator yields ``KEEP_ALIVE_FRAME``; with the Server's Stream
    Store disabled it only yields those. It never ends on its own; the web
    framework closes it when the browser disconnects.

    The browser applies the event rules of the session contract. Within one
    ``step_execution_id`` it keeps only the highest ``attempt`` and drops the
    partial text of lower attempts, so a Step retried after a crash shows only
    its new attempt. After ``turn_completed`` or ``turn_failed`` it replaces the
    turn's live text with the stored messages. On every connection after a gap,
    load ``read_agent_event_stream_snapshot`` first and open the stream from its
    ``resume_token``. The session ID usually comes from the browser, so the
    route must check that the caller may read that session.

    Example:
        >>> from fastapi import FastAPI, Request, Response
        >>> from fastapi.responses import StreamingResponse
        >>> @app.get("/sessions/{session_id}/events")
        ... async def get_session_events(session_id: str, request: Request):
        ...     resume_token = request.headers.get("last-event-id") or (
        ...         request.query_params.get("resume_token", "")
        ...     )
        ...     try:
        ...         frames = await stream_agent_event_frames(
        ...             client, flow, session_id, resume_token=resume_token
        ...         )
        ...     except InvalidAgentEventResumeTokenError:
        ...         return Response(status_code=400)
        ...     return StreamingResponse(frames, headers=dict(AGENT_EVENT_STREAM_HEADERS))

    Args:
        client: An open ``AsyncClient`` whose Registry contains ``flow``. The
            caller keeps ownership.
        flow: The registered agent session Flow.
        session_id: Flow ID of the session.
        resume_token: Dex resume token of the last event the browser received,
            or ``""`` for the oldest retained event.
        poll_timeout: Long-poll duration of one Stream read, in whole seconds.

    Returns:
        An async generator of SSE frames and keep-alive comments, as ``str``.

    Raises:
        ValueError: If ``poll_timeout`` is not a positive whole number of seconds.
        InvalidAgentEventResumeTokenError: If Dex rejects ``resume_token``.
        DexServiceError: If the first Stream read fails for another reason; the
            generator raises it for a later read.
    """
    _require_poll_timeout(poll_timeout)
    try:
        first_message = await _read_next_agent_event(
            client, flow, session_id, resume_token, _RESUME_TOKEN_PROBE_TIMEOUT
        )
    except DexServiceError as error:
        if _is_resume_token_rejected(error):
            raise InvalidAgentEventResumeTokenError(session_id) from error
        raise
    return _generate_agent_event_frames(
        client, flow, session_id, resume_token, poll_timeout, first_message
    )


async def _generate_agent_event_frames(
    client: AsyncClient,
    flow: AgentSessionFlow,
    session_id: str,
    resume_token: str,
    poll_timeout: timedelta,
    first_message: StreamMessage[AgentEvent] | None,
) -> AsyncGenerator[str, None]:
    message = first_message
    while True:
        if message is None:
            message = await _read_next_agent_event(
                client, flow, session_id, resume_token, poll_timeout
            )
        if message is None:
            yield KEEP_ALIVE_FRAME
            continue
        resume_token = message.resume_token
        yield format_agent_event_frame(message)
        message = None


def stream_agent_event_frames_sync(
    client: Client,
    flow: AgentSessionFlow,
    session_id: str,
    *,
    resume_token: str = "",
    poll_timeout: timedelta = DEFAULT_EVENT_POLL_TIMEOUT,
) -> Generator[str, None, None]:
    """Check a resume token, then return a generator of ``AgentEvents`` frames, blocking.

    It behaves like ``stream_agent_event_frames`` with a synchronous ``Client``,
    for WSGI servers such as Flask: it raises
    ``InvalidAgentEventResumeTokenError`` before returning the generator. Each
    Stream read blocks the calling thread for up to ``poll_timeout``, so serve
    it from a server with one thread or greenlet per streaming response.

    Example:
        >>> @app.get("/sessions/<session_id>/events")
        ... def get_session_events(session_id: str):
        ...     resume_token = request.headers.get("Last-Event-ID") or (
        ...         request.args.get("resume_token", "")
        ...     )
        ...     try:
        ...         frames = stream_agent_event_frames_sync(
        ...             client, flow, session_id, resume_token=resume_token
        ...         )
        ...     except InvalidAgentEventResumeTokenError:
        ...         return Response(status=400)
        ...     return Response(frames, headers=dict(AGENT_EVENT_STREAM_HEADERS))

    Args:
        client: An open ``Client`` whose Registry contains ``flow``.
        flow: The registered agent session Flow.
        session_id: Flow ID of the session.
        resume_token: Dex resume token of the last event the browser received,
            or ``""`` for the oldest retained event.
        poll_timeout: Long-poll duration of one Stream read, in whole seconds.

    Returns:
        A generator of SSE frames and keep-alive comments, as ``str``.

    Raises:
        ValueError: If ``poll_timeout`` is not a positive whole number of seconds.
        InvalidAgentEventResumeTokenError: If Dex rejects ``resume_token``.
        DexServiceError: If the first Stream read fails for another reason; the
            generator raises it for a later read.
    """
    _require_poll_timeout(poll_timeout)
    try:
        first_message = _read_next_agent_event_sync(
            client, flow, session_id, resume_token, _RESUME_TOKEN_PROBE_TIMEOUT
        )
    except DexServiceError as error:
        if _is_resume_token_rejected(error):
            raise InvalidAgentEventResumeTokenError(session_id) from error
        raise
    return _generate_agent_event_frames_sync(
        client, flow, session_id, resume_token, poll_timeout, first_message
    )


def _generate_agent_event_frames_sync(
    client: Client,
    flow: AgentSessionFlow,
    session_id: str,
    resume_token: str,
    poll_timeout: timedelta,
    first_message: StreamMessage[AgentEvent] | None,
) -> Generator[str, None, None]:
    message = first_message
    while True:
        if message is None:
            message = _read_next_agent_event_sync(
                client, flow, session_id, resume_token, poll_timeout
            )
        if message is None:
            yield KEEP_ALIVE_FRAME
            continue
        resume_token = message.resume_token
        yield format_agent_event_frame(message)
        message = None


async def read_agent_event_stream_snapshot(
    client: AsyncClient,
    flow: AgentSessionFlow,
    session_id: str,
) -> AgentEventStreamSnapshot:
    """Read a session snapshot and the Dex resume token to open its event stream from.

    Call it when the browser connects, and again after any gap. It reads the
    newest event first and the snapshot second, so events after the token may
    repeat what the snapshot shows, but none is missing. While a turn is active
    the token replays that turn's events, so the browser can rebuild its live
    text. The ``get_agent_snapshot`` RPC needs a running Worker.

    Example:
        >>> @app.get("/sessions/{session_id}/snapshot")
        ... async def get_session_snapshot(session_id: str) -> dict[str, Any]:
        ...     value = await read_agent_event_stream_snapshot(client, flow, session_id)
        ...     return value.to_json()

    Args:
        client: An open ``AsyncClient`` whose Registry contains ``flow``.
        flow: The registered agent session Flow.
        session_id: Flow ID of the session.

    Returns:
        The snapshot and its resume token.

    Raises:
        FlowNotActiveOrNotFoundError: If the session does not exist.
        DexServiceError: If the Stream read or the RPC fails.
    """
    try:
        tail_page = await client.list_stream_messages(session_id, flow.agent_events, 1)
    except DexServiceError as error:
        if not _is_stream_store_disabled(error):
            raise
        snapshot = await client.invoke_rpc(flow.get_agent_snapshot, session_id)
        return AgentEventStreamSnapshot(snapshot=snapshot, resume_token="")
    tail_resume_token = _newest_resume_token(tail_page)
    snapshot = await client.invoke_rpc(flow.get_agent_snapshot, session_id)
    active_turn_id = None if snapshot.state is None else snapshot.state.active_turn_id
    if active_turn_id is None:
        return AgentEventStreamSnapshot(snapshot=snapshot, resume_token=tail_resume_token)
    search = _TurnStartSearch(active_turn_id)
    page_token = ""
    for _ in range(_TURN_START_SEARCH_PAGE_LIMIT):
        page = await client.list_stream_messages(
            session_id, flow.agent_events, _TURN_START_SEARCH_PAGE_SIZE, page_token
        )
        step = search.inspect_page(page)
        if step.resume_token is not None:
            return AgentEventStreamSnapshot(snapshot=snapshot, resume_token=step.resume_token)
        if step.older_page_token is not None:
            older_page = await client.list_stream_messages(
                session_id, flow.agent_events, 1, step.older_page_token
            )
            return AgentEventStreamSnapshot(
                snapshot=snapshot, resume_token=_newest_resume_token(older_page)
            )
        if step.next_page_token is None:
            break
        page_token = step.next_page_token
    return AgentEventStreamSnapshot(snapshot=snapshot, resume_token=tail_resume_token)


def read_agent_event_stream_snapshot_sync(
    client: Client,
    flow: AgentSessionFlow,
    session_id: str,
) -> AgentEventStreamSnapshot:
    """Read a session snapshot and its event stream resume token, blocking.

    It behaves like ``read_agent_event_stream_snapshot`` with a synchronous
    ``Client``, for WSGI servers such as Flask.

    Example:
        >>> @app.get("/sessions/<session_id>/snapshot")
        ... def get_session_snapshot(session_id: str):
        ...     return read_agent_event_stream_snapshot_sync(client, flow, session_id).to_json()

    Args:
        client: An open ``Client`` whose Registry contains ``flow``.
        flow: The registered agent session Flow.
        session_id: Flow ID of the session.

    Returns:
        The snapshot and its resume token.

    Raises:
        FlowNotActiveOrNotFoundError: If the session does not exist.
        DexServiceError: If the Stream read or the RPC fails.
    """
    try:
        tail_page = client.list_stream_messages(session_id, flow.agent_events, 1)
    except DexServiceError as error:
        if not _is_stream_store_disabled(error):
            raise
        snapshot = client.invoke_rpc(flow.get_agent_snapshot, session_id)
        return AgentEventStreamSnapshot(snapshot=snapshot, resume_token="")
    tail_resume_token = _newest_resume_token(tail_page)
    snapshot = client.invoke_rpc(flow.get_agent_snapshot, session_id)
    active_turn_id = None if snapshot.state is None else snapshot.state.active_turn_id
    if active_turn_id is None:
        return AgentEventStreamSnapshot(snapshot=snapshot, resume_token=tail_resume_token)
    search = _TurnStartSearch(active_turn_id)
    page_token = ""
    for _ in range(_TURN_START_SEARCH_PAGE_LIMIT):
        page = client.list_stream_messages(
            session_id, flow.agent_events, _TURN_START_SEARCH_PAGE_SIZE, page_token
        )
        step = search.inspect_page(page)
        if step.resume_token is not None:
            return AgentEventStreamSnapshot(snapshot=snapshot, resume_token=step.resume_token)
        if step.older_page_token is not None:
            older_page = client.list_stream_messages(
                session_id, flow.agent_events, 1, step.older_page_token
            )
            return AgentEventStreamSnapshot(
                snapshot=snapshot, resume_token=_newest_resume_token(older_page)
            )
        if step.next_page_token is None:
            break
        page_token = step.next_page_token
    return AgentEventStreamSnapshot(snapshot=snapshot, resume_token=tail_resume_token)


@dataclass(frozen=True)
class _TurnStartSearchStep:
    """What a turn start search needs next; exactly one field is set, or none when it gave up."""

    resume_token: str | None = None
    older_page_token: str | None = None
    next_page_token: str | None = None


class _TurnStartSearch:
    """Finds the position just before a turn's first retained event, reading pages newest first."""

    def __init__(self, turn_id: str) -> None:
        self._turn_id = turn_id
        self._turn_number = _parse_turn_number(turn_id)
        self._has_turn_event = False

    def inspect_page(self, page: StreamMessagesPage[AgentEvent]) -> _TurnStartSearchStep:
        messages = page.messages
        for index, message in enumerate(messages):
            event = message.value
            if event.turn_id == self._turn_id:
                self._has_turn_event = True
                if event.type != "turn_started":
                    continue
                if index + 1 < len(messages):
                    return _TurnStartSearchStep(resume_token=messages[index + 1].resume_token)
                if page.next_page_token == "":
                    return _TurnStartSearchStep(resume_token="")
                return _TurnStartSearchStep(older_page_token=page.next_page_token)
            # Every event of the turn is newer than an event of an earlier turn.
            if self._is_earlier_turn(event.turn_id):
                return (
                    _TurnStartSearchStep(resume_token=message.resume_token)
                    if self._has_turn_event
                    else _TurnStartSearchStep()
                )
        if page.next_page_token == "":
            return (
                _TurnStartSearchStep(resume_token="")
                if self._has_turn_event
                else _TurnStartSearchStep()
            )
        return _TurnStartSearchStep(next_page_token=page.next_page_token)

    def _is_earlier_turn(self, turn_id: str | None) -> bool:
        if turn_id is None or self._turn_number is None:
            return False
        other_turn_number = _parse_turn_number(turn_id)
        return other_turn_number is not None and other_turn_number < self._turn_number


def _parse_turn_number(turn_id: str) -> int | None:
    prefix, _, number = turn_id.partition("-")
    is_ascii_number = number != "" and all("0" <= character <= "9" for character in number)
    return int(number) if prefix == "turn" and is_ascii_number else None


async def _read_next_agent_event(
    client: AsyncClient,
    flow: AgentSessionFlow,
    session_id: str,
    resume_token: str,
    poll_timeout: timedelta,
) -> StreamMessage[AgentEvent] | None:
    try:
        return await client.read_stream(session_id, flow.agent_events, resume_token, poll_timeout)
    except LongPollTimeoutError:
        return None
    except DexServiceError as error:
        if not _is_stream_store_disabled(error):
            raise
        # A disabled Stream Store holds no events; wait as an empty poll would.
        await asyncio.sleep(min(poll_timeout, _RESUME_TOKEN_PROBE_TIMEOUT).total_seconds())
        return None


def _read_next_agent_event_sync(
    client: Client,
    flow: AgentSessionFlow,
    session_id: str,
    resume_token: str,
    poll_timeout: timedelta,
) -> StreamMessage[AgentEvent] | None:
    try:
        return client.read_stream(session_id, flow.agent_events, resume_token, poll_timeout)
    except LongPollTimeoutError:
        return None
    except DexServiceError as error:
        if not _is_stream_store_disabled(error):
            raise
        time.sleep(min(poll_timeout, _RESUME_TOKEN_PROBE_TIMEOUT).total_seconds())
        return None


def _is_resume_token_rejected(error: DexServiceError) -> bool:
    return str(error.code.name) == "INVALID_ARGUMENT"


def _is_stream_store_disabled(error: DexServiceError) -> bool:
    return str(error.code.name) == "FAILED_PRECONDITION"


def _newest_resume_token(page: StreamMessagesPage[AgentEvent]) -> str:
    return page.messages[0].resume_token if page.messages else ""


def _require_poll_timeout(poll_timeout: timedelta) -> None:
    seconds = poll_timeout.total_seconds()
    if seconds < 1 or not seconds.is_integer():
        raise ValueError("poll_timeout must be a positive whole number of seconds")


def _convert_wire_value(value: Any) -> Any:
    if is_dataclass(value) and not isinstance(value, type):
        return {
            field.name: _convert_wire_value(getattr(value, field.name)) for field in fields(value)
        }
    if isinstance(value, Enum):
        return value.value
    if isinstance(value, list | tuple):
        return [_convert_wire_value(item) for item in value]
    return value
