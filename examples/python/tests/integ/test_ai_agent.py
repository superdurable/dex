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

"""Integration coverage for durable context and MCP tool routing."""

from __future__ import annotations

import asyncio
import socket
import sys
from collections.abc import Callable
from pathlib import Path

import pytest
from dex import AsyncClient, ChannelMessageNotFoundError, StartFlowOptions

from dex_examples.app import ExampleApp
from dex_examples.products.ai_agent.mcp_registry import MCPRegistry
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    HistoryRequest,
    PlanExecutionRequest,
    SteerMessageRequest,
    ToolApprovalRequest,
    UserMessage,
)
from tests.integ.conftest import WAIT_TIMEOUT, wait_until


async def test_mcp_registry_supports_streamable_http(tmp_path: Path) -> None:
    port = _available_port()
    server_path = Path(__file__).with_name("ai_agent_mcp_server.py")
    process = await asyncio.create_subprocess_exec(
        sys.executable,
        str(server_path),
        "--transport",
        "streamable-http",
        "--port",
        str(port),
        stdout=asyncio.subprocess.DEVNULL,
        stderr=asyncio.subprocess.PIPE,
    )
    registry: MCPRegistry | None = None
    try:
        await _wait_for_port(port, process)
        config_path = tmp_path / "mcp-http.yaml"
        config_path.write_text(
            f"""
servers:
  - name: http_test
    transport: streamable_http
    url: http://127.0.0.1:{port}/mcp
    trust_read_only_annotations: true
""".strip()
        )
        registry = MCPRegistry.from_file(config_path)
        await registry.start()

        async def write_progress(message: str) -> None:
            pass

        result = await registry.execute(
            "http_test__lookup",
            {"query": "streamable"},
            [],
            write_progress,
        )
        assert "found:streamable" in result.content
    finally:
        if registry is not None:
            await registry.close()
        process.terminate()
        await process.wait()


async def test_local_portal_mcp_servers_are_ready_before_start() -> None:
    config_path = Path(__file__).parents[2] / "ai-agent" / "mcp-servers.local.yaml"
    registry = MCPRegistry.from_file(config_path)
    try:
        await registry.start()
        assert registry.server_names == ["google_docs", "search", "slack"]
        assert registry.tool_names == [
            "google_docs__create_document",
            "google_docs__search_documents",
            "search__web_search",
            "slack__post_message",
            "slack__search_messages",
        ]
    finally:
        await registry.close()


async def test_ai_agent_executes_mcp_primitives_and_approval(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-mcp")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(enabled_mcp_servers=["test"]),
        StartFlowOptions(),
    )
    await _send(client, app, flow_id, '/tool test__lookup {"query":"dex"}')
    await _wait_for_content(client, app, flow_id, "found:dex")

    await _send(client, app, flow_id, '/tool test__publish {"message":"hello"}')

    async def approval_id() -> str | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.pending_approval_call_id

    call_id = await wait_until("MCP write approval", approval_id, WAIT_TIMEOUT)
    assert await client.invoke_rpc(
        app.ai_agent.approve_tool,
        flow_id,
        ToolApprovalRequest(call_id, True),
    )
    await _wait_for_content(client, app, flow_id, "published:hello")

    await _send(
        client,
        app,
        flow_id,
        '/tool mcp_read_resource {"server":"test","uri":"test://guide"}',
    )
    await _wait_for_content(client, app, flow_id, "Durable MCP resource")

    await _send(
        client,
        app,
        flow_id,
        '/tool mcp_get_prompt {"server":"test","name":"greeting","arguments":{"name":"Dex"}}',
    )
    await _wait_for_content(client, app, flow_id, "Greet Dex")


async def test_ai_agent_requests_and_resumes_from_durable_user_input(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-user-input")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(),
        StartFlowOptions(),
    )
    await _send(
        client,
        app,
        flow_id,
        "/ask What date should I use?",
        plan_mode=True,
    )

    async def draft_revision() -> int | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        if (
            description.status != "waiting_for_message"
            or description.plan is None
            or description.plan["status"] != "draft"
        ):
            return None
        revision = description.plan["revision"]
        assert isinstance(revision, int)
        return revision

    revision = await wait_until("AI Agent input plan", draft_revision, WAIT_TIMEOUT)
    assert await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(revision),
    )

    async def is_waiting_for_input() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return (
            description.status == "waiting_for_message"
            and description.pending_user_input_prompt == "What date should I use?"
        )

    await wait_until("AI Agent user input request", is_waiting_for_input, WAIT_TIMEOUT)
    history = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=200),
    )
    request_message = next(
        item
        for item in reversed(history.messages)
        if any(call.name == "request_user_input" for call in item.message.tool_calls)
    )
    request_call = next(
        call
        for call in request_message.message.tool_calls
        if call.name == "request_user_input"
    )
    request_result = next(
        item
        for item in history.messages
        if item.message.tool_call_id == request_call.id
    )
    assert request_result.sequence == request_message.sequence + 1
    assert request_result.message.role == "tool"

    await _send(client, app, flow_id, "September 12")

    async def has_completed_plan() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return (
            description.pending_user_input_prompt is None
            and description.plan is not None
            and description.plan["status"] == "completed"
        )

    await wait_until("AI Agent resumed plan", has_completed_plan, WAIT_TIMEOUT)
    description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
    assert description.pending_user_input_prompt is None
    resumed_history = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=200),
    )
    user_answer = next(
        item
        for item in resumed_history.messages
        if item.message.role == "user" and item.message.content == "September 12"
    )
    assert user_answer.sequence == request_result.sequence + 1


async def test_ai_agent_closes_remaining_tool_calls_before_waiting_for_input(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-user-input-tool-closure")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(),
        StartFlowOptions(),
    )
    await _send(client, app, flow_id, "/ask-many What date should I use?")

    async def is_waiting_for_input() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.pending_user_input_prompt == "What date should I use?"

    await wait_until("AI Agent multi-call input request", is_waiting_for_input)
    history = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=200),
    )
    request_message = next(
        item for item in reversed(history.messages) if len(item.message.tool_calls) == 2
    )
    tool_results = [
        item
        for item in history.messages
        if item.message.tool_call_id
        in {call.id for call in request_message.message.tool_calls}
    ]
    assert [item.sequence for item in tool_results] == [
        request_message.sequence + 1,
        request_message.sequence + 2,
    ]
    assert [item.message.tool_call_id for item in tool_results] == [
        call.id for call in request_message.message.tool_calls
    ]
    assert '"status": "cancelled"' in tool_results[1].message.content
    await _send(client, app, flow_id, "September 12")
    await _wait_for_content(
        client,
        app,
        flow_id,
        "Local demo response: September 12",
    )


async def test_ai_agent_offers_durable_user_input_choices(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-user-input-choices")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(),
        StartFlowOptions(),
    )
    await _send(
        client,
        app,
        flow_id,
        "/choose Where should I deploy? | Staging | Production",
    )

    async def choices() -> list[str] | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        if description.pending_user_input_prompt != "Where should I deploy?":
            return None
        return description.pending_user_input_choices

    assert await wait_until("AI Agent input choices", choices) == [
        "Staging",
        "Production",
    ]
    await _send(client, app, flow_id, "Production")

    async def input_was_consumed() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.pending_user_input_prompt is None

    await wait_until("AI Agent consumed selected choice", input_was_consumed)

    await _wait_for_content(client, app, flow_id, "Local demo response: Production")


async def test_ai_agent_plans_before_execution(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-plan")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(enabled_mcp_servers=["test"]),
        StartFlowOptions(),
    )
    await _send(client, app, flow_id, "Research Dex and report back", plan_mode=True)

    async def draft_revision() -> int | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        if (
            description.status != "waiting_for_message"
            or description.plan is None
            or description.plan["status"] != "draft"
        ):
            return None
        tasks = description.plan["tasks"]
        assert isinstance(tasks, list)
        assert all(task["status"] == "pending" for task in tasks)
        revision = description.plan["revision"]
        assert isinstance(revision, int)
        return revision

    revision = await wait_until("AI Agent draft plan", draft_revision, WAIT_TIMEOUT)
    assert not await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(revision + 1),
    )
    assert await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(revision),
    )
    assert not await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(revision),
    )

    async def completed_plan() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return (
            description.status == "waiting_for_message"
            and description.plan is not None
            and description.plan["status"] == "completed"
            and all(
                task["status"] == "completed"
                for task in description.plan["tasks"]
            )
        )

    await wait_until("AI Agent completed plan", completed_plan, WAIT_TIMEOUT)
    assert not await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(revision),
    )


async def test_ai_agent_draft_blocks_business_tools_until_execution(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-plan-gating")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(enabled_mcp_servers=["test"]),
        StartFlowOptions(),
    )
    await _send(client, app, flow_id, "Plan a lookup", plan_mode=True)

    async def has_draft() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.plan is not None and description.plan["status"] == "draft"

    await wait_until("AI Agent draft plan", has_draft, WAIT_TIMEOUT)
    await _send(client, app, flow_id, '/tool test__lookup {"query":"blocked"}')
    await _wait_for_content(client, app, flow_id, "unknown_or_disabled_tool")

    page = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=100),
    )
    assert not any("found:blocked" in item.message.content for item in page.messages)

    await _send(client, app, flow_id, "/plan-clear")

    async def plan_cleared() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.status == "waiting_for_message" and description.plan is None

    await wait_until("AI Agent cleared draft", plan_cleared, WAIT_TIMEOUT)


async def test_ai_agent_waiting_does_not_mark_an_incomplete_plan_completed(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-plan-advisory")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(enabled_mcp_servers=["test"]),
        StartFlowOptions(),
    )
    await _send(
        client,
        app,
        flow_id,
        "/plan-stop demonstrate advisory completion",
        plan_mode=True,
    )

    async def draft_revision() -> int | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        if (
            description.status != "waiting_for_message"
            or description.plan is None
            or description.plan["status"] != "draft"
        ):
            return None
        revision = description.plan["revision"]
        return revision if isinstance(revision, int) else None

    draft = await wait_until("AI Agent advisory draft", draft_revision, WAIT_TIMEOUT)
    assert await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(draft),
    )

    async def active_revision() -> int | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        if (
            description.status != "waiting_for_message"
            or description.plan is None
            or description.plan["status"] != "active"
        ):
            return None
        revision = description.plan["revision"]
        return revision if isinstance(revision, int) else None

    active = await wait_until(
        "AI Agent incomplete active plan",
        active_revision,
        WAIT_TIMEOUT,
    )
    await _send(client, app, flow_id, '/tool test__lookup {"query":"blocked"}')
    await _wait_for_content(client, app, flow_id, "unknown_or_disabled_tool")
    description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
    assert description.plan is not None
    assert description.plan["status"] == "active"
    page = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=100),
    )
    assert not any("found:blocked" in item.message.content for item in page.messages)

    async def returned_to_waiting() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.status == "waiting_for_message"

    await wait_until("AI Agent returned to waiting", returned_to_waiting, WAIT_TIMEOUT)

    assert await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(active),
    )


async def test_ai_agent_revises_and_clears_a_draft_before_execution(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-plan-revision")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(),
        StartFlowOptions(),
    )
    await _send(client, app, flow_id, "Plan the first objective", plan_mode=True)

    async def plan_revision() -> int | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        if description.plan is None:
            return None
        revision = description.plan["revision"]
        return revision if isinstance(revision, int) else None

    first_revision = await wait_until(
        "AI Agent first plan revision",
        plan_revision,
        WAIT_TIMEOUT,
    )
    await _send(client, app, flow_id, "Plan the revised objective", plan_mode=True)
    assert not await client.invoke_rpc(
        app.ai_agent.execute_plan,
        flow_id,
        PlanExecutionRequest(first_revision),
    )

    async def revised_plan() -> int | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        if description.plan is None:
            return None
        revision = description.plan["revision"]
        tasks = description.plan["tasks"]
        if (
            not isinstance(revision, int)
            or revision <= first_revision
            or not any("revised objective" in task["content"] for task in tasks)
        ):
            return None
        return revision

    await wait_until("AI Agent revised draft", revised_plan, WAIT_TIMEOUT)
    await _send(client, app, flow_id, "/plan-clear", plan_mode=True)

    async def plan_cleared() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.status == "waiting_for_message" and description.plan is None

    await wait_until("AI Agent cleared plan", plan_cleared, WAIT_TIMEOUT)


async def test_ai_agent_compacts_before_enforcing_message_retention(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-compaction")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(
            max_context_tokens=80,
            compaction_trigger_fraction=0.5,
            compaction_keep_fraction=0.1,
            message_retention_limit=3,
        ),
        StartFlowOptions(),
    )

    for index in range(5):
        await _send(client, app, flow_id, f"turn {index}: " + "context " * 20)
        expected_sequence = (index + 1) * 2

        async def turn_finished() -> bool:
            description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
            return description.last_sequence >= expected_sequence

        await wait_until(f"AI Agent turn {index}", turn_finished, WAIT_TIMEOUT)

    description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
    page = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=20),
    )
    assert description.summarized_through_sequence > 0
    assert len(page.messages) <= 3
    assert page.messages[-1].message.role == "assistant"


async def test_ai_agent_rejects_tools_disabled_for_the_session(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-tool-allowlist")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(
            enabled_mcp_servers=["test"],
            enabled_tools=["test__lookup"],
        ),
        StartFlowOptions(),
    )

    await _send(client, app, flow_id, '/tool test__publish {"message":"blocked"}')
    await _wait_for_content(client, app, flow_id, "unknown_or_disabled_tool")
    description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
    assert description.pending_approval_call_id is None


async def test_ai_agent_queues_messages_and_steers_at_a_safe_boundary(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-steer")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(),
        StartFlowOptions(),
    )
    await _send(client, app, flow_id, "/wait 30 integration timer")

    async def is_waiting_for_timer() -> bool:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.status == "waiting_for_timer"

    await wait_until("AI Agent durable wait", is_waiting_for_timer, WAIT_TIMEOUT)
    await _send(client, app, flow_id, "replace the current objective")
    pending = await client.get_channel_messages(
        flow_id,
        app.ai_agent.queued_user_messages,
    )
    assert [message.value.content for message in pending] == [
        "replace the current objective"
    ]
    description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
    assert description.status == "waiting_for_timer"
    assert description.pending_queued_message_count == 1

    message = pending[0]
    assert await client.invoke_rpc(
        app.ai_agent.steer_message,
        flow_id,
        SteerMessageRequest(message.message_id, message.value),
    )
    with pytest.raises(ChannelMessageNotFoundError):
        await client.invoke_rpc(
            app.ai_agent.steer_message,
            flow_id,
            SteerMessageRequest(message.message_id, message.value),
        )

    await _wait_for_content(
        client,
        app,
        flow_id,
        "Local demo response: replace the current objective",
    )
    assert not await client.get_channel_messages(
        flow_id,
        app.ai_agent.queued_user_messages,
    )
    history = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=100),
    )
    assert any(
        message.message.role == "tool"
        and '"status": "interrupted"' in message.message.content
        for message in history.messages
    )


async def _send(
    client: AsyncClient,
    app: ExampleApp,
    flow_id: str,
    content: str,
    plan_mode: bool = False,
) -> None:
    assert await client.invoke_rpc(
        app.ai_agent.send_message,
        flow_id,
        UserMessage(content, plan_mode),
    )


async def _wait_for_content(
    client: AsyncClient,
    app: ExampleApp,
    flow_id: str,
    expected: str,
) -> None:
    async def contains_content() -> bool:
        page = await client.invoke_rpc(
            app.ai_agent.history,
            flow_id,
            HistoryRequest(limit=100),
        )
        return any(expected in item.message.content for item in page.messages)

    await wait_until(f"message containing {expected!r}", contains_content, WAIT_TIMEOUT)


def _available_port() -> int:
    with socket.socket() as server_socket:
        server_socket.bind(("127.0.0.1", 0))
        return int(server_socket.getsockname()[1])


async def _wait_for_port(
    port: int,
    process: asyncio.subprocess.Process,
) -> None:
    deadline = asyncio.get_running_loop().time() + 10
    while asyncio.get_running_loop().time() < deadline:
        if process.returncode is not None:
            stderr = await process.stderr.read() if process.stderr is not None else b""
            raise RuntimeError(f"MCP HTTP server exited: {stderr.decode()}")
        try:
            _, writer = await asyncio.open_connection("127.0.0.1", port)
            writer.close()
            await writer.wait_closed()
            return
        except OSError:
            await asyncio.sleep(0.05)
    raise RuntimeError("MCP HTTP server did not become ready")
