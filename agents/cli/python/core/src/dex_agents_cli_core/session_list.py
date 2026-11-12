# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""How the ``sessions`` subcommand loads SearchFlows pages before it sorts them."""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from typing import Generic, Protocol, TypeVar

#: Most sessions that ``sessions`` loads before it sorts them.
SESSION_LIST_ROW_LIMIT = 250
#: Most SearchFlows requests that ``sessions`` sends, so pages without sessions cannot loop forever.
SESSION_LIST_REQUEST_LIMIT = 20

_Session = TypeVar("_Session")
_PageSession_co = TypeVar("_PageSession_co", covariant=True)


class SessionListPage(Protocol[_PageSession_co]):
    """One loaded page: its sessions in Dex order, and its next page token, ``""`` at the end."""

    @property
    def sessions(self) -> Sequence[_PageSession_co]: ...

    @property
    def next_page_token(self) -> str: ...


@dataclass(frozen=True)
class CollectedSessionList(Generic[_Session]):
    """The sessions that ``sessions`` lists."""

    #: At most ``SESSION_LIST_ROW_LIMIT`` sessions, in Dex order.
    sessions: list[_Session]
    #: Whether Dex has sessions that the list leaves out.
    has_more_sessions: bool


async def collect_sessions_for_list(
    load_page: Callable[[str], Awaitable[SessionListPage[_Session]]],
) -> CollectedSessionList[_Session]:
    """Load pages until there are ``SESSION_LIST_ROW_LIMIT`` sessions or no next page token.

    A page may hold fewer sessions than its page size while more exist, so the count of
    pages is no bound.

    Args:
        load_page: Loads the page that a token names; ``""`` names the first page.

    Returns:
        The first sessions in Dex order, and whether more exist.
    """
    sessions: list[_Session] = []
    next_page_token = ""
    for _ in range(SESSION_LIST_REQUEST_LIMIT):
        page = await load_page(next_page_token)
        sessions.extend(page.sessions)
        next_page_token = page.next_page_token
        if not next_page_token or len(sessions) >= SESSION_LIST_ROW_LIMIT:
            break
    is_truncated = len(sessions) > SESSION_LIST_ROW_LIMIT
    return CollectedSessionList(
        sessions=sessions[:SESSION_LIST_ROW_LIMIT],
        has_more_sessions=bool(next_page_token) or is_truncated,
    )
