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

from collections.abc import Awaitable, Callable
from typing import Protocol

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, AgentSnapshot, MessageRole

# Asks your service about one call: (idempotency key, tool arguments as JSON) -> report text.
OutcomeCheck = Callable[[str, str], Awaitable[str]]


class SessionMessenger(Protocol):
    async def get_agent_snapshot(self, session_id: str) -> AgentSnapshot: ...

    async def send_user_message(self, session_id: str, text: str) -> bool: ...


async def report_unknown_outcomes(
    sessions: SessionMessenger, session_id: str, check: OutcomeCheck
) -> list[str]:
    """Tells the session what really happened to each call with an unknown outcome.

    Returns the reports it sent. A call it reported before is skipped, so running
    it again sends nothing new.
    """
    snapshot = await sessions.get_agent_snapshot(session_id)
    sent = "\n".join(m.text or "" for m in snapshot.messages if m.role == MessageRole.USER)
    reports: list[str] = []
    for message in snapshot.messages:
        if OUTCOME_UNKNOWN_TOOL_RESULT not in (message.tool_output or ""):
            continue
        # Every attempt of the call passed this key to the service.
        key = f"{session_id}/{message.turn_id}/{message.tool_call_id}"
        if key in sent:
            continue
        report = f"{await check(key, message.tool_arguments or '{}')} (tool call {key})"
        await sessions.send_user_message(session_id, report)
        reports.append(report)
    return reports
