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

"""Polling helpers for AI Agent session tests, on the public AgentSessionClient."""

from __future__ import annotations

from collections.abc import Callable
from datetime import timedelta
from typing import TypeVar

from dex import DexServiceError
from dex_agents import (
    AgentInterrupt,
    AgentMessage,
    AgentSessionClient,
    AgentSessionStatus,
    AgentSnapshot,
    InterruptKind,
    PlanStatus,
    ToolCallStatus,
)
from tests.integ.conftest import WAIT_TIMEOUT, wait_until

SelectedValue = TypeVar("SelectedValue")


async def send_user_message(
    sessions: AgentSessionClient, session_id: str, text: str, plan_mode: bool = False
) -> None:
    assert await sessions.send_user_message(session_id, text, plan_mode=plan_mode)


async def wait_for_snapshot(
    sessions: AgentSessionClient,
    session_id: str,
    description: str,
    select: Callable[[AgentSnapshot], SelectedValue | None],
    timeout: timedelta = WAIT_TIMEOUT,
) -> SelectedValue:
    async def selected() -> SelectedValue | None:
        try:
            snapshot = await sessions.get_agent_snapshot(session_id)
        except DexServiceError:
            # A restarting engine or Worker answers nothing for a moment.
            return None
        return select(snapshot)

    return await wait_until(f"{session_id} {description}", selected, timeout)


async def list_all_messages(sessions: AgentSessionClient, session_id: str) -> list[AgentMessage]:
    return (await sessions.list_agent_messages(session_id, limit=200)).messages


async def wait_for_message_text(
    sessions: AgentSessionClient,
    session_id: str,
    expected: str,
    timeout: timedelta = WAIT_TIMEOUT,
) -> AgentMessage:
    async def matching_message() -> AgentMessage | None:
        try:
            messages = await list_all_messages(sessions, session_id)
        except DexServiceError:
            return None
        return next((message for message in messages if expected in message_text(message)), None)

    description = f"{session_id} message containing {expected!r}"
    return await wait_until(description, matching_message, timeout)


def message_text(message: AgentMessage) -> str:
    return message.text or message.tool_output or ""


def is_idle(snapshot: AgentSnapshot) -> bool:
    return (
        snapshot.state is not None
        and snapshot.state.status == AgentSessionStatus.WAITING_FOR_USER_MESSAGE
        and snapshot.state.active_turn_id is None
    )


def pending_interrupt(snapshot: AgentSnapshot, kind: InterruptKind) -> AgentInterrupt | None:
    return next((item for item in snapshot.pending_interrupts if item.kind == kind), None)


def is_waiting_for_timer(snapshot: AgentSnapshot) -> bool:
    return any(call.status == ToolCallStatus.WAITING_FOR_TIMER for call in snapshot.tool_calls)


def draft_revision(snapshot: AgentSnapshot) -> int | None:
    plan = snapshot.plan
    is_draft = plan is not None and plan.status == PlanStatus.DRAFT
    return plan.revision if is_idle(snapshot) and is_draft and plan is not None else None
