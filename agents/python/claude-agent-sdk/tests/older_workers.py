# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns that a Worker without ``build_unfinished_turn_messages`` ends, as adapter 0.6.11 does."""

from __future__ import annotations

from collections.abc import Sequence

from dex_agents import (
    AgentInterrupt,
    AgentMessage,
    AgentMessageOrigin,
    AgentSnapshot,
    FrameworkMessage,
    MessageRole,
)
from dex_agents.testing import AgentWorkerProcess
from tests.claude_test_agent import WITHOUT_UNFINISHED_TURN_MESSAGES_ENVIRONMENT_VARIABLE
from tests.conftest import ClaudeAgentHarness

OLDER_WORKER_ENVIRONMENT = {WITHOUT_UNFINISHED_TURN_MESSAGES_ENVIRONMENT_VARIABLE: "1"}
OPEN_WRITE_PROMPT = 'call publish {"message": "draft"}'


async def cancel_on_an_older_worker(
    harness: ClaudeAgentHarness, session_id: str
) -> tuple[AgentInterrupt, AgentSnapshot]:
    """Defer a write on this adapter's Worker, then cancel the turn on an older Worker."""
    worker = await harness.stack.start_worker()
    await harness.sessions.start_session(session_id, OPEN_WRITE_PROMPT)
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    ended = await end_turn_on_an_older_worker(harness, worker, session_id, 1)
    # The core's note is all such a Worker stores; the stored tool use keeps no result.
    assert roles_of(ended.messages) == [
        (MessageRole.USER, "turn-1", None),
        (MessageRole.ASSISTANT, "turn-1", "publish"),
        (MessageRole.USER, "turn-1", None),
    ]
    return interrupt, ended


async def end_turn_on_an_older_worker(
    harness: ClaudeAgentHarness, worker: AgentWorkerProcess, session_id: str, turn_count: int
) -> AgentSnapshot:
    """Replace ``worker`` with an older Worker, cancel the turn there, and stop that Worker."""
    worker.kill()
    older_worker = await harness.stack.start_worker(OLDER_WORKER_ENVIRONMENT)
    assert await harness.sessions.cancel_turn(session_id)
    ended = await harness.wait_for_turns(session_id, turn_count)
    older_worker.kill()
    assert ended.messages[-1].origin == AgentMessageOrigin.TURN_CANCELLED_NOTE
    return ended


def roles_of(
    messages: Sequence[AgentMessage | FrameworkMessage],
) -> list[tuple[MessageRole, str | None, str | None]]:
    """Return each message's role, turn ID, and tool name."""
    return [
        (message.role, getattr(message, "turn_id", None), message.tool_name) for message in messages
    ]
