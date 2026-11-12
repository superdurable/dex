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

from __future__ import annotations

import grpc  # type: ignore[import-untyped]
import pytest
from dex import DexServiceError, ErrorSubStatus
from dex_agents import AgentSnapshot, AgentUserMessage, PendingUserMessage

from agent_harness_examples.quick_start.safe_send import send_user_message_once


async def test_a_lost_response_does_not_queue_the_message_twice() -> None:
    sessions = _FakeSessions(failures=["queued-then-unavailable"])

    await send_user_message_once(sessions, "session-1", "Cancel order 1002", retry_delay_seconds=0)

    assert sessions.queued_texts() == ["Cancel order 1002"]
    assert sessions.send_calls == 1


async def test_a_send_that_reached_no_worker_is_retried() -> None:
    sessions = _FakeSessions(failures=["unavailable", "unavailable"])

    await send_user_message_once(sessions, "session-1", "Cancel order 1002", retry_delay_seconds=0)

    assert sessions.queued_texts() == ["Cancel order 1002"]
    assert sessions.send_calls == 3


async def test_an_extra_queued_copy_is_deleted() -> None:
    sessions = _FakeSessions(failures=["queued-twice"])

    await send_user_message_once(sessions, "session-1", "Cancel order 1002", retry_delay_seconds=0)

    assert sessions.queued_texts() == ["Cancel order 1002"]


async def test_other_errors_are_raised() -> None:
    sessions = _FakeSessions(failures=["invalid"])

    with pytest.raises(DexServiceError):
        await send_user_message_once(sessions, "session-1", "hi", retry_delay_seconds=0)


class _FakeSessions:
    """A session queue whose sends fail in scripted ways."""

    def __init__(self, failures: list[str]) -> None:
        self.failures = failures
        self.queued: list[PendingUserMessage] = []
        self.send_calls = 0

    def queued_texts(self) -> list[str]:
        return [item.message.text for item in self.queued]

    async def get_agent_snapshot(self, session_id: str) -> AgentSnapshot:
        return AgentSnapshot(state=None, queued_messages=list(self.queued))

    async def send_user_message(self, session_id: str, text: str) -> bool:
        self.send_calls += 1
        failure = self.failures.pop(0) if self.failures else None
        if failure in (None, "queued-then-unavailable", "queued-twice"):
            self._queue(text)
        if failure == "queued-twice":
            self._queue(text)
        if failure in ("unavailable", "queued-then-unavailable"):
            raise _error(grpc.StatusCode.UNAVAILABLE)
        if failure == "invalid":
            raise _error(grpc.StatusCode.INVALID_ARGUMENT)
        return True

    async def delete_queued_message(self, session_id: str, message_id: str) -> bool:
        before = len(self.queued)
        self.queued = [item for item in self.queued if item.message_id != message_id]
        return len(self.queued) < before

    def _queue(self, text: str) -> None:
        self.queued.append(
            PendingUserMessage(message_id=f"m-{len(self.queued) + 1}", message=AgentUserMessage(text=text))
        )


def _error(code: grpc.StatusCode) -> DexServiceError:
    return DexServiceError(code, ErrorSubStatus.UNCATEGORIZED, "test", "send_user_message", "session-1")
