# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""List agent sessions with their title, last activity time, and turn count.

Every session Flow writes three indexed Attributes as its turns start and end:
``AGENT_SESSION_TITLE``, ``AGENT_SESSION_LAST_ACTIVITY_AT``, and
``AGENT_SESSION_TURN_COUNT``. ``AsyncClient.search_flows`` returns them with
each session, so ``list_agent_sessions`` reads a page of sessions in one call,
without a snapshot per session and without a running Worker.

Example:
    >>> page = await list_agent_sessions(client, "support-agent", page_size=50)
    >>> for session in sort_agent_sessions_by_last_activity(page.sessions):
    ...     print(session.session_id, session.title or "-", session.turn_count)
"""

from __future__ import annotations

import re
from collections.abc import Iterable, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from dex import AsyncClient, Attribute, FlowStatus, SearchFlowEntry, Value, WireKind

from dex_agents.persistence import (
    AGENT_SESSION_LAST_ACTIVITY_AT,
    AGENT_SESSION_TITLE,
    AGENT_SESSION_TURN_COUNT,
)

#: Most Unicode code points ``build_session_title`` keeps.
#:
#: .. versionadded:: 0.6.0
SESSION_TITLE_MAX_CODE_POINTS = 80

#: ``page_size`` that ``list_agent_sessions`` uses when the caller gives none.
#:
#: .. versionadded:: 0.6.0
DEFAULT_SESSION_LIST_PAGE_SIZE = 50

# Only ASCII whitespace, so every core collapses exactly the same characters.
_TITLE_WHITESPACE = re.compile(r"[ \t\n\r\f\v]+")


@dataclass(frozen=True)
class AgentSessionSummary:
    """One session of a list page, read from its search entry.

    ``title``, ``last_activity_at``, and ``turn_count`` are ``None`` when the
    session does not have them. A session that a release before 0.6.0 started
    has no title. It gains the last activity time and the turn count when a
    0.6.0 or later Worker starts or ends one of its turns. Show such a missing
    value as a dash.

    .. versionadded:: 0.6.0

    Attributes:
        session_id: The session ID, which is the Flow ID.
        run_id: The run ID of this Flow run.
        status: The Flow status, such as ``FlowStatus.RUNNING``.
        started_at: When the session started, timezone-aware.
        closed_at: When the session closed, or ``None`` while it runs.
        title: ``AGENT_SESSION_TITLE``, or ``None``.
        last_activity_at: ``AGENT_SESSION_LAST_ACTIVITY_AT`` as a timezone-aware
            time, or ``None``.
        turn_count: ``AGENT_SESSION_TURN_COUNT``, or ``None``.
    """

    session_id: str
    run_id: str
    status: FlowStatus
    started_at: datetime
    closed_at: datetime | None
    title: str | None
    last_activity_at: datetime | None
    turn_count: int | None


@dataclass(frozen=True)
class AgentSessionSummaryPage:
    """One page of ``list_agent_sessions``.

    .. versionadded:: 0.6.0

    Attributes:
        sessions: The sessions of the page, in Dex search order: running
            sessions first, then the most recently closed, then the most
            recently started.
        next_page_token: Pass it to ``list_agent_sessions`` for the next page;
            ``""`` on the last page.
    """

    sessions: Sequence[AgentSessionSummary]
    next_page_token: str


def build_session_title(first_message_text: str) -> str | None:
    """Return the title a session takes from its first user message.

    Each run of ASCII spaces, tabs, and line breaks becomes one space, leading
    and trailing spaces are removed, and the text is cut to
    ``SESSION_TITLE_MAX_CODE_POINTS`` code points, without a trailing space.
    The TypeScript core gives the same title for the same text.

    .. versionadded:: 0.6.0

    Args:
        first_message_text: Text of the session's first user message.

    Returns:
        The title, or ``None`` when the text has no other character.
    """
    collapsed = _TITLE_WHITESPACE.sub(" ", first_message_text).strip(" ")
    title = collapsed[:SESSION_TITLE_MAX_CODE_POINTS].rstrip(" ")
    return title or None


async def list_agent_sessions(
    client: AsyncClient,
    flow_type: str,
    *,
    page_size: int = DEFAULT_SESSION_LIST_PAGE_SIZE,
    next_page_token: str = "",
    title_search: str | None = None,
) -> AgentSessionSummaryPage:
    """Return one page of the sessions of a session Flow type.

    It makes one ``AsyncClient.search_flows`` call. It needs no Worker and reads
    no snapshot. Dex orders the page; it cannot sort by last activity, so sort
    the sessions you loaded with ``sort_agent_sessions_by_last_activity``.

    ``title_search`` keeps only the sessions whose title contains every word of
    it, compared without case. A word is a run of letters and digits, and only
    whole words match: ``flaky`` finds "Fix the flaky test", ``flak`` does not.
    Sessions without a title never match. Pass the same ``title_search`` with
    each page token.

    .. versionadded:: 0.6.0

    Example:
        >>> page = await list_agent_sessions(client, "support-agent", title_search="refund")
        >>> while page.next_page_token:
        ...     page = await list_agent_sessions(
        ...         client, "support-agent", title_search="refund",
        ...         next_page_token=page.next_page_token,
        ...     )

    Args:
        client: An open ``AsyncClient``. Its Registry does not need the session
            Flow.
        flow_type: The session Flow type, such as ``AgentSessionFlow.flow_type``.
        page_size: Most sessions in the page. Defaults to 50.
        next_page_token: ``AgentSessionSummaryPage.next_page_token`` of the
            previous page, or ``""`` for the first page.
        title_search: Words the title must contain, or ``None`` or blank for
            every session.

    Returns:
        The page of sessions.

    Raises:
        ValueError: If ``page_size`` is negative.
        DexServiceError: If Dex rejects the search. A title search fails with
            ``INVALID_ARGUMENT`` while no Worker of 0.6.0 or later has started
            on the server, because the title index does not exist yet.
    """
    query = f"FlowType = {_quote(flow_type)}"
    if title_search is not None and title_search.strip():
        query += f" AND {_TITLE_INDEX_KEY} = {_quote(title_search)}"
    page = await client.search_flows(query, page_size, next_page_token)
    return AgentSessionSummaryPage(
        sessions=[_session_summary(entry) for entry in page.flows],
        next_page_token=page.next_page_token,
    )


def sort_agent_sessions_by_last_activity(
    sessions: Iterable[AgentSessionSummary],
) -> list[AgentSessionSummary]:
    """Return sessions with the most recent activity first.

    A session without a last activity time sorts by its start time. Sessions
    with the same time sort by session ID.

    .. versionadded:: 0.6.0

    Args:
        sessions: Sessions from one or more ``list_agent_sessions`` pages.

    Returns:
        A new list, most recent first.
    """
    by_session_id = sorted(sessions, key=lambda session: session.session_id)
    return sorted(
        by_session_id,
        key=lambda session: session.last_activity_at or session.started_at,
        reverse=True,
    )


def _session_summary(entry: SearchFlowEntry) -> AgentSessionSummary:
    attributes = entry.indexed_attributes
    title = _string_of(attributes.get(_TITLE_INDEX_KEY))
    last_activity_text = _string_of(attributes.get(_LAST_ACTIVITY_INDEX_KEY))
    turn_count = attributes.get(_TURN_COUNT_INDEX_KEY)
    return AgentSessionSummary(
        session_id=entry.flow_id,
        run_id=entry.run_id,
        status=entry.status,
        started_at=entry.started_at,
        closed_at=entry.closed_at,
        title=title,
        last_activity_at=_time_of(last_activity_text),
        turn_count=(
            turn_count.data
            if turn_count is not None
            and turn_count.kind is WireKind.INT64
            and isinstance(turn_count.data, int)
            else None
        ),
    )


def _string_of(value: Value | None) -> str | None:
    if value is None or value.kind is not WireKind.STRING or not isinstance(value.data, str):
        return None
    return value.data


def _time_of(text: str | None) -> datetime | None:
    if text is None:
        return None
    try:
        moment = datetime.fromisoformat(text)
    except ValueError:
        return None
    return moment if moment.tzinfo is not None else None


def _index_key_of(attribute: Attribute[Any]) -> str:
    if attribute.index is None or not attribute.index.index_key:
        raise TypeError(f"{attribute.name} is not indexed under an explicit key")
    return attribute.index.index_key


def _quote(text: str) -> str:
    return '"' + text.replace("\\", "\\\\").replace('"', '\\"') + '"'


_TITLE_INDEX_KEY = _index_key_of(AGENT_SESSION_TITLE)
_LAST_ACTIVITY_INDEX_KEY = _index_key_of(AGENT_SESSION_LAST_ACTIVITY_AT)
_TURN_COUNT_INDEX_KEY = _index_key_of(AGENT_SESSION_TURN_COUNT)
