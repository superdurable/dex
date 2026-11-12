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

"""The Agent Quick Start agents, each in its own Worker and Client, on a real Dex server.

A turn reads an order. The next turn cancels an order after one approval, and
the order desk records one call. A session whose Worker stops while a
cancellation waits for approval finishes on a new Worker, which runs the
cancellation once.
"""

from __future__ import annotations

import os
import uuid
from pathlib import Path

import pytest
from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentInterrupt,
    AgentMessage,
    AgentSessionClient,
    AgentSnapshot,
    MessageRole,
)

from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.demo_model_script import DEMO_REPLY_PREFIX
from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.quick_start.embed import ask_agent
from agent_harness_examples.quick_start.frameworks import FRAMEWORKS, build_quick_start_agent
from agent_harness_examples.quick_start.reconcile import report_unknown_outcomes
from agent_harness_examples.quick_start.session import (
    QuickStartApp,
    run_turn,
)
from tests.conftest import wait_until
from tests.test_order_support_agents import claude_messages_api  # noqa: F401  (fixture)

CANCELLED_REPLY = DEMO_REPLY_PREFIX + "Order 1002 is cancelled: requested by the customer."


@pytest.mark.parametrize("framework", FRAMEWORKS)
async def test_quick_start_agent_reads_then_cancels_once(
    framework: str, tmp_path: Path, request: pytest.FixtureRequest
) -> None:
    if framework == "claude-agent-sdk":
        request.getfixturevalue("claude_messages_api")
    order_desk = OrderDesk()
    app = await _started_app(framework, order_desk, tmp_path / "cache")
    approvals: list[str] = []

    async def approve(interrupt: AgentInterrupt) -> bool:
        approvals.append(interrupt.tool_name)
        return True

    try:
        session_id = await _start_session(app.sessions, framework)
        replies = await run_turn(app.sessions, session_id, "Look up order 1001", approve)
        assert replies[-1] == DEMO_REPLY_PREFIX + "Order 1001: 2 notebooks, shipped."
        assert approvals == []

        replies = await run_turn(app.sessions, session_id, "Cancel order 1002", approve)
        assert replies[-1] == CANCELLED_REPLY
        assert approvals == ["cancel_order"]
        assert order_desk.cancel_order_calls == ["1002"]
    finally:
        await app.close()


@pytest.mark.parametrize("framework", FRAMEWORKS)
async def test_quick_start_agent_cancels_once_after_a_worker_restart(
    framework: str, tmp_path: Path, request: pytest.FixtureRequest
) -> None:
    if framework == "claude-agent-sdk":
        request.getfixturevalue("claude_messages_api")
    first_order_desk = OrderDesk()
    first_app = await _started_app(framework, first_order_desk, tmp_path / "cache-1")
    try:
        session_id = await _start_session(first_app.sessions, framework)
        await first_app.sessions.send_user_message(session_id, "Cancel order 1002")
        await wait_until(
            "the cancellation to wait for approval",
            lambda: _pending_interrupts(first_app.sessions, session_id),
        )
    finally:
        await first_app.close()

    second_order_desk = OrderDesk()
    second_app = await _started_app(framework, second_order_desk, tmp_path / "cache-2")

    async def approve(interrupt: AgentInterrupt) -> bool:
        return True

    try:
        replies = await run_turn(second_app.sessions, session_id, None, approve)
        assert replies[-1] == CANCELLED_REPLY
        assert first_order_desk.cancel_order_calls == []
        assert second_order_desk.cancel_order_calls == ["1002"]
    finally:
        await second_app.close()


async def test_embedded_agent_answers_one_turn(tmp_path: Path) -> None:
    config = AgentHarnessExamplesConfig(
        server_address=os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
        http_address="127.0.0.1:0",
        blob_cache_dir=tmp_path / "cache",
    )
    session_flow = build_quick_start_agent("openai-agents", OrderDesk(), config)
    snapshot = await ask_agent(
        session_flow,
        f"embed-test-{uuid.uuid4().hex}",
        "Look up order 1001",
        server_address=config.server_address,
        blob_cache_dir=str(tmp_path / "embed-cache"),
    )
    replies = [m.text for m in snapshot.messages if m.role == MessageRole.ASSISTANT and m.text]
    assert replies[-1] == DEMO_REPLY_PREFIX + "Order 1001: 2 notebooks, shipped."


async def test_unknown_outcomes_are_reported_once() -> None:
    sessions = _RecordingSessions()
    checked: list[str] = []

    async def check(key: str, arguments: str) -> str:
        checked.append(key)
        return "Order 1002 was cancelled."

    first = await report_unknown_outcomes(sessions, "session-1", check)
    second = await report_unknown_outcomes(sessions, "session-1", check)

    assert first == ["Order 1002 was cancelled. (tool call session-1/turn-1/call-7)"]
    assert second == []
    assert checked == ["session-1/turn-1/call-7"]


class _RecordingSessions:
    """A session whose history holds one cancel_order call with an unknown outcome."""

    def __init__(self) -> None:
        self.messages = [
            AgentMessage(
                sequence=1,
                turn_id="turn-1",
                role=MessageRole.TOOL,
                tool_call_id="call-7",
                tool_name="cancel_order",
                tool_arguments='{"order_id": "1002"}',
                tool_output=OUTCOME_UNKNOWN_TOOL_RESULT,
                framework_item="{}",
                created_at="2026-11-11T00:00:00Z",
            )
        ]

    async def get_agent_snapshot(self, session_id: str) -> AgentSnapshot:
        return AgentSnapshot(state=None, messages=list(self.messages))

    async def send_user_message(self, session_id: str, text: str) -> bool:
        self.messages.append(
            AgentMessage(
                sequence=len(self.messages) + 1,
                turn_id="turn-2",
                role=MessageRole.USER,
                text=text,
                framework_item="{}",
                created_at="2026-11-11T00:00:01Z",
            )
        )
        return True


async def _started_app(framework: str, order_desk: OrderDesk, cache_dir: Path) -> QuickStartApp:
    config = AgentHarnessExamplesConfig(
        server_address=os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
        http_address="127.0.0.1:0",
        blob_cache_dir=cache_dir,
    )
    app = QuickStartApp(
        build_quick_start_agent(framework, order_desk, config),
        config.server_address,
        config.blob_cache_dir,
    )
    if not await _is_server_ready(app):
        await app.close()
        message = f"no Dex server at {config.server_address}; set DEX_FLOW_SERVICE_ADDRESS"
        if os.environ.get("DEX_EXAMPLES_ALLOW_SKIP") == "1":
            pytest.skip(message)
        pytest.fail(message)
    await app.start_worker()
    return app


async def _is_server_ready(app: QuickStartApp) -> bool:
    try:
        return await app.client.health_check()
    except Exception:
        return False


async def _start_session(sessions: AgentSessionClient, framework: str) -> str:
    session_id = f"{framework}-quick-start-test-{uuid.uuid4().hex}"
    await sessions.start_session(session_id)
    return session_id


async def _pending_interrupts(sessions: AgentSessionClient, session_id: str) -> bool:
    snapshot = await sessions.get_agent_snapshot(session_id)
    return bool(snapshot.pending_interrupts)
