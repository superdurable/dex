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

"""Each framework's order support agent, through the HTTP routes, on a real Dex server.

A turn reads an order with the READ tool. The next turn cancels an order: the
write waits for approval, runs nothing before it, and runs once after it. A
Slack thread reply through the Slack Connector's mock does the same, in its
own tool Step under the connector's Call ID.
"""

from __future__ import annotations

import json
import os
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest
from dex_agents import AgentInterrupt, AgentSessionClient, AgentSessionStatus, AgentSnapshot
from dex_agents.testing import EXECUTE_COMPLETED_EVENT, read_step_history
from dex_connectors.sdk import derive_call_id
from dex_connectors.sdk.connectormock import RecordedCall
from dex_connectors.slack import PostThreadReplyInput, slackmock
from quart.typing import TestClientProtocol as QuartTestClient

from agent_harness_examples.app import AgentHarnessExamplesApp
from agent_harness_examples.demo_model_script import (
    DEMO_REPLY_PREFIX,
    SLACK_POST_THREAD_REPLY_TOOL,
)
from agent_harness_examples.http_app import create_app
from agent_harness_examples.http_routes import SESSION_TOKEN_HEADER
from tests.claude_messages_api_stub import ClaudeMessagesApiStub
from tests.conftest import wait_until

HARNESS_NAMES = ["claude-agent-sdk", "openai-agents", "pydantic-ai", "langchain", "google-adk"]
STUB_API_KEY = "sk-ant-stub-not-a-real-key"
# A refused port, so a request that skips the stub fails instead of reaching the network.
UNREACHABLE_PROXY = "http://127.0.0.1:9"
SLACK_CHANNEL_ID = "C0123456789"
SLACK_THREAD_TIMESTAMP = "1710000000.000100"
# The timestamp of the reply that the mock's default postThreadReply case returns.
SLACK_MOCK_REPLY_TIMESTAMP = "1710000011.000600"
SINGLE_ATTEMPT_TOOL_STEP_TYPE = "CallSingleAttemptTool"


@pytest.fixture()
def claude_messages_api(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> Iterator[ClaudeMessagesApiStub]:
    """Points the Claude Code CLI, which inherits the Worker's environment, at a local stub."""
    stub = ClaudeMessagesApiStub()
    stub.start()
    for name in list(os.environ):
        if name.startswith(("CLAUDE", "ANTHROPIC")):
            monkeypatch.delenv(name)
    environment = {
        "HOME": str(tmp_path / "home"),
        "CLAUDE_CONFIG_DIR": str(tmp_path / "claude-config"),
        "ANTHROPIC_API_KEY": STUB_API_KEY,
        "ANTHROPIC_BASE_URL": stub.base_url,
        "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
        "CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK": "1",
        "HTTPS_PROXY": UNREACHABLE_PROXY,
        "HTTP_PROXY": UNREACHABLE_PROXY,
        "NO_PROXY": "127.0.0.1,localhost",
        # Other sign-ins the example must override, so only the API key reaches the stub.
        "ANTHROPIC_AUTH_TOKEN": "sk-ant-other-sign-in",
        "CLAUDE_CODE_USE_BEDROCK": "1",
    }
    for name, value in environment.items():
        monkeypatch.setenv(name, value)
    (tmp_path / "home").mkdir()
    try:
        yield stub
    finally:
        stub.stop()


@pytest.mark.parametrize("harness_name", HARNESS_NAMES)
async def test_order_support_agent_reads_and_cancels_after_approval(
    app: AgentHarnessExamplesApp, harness_name: str, request: pytest.FixtureRequest
) -> None:
    stub = (
        request.getfixturevalue("claude_messages_api")
        if harness_name == "claude-agent-sdk"
        else None
    )
    example = next(item for item in app.examples if item.harness_name == harness_name)
    sessions = AgentSessionClient(app.client, example.session_flow)
    routes = _SessionRoutes(create_app(app).test_client(), harness_name)
    session_id = await routes.start_session()

    await routes.send_user_message(session_id, "Look up order 1001")
    first_turn = await _wait_for_idle_turn(sessions, session_id, 1)
    assert first_turn.messages[-1].text == DEMO_REPLY_PREFIX + "Order 1001: 2 notebooks, shipped."
    assert _tool_outputs(first_turn, "look_up_order") == ["Order 1001: 2 notebooks, shipped."]

    await routes.send_user_message(session_id, "Please cancel order 1002")
    interrupt = await _wait_for_pending_interrupt(sessions, session_id)
    assert interrupt.tool_name == "cancel_order"
    assert example.order_desk.cancel_order_calls.count("1002") == 0
    snapshot_json = await routes.get_agent_snapshot(session_id)
    assert interrupt.interrupt_id in str(snapshot_json)

    await routes.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
    second_turn = await _wait_for_idle_turn(sessions, session_id, 2)
    assert second_turn.messages[-1].text == (
        DEMO_REPLY_PREFIX + "Order 1002 is cancelled: requested by the customer."
    )
    assert example.order_desk.cancel_order_calls.count("1002") == 1
    assert example.order_desk.describe_order("1002") == "Order 1002: 1 desk lamp, cancelled."
    assert _tool_outputs(second_turn, "cancel_order") == [
        "Order 1002 is cancelled: requested by the customer."
    ]

    if stub is not None:
        assert {recorded.api_key for recorded in stub.requests} == {STUB_API_KEY}
        assert {recorded.authorization for recorded in stub.requests} == {None}
        assert any(recorded.path.startswith("/v1/messages") for recorded in stub.requests)


@pytest.mark.parametrize("harness_name", HARNESS_NAMES)
async def test_order_support_agent_never_runs_a_rejected_cancellation(
    app: AgentHarnessExamplesApp, harness_name: str, request: pytest.FixtureRequest
) -> None:
    if harness_name == "claude-agent-sdk":
        request.getfixturevalue("claude_messages_api")
    example = next(item for item in app.examples if item.harness_name == harness_name)
    sessions = AgentSessionClient(app.client, example.session_flow)
    routes = _SessionRoutes(create_app(app).test_client(), harness_name)
    session_id = await routes.start_session()

    await routes.send_user_message(session_id, "Cancel order 1003")
    interrupt = await _wait_for_pending_interrupt(sessions, session_id)
    assert interrupt.tool_name == "cancel_order"
    await routes.resolve_interrupt(session_id, interrupt.interrupt_id, approved=False)
    turn = await _wait_for_idle_turn(sessions, session_id, 1)
    assert example.order_desk.cancel_order_calls.count("1003") == 0
    assert example.order_desk.describe_order("1003") == "Order 1003: 3 pens, processing."
    outputs = _tool_outputs(turn, "cancel_order")
    assert not any("is cancelled" in output for output in outputs), outputs

@pytest.mark.parametrize("harness_name", HARNESS_NAMES)
async def test_order_support_agent_posts_a_slack_thread_reply_once_after_approval(
    app: AgentHarnessExamplesApp,
    slack_mock: slackmock.Mock,
    harness_name: str,
    request: pytest.FixtureRequest,
) -> None:
    if harness_name == "claude-agent-sdk":
        request.getfixturevalue("claude_messages_api")
    example = next(item for item in app.examples if item.harness_name == harness_name)
    sessions = AgentSessionClient(app.client, example.session_flow)
    routes = _SessionRoutes(create_app(app).test_client(), harness_name)
    session_id = await routes.start_session()
    reply_text = "Order 1002 ships on Thursday."

    await routes.send_user_message(
        session_id,
        f"Reply in Slack thread {SLACK_CHANNEL_ID} {SLACK_THREAD_TIMESTAMP}: {reply_text}",
    )
    interrupt = await _wait_for_pending_interrupt(sessions, session_id)
    assert interrupt.tool_name == SLACK_POST_THREAD_REPLY_TOOL
    assert _slack_thread_replies(slack_mock, session_id) == []

    await routes.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
    turn = await _wait_for_idle_turn(sessions, session_id, 1)
    replies = _slack_thread_replies(slack_mock, session_id)
    assert len(replies) == 1
    reply = replies[0]
    assert reply.input == PostThreadReplyInput(
        channel_id=SLACK_CHANNEL_ID, thread_timestamp=SLACK_THREAD_TIMESTAMP, text=reply_text
    )
    assert (reply.branch, reply.retry, reply.replay) == ("sent", False, False)

    # The Call ID comes from the tool Step's execution ID, so every attempt of that Step shares it.
    history = await read_step_history(
        app.config.server_address, session_id, dexcli=_dexcli_path()
    )
    tool_steps = [
        entry
        for entry in history
        if entry.event_type == EXECUTE_COMPLETED_EVENT
        and entry.step_type == SINGLE_ATTEMPT_TOOL_STEP_TYPE
    ]
    assert len(tool_steps) == 1
    operation = slack_mock.post_thread_reply_script.definition().operation
    assert reply.call_id == derive_call_id(session_id, tool_steps[0].step_execution_id, operation)

    outputs = _tool_outputs(turn, SLACK_POST_THREAD_REPLY_TOOL)
    assert len(outputs) == 1
    assert json.loads(outputs[0])["message"]["timestamp"] == SLACK_MOCK_REPLY_TIMESTAMP
    assert turn.messages[-1].text is not None
    assert turn.messages[-1].text.startswith(DEMO_REPLY_PREFIX)


class _SessionRoutes:
    def __init__(self, http: QuartTestClient, harness_name: str) -> None:
        self._http = http
        self._prefix = f"/products/agent-harnesses/{harness_name}"
        self._tokens: dict[str, str] = {}

    async def start_session(self) -> str:
        body = await self._post_json("/sessions", {}, {})
        session_id = str(body["session_id"])
        self._tokens[session_id] = str(body["session_token"])
        return session_id

    async def send_user_message(self, session_id: str, text: str) -> None:
        body = await self._post_json(
            "/messages", {"session_id": session_id, "text": text}, self._headers(session_id)
        )
        assert body == {"accepted": True}

    async def resolve_interrupt(self, session_id: str, interrupt_id: str, approved: bool) -> None:
        payload = {"session_id": session_id, "interrupt_id": interrupt_id, "approved": approved}
        body = await self._post_json("/interrupts/resolve", payload, self._headers(session_id))
        assert body == {"accepted": True}

    async def get_agent_snapshot(self, session_id: str) -> Any:
        response = await self._http.get(
            f"{self._prefix}/snapshot",
            query_string={"session_id": session_id},
            headers=self._headers(session_id),
        )
        assert response.status_code == 200, await response.get_data(as_text=True)
        return await response.get_json()

    async def _post_json(self, path: str, payload: dict[str, Any], headers: dict[str, str]) -> Any:
        response = await self._http.post(f"{self._prefix}{path}", json=payload, headers=headers)
        assert response.status_code == 200, await response.get_data(as_text=True)
        return await response.get_json()

    def _headers(self, session_id: str) -> dict[str, str]:
        return {SESSION_TOKEN_HEADER: self._tokens[session_id]}


def _slack_thread_replies(
    slack_mock: slackmock.Mock, session_id: str
) -> list[RecordedCall[PostThreadReplyInput]]:
    return [call for call in slack_mock.post_thread_reply_script.calls() if call.flow_id == session_id]


def _dexcli_path() -> Path | None:
    """Returns the dexcli that run-integ-tests.sh built, or None to use $DEXCLI or PATH."""
    configured = os.environ.get("DEXCLI_PATH")
    return Path(configured) if configured else None


def _tool_outputs(snapshot: AgentSnapshot, tool_name: str) -> list[str]:
    return [
        message.tool_output
        for message in snapshot.messages
        if message.tool_name == tool_name and message.tool_output is not None
    ]


async def _wait_for_idle_turn(
    sessions: AgentSessionClient, session_id: str, turn_count: int
) -> AgentSnapshot:
    async def idle_snapshot() -> AgentSnapshot | None:
        snapshot = await sessions.get_agent_snapshot(session_id)
        state = snapshot.state
        if state is None:
            return None
        assert state.last_turn_error is None, state.last_turn_error
        is_idle = (
            state.status == AgentSessionStatus.WAITING_FOR_USER_MESSAGE
            and state.active_turn_id is None
            and state.turn_count == turn_count
        )
        return snapshot if is_idle else None

    snapshot: AgentSnapshot = await wait_until(f"{session_id} turn {turn_count}", idle_snapshot)
    return snapshot


async def _wait_for_pending_interrupt(
    sessions: AgentSessionClient, session_id: str
) -> AgentInterrupt:
    async def pending_interrupt() -> AgentInterrupt | None:
        snapshot = await sessions.get_agent_snapshot(session_id)
        return next(iter(snapshot.pending_interrupts), None)

    interrupt: AgentInterrupt = await wait_until(f"{session_id} approval", pending_interrupt)
    return interrupt

