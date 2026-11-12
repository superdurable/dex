# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Reports the real outcome of tool calls that the session recorded as outcome_unknown."""
from __future__ import annotations

import asyncio
from typing import Protocol

import grpc  # type: ignore[import-untyped]
from dex import DexServiceError
from dex_agents import AgentSnapshot, MessageRole

# A lost response leaves a send's outcome unknown; these codes mean "check first".
UNCERTAIN_CODES = (grpc.StatusCode.UNAVAILABLE, grpc.StatusCode.DEADLINE_EXCEEDED)


class MessageSessions(Protocol):
    async def get_agent_snapshot(self, session_id: str) -> AgentSnapshot: ...

    async def send_user_message(self, session_id: str, text: str) -> bool: ...

    async def delete_queued_message(self, session_id: str, message_id: str) -> bool: ...


async def send_user_message_once(
    sessions: MessageSessions,
    session_id: str,
    text: str,
    *,
    attempts: int = 5,
    retry_delay_seconds: float = 1.0,
) -> None:
    """Queues one user message, retrying a failed send without queuing it twice.

    Before each retry it reads the session, and stops when a new copy of the
    text is there. At the end it deletes any extra queued copy. It tells
    messages apart by their text, so call it once for each message a user sends.

    Raises:
        DexServiceError: When no attempt reached a Worker, or on any other error.
    """
    copies_before = _copies(await sessions.get_agent_snapshot(session_id), text)
    for attempt in range(1, attempts + 1):
        try:
            await sessions.send_user_message(session_id, text)
            break
        except DexServiceError as error:
            if error.code not in UNCERTAIN_CODES or attempt == attempts:
                raise
        await asyncio.sleep(retry_delay_seconds)
        if _copies(await sessions.get_agent_snapshot(session_id), text) > copies_before:
            break
    await _delete_extra_copies(sessions, session_id, text, copies_before + 1)


async def _delete_extra_copies(
    sessions: MessageSessions, session_id: str, text: str, wanted: int
) -> None:
    snapshot = await sessions.get_agent_snapshot(session_id)
    extra = _copies(snapshot, text) - wanted
    queued = [item for item in snapshot.queued_messages if item.message.text == text]
    if extra <= 0:
        return
    for item in reversed(queued[max(0, len(queued) - extra) :]):
        await sessions.delete_queued_message(session_id, item.message_id)


def _copies(snapshot: AgentSnapshot, text: str) -> int:
    delivered = sum(
        1 for message in snapshot.messages if message.role == MessageRole.USER and message.text == text
    )
    queued = sum(1 for item in snapshot.queued_messages if item.message.text == text)
    return delivered + queued
