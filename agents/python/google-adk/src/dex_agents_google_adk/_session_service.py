# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""An ADK session service that holds one session for one Dex Step attempt."""

from __future__ import annotations

from typing import Any

from google.adk.events import Event
from google.adk.sessions import BaseSessionService, Session
from google.adk.sessions.base_session_service import GetSessionConfig, ListSessionsResponse


class TurnSessionService(BaseSessionService):
    """Serve one ADK session rebuilt from Dex state and collect the events ADK appends.

    The Dex session Flow owns durable history, so this service never persists
    anything. The adapter reads ``appended_events`` after the run and returns
    them to the session Flow.
    """

    def __init__(self, session: Session) -> None:
        self._session = session
        self.appended_events: list[Event] = []

    async def create_session(
        self,
        *,
        app_name: str,
        user_id: str,
        state: dict[str, Any] | None = None,
        session_id: str | None = None,
    ) -> Session:
        raise RuntimeError(
            f"a Dex agent session serves only ADK session {self._session.id}; "
            "it cannot create another"
        )

    async def get_session(
        self,
        *,
        app_name: str,
        user_id: str,
        session_id: str,
        config: GetSessionConfig | None = None,
    ) -> Session | None:
        if (app_name, user_id, session_id) != (
            self._session.app_name,
            self._session.user_id,
            self._session.id,
        ):
            return None
        return self._session

    async def list_sessions(
        self, *, app_name: str, user_id: str | None = None
    ) -> ListSessionsResponse:
        if app_name != self._session.app_name or user_id not in (None, self._session.user_id):
            return ListSessionsResponse()
        return ListSessionsResponse(sessions=[self._session.model_copy(update={"events": []})])

    async def delete_session(self, *, app_name: str, user_id: str, session_id: str) -> None:
        raise RuntimeError("a Dex agent session ends only when its Flow stops")

    async def append_event(self, session: Session, event: Event) -> Event:
        appended = await super().append_event(session, event)
        if appended.partial:
            return appended
        if session is not self._session:
            self._session.events.append(appended)
            self._session.state.update(appended.actions.state_delta)
        self.appended_events.append(appended)
        return appended
