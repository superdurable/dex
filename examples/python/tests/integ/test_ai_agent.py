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

"""Integration coverage for the AI Agent: chat, MCP tools, questions, plans, and steering."""

from __future__ import annotations

import asyncio
import json
import socket
import sys
from collections.abc import Callable
from pathlib import Path

import pytest
from dex import AsyncClient
from dex_agents import (
    AgentSessionClient,
    InterruptKind,
    MessageRole,
    PlanStatus,
    PlanTaskStatus,
)
from dex_agents.mcp import McpToolSource
from tests.integ.conftest import WAIT_TIMEOUT
from tests.integ.ai_agent_sessions import (
    draft_revision,
    is_idle,
    is_waiting_for_timer,
    list_all_messages,
    message_text,
    pending_interrupt,
    send_user_message,
    wait_for_message_text,
    wait_for_snapshot,
)

from dex_examples.app import ExampleApp
from dex_examples.http_app import create_app
from dex_examples.products.ai_agent.ai_agent_session import load_mcp_servers
from dex_examples.products.ai_agent.http_routes import SESSION_TOKEN_HEADER, sign_session_id

PLANNING_REJECTION = "a planning turn can only call write_todos"


@pytest.fixture()
def sessions(app: ExampleApp, client: AsyncClient) -> AgentSessionClient:
    return AgentSessionClient(client, app.ai_agent)


async def test_mcp_tool_source_supports_streamable_http(tmp_path: Path) -> None:
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
        servers = load_mcp_servers(config_path)
        tools = McpToolSource(servers)
        await tools.list_tools()
        result = await tools.call_tool(
            "http_test__lookup", {"query": "streamable"}, idempotency_key="http-test/1"
        )
        assert "found:streamable" in result.output
    finally:
        process.terminate()
        await process.wait()


async def test_local_playground_mcp_servers_list_their_tools() -> None:
    config_path = Path(__file__).parents[2] / "ai-agent" / "mcp-servers.local.yaml"
    servers = load_mcp_servers(config_path)
    assert sorted(server.name for server in servers) == ["google_docs", "search", "slack", "web"]
    tools = await McpToolSource(servers).list_tools()
    assert sorted(tool.name for tool in tools) == [
        "google_docs__create_document",
        "google_docs__search_documents",
        "search__web_search",
        "slack__post_message",
        "slack__search_messages",
        "web__fetch_page",
    ]


async def test_ai_agent_runs_mcp_tools_resources_prompts_and_approvals(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-mcp")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, '/tool test__lookup {"query":"dex"}')
    await wait_for_message_text(sessions, session_id, "Local demo response: found:dex")

    await send_user_message(sessions, session_id, '/tool test__publish {"message":"hello"}')
    approval = await wait_for_snapshot(
        sessions,
        session_id,
        "MCP write approval",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.TOOL_APPROVAL),
    )
    assert approval.tool_name == "test__publish"
    assert await sessions.resolve_interrupt(session_id, approval.interrupt_id, approved=True)
    await wait_for_message_text(sessions, session_id, "Local demo response: published:hello")

    await send_user_message(
        sessions, session_id, '/tool mcp_read_resource {"server":"test","uri":"test://guide"}'
    )
    resource = await wait_for_message_text(sessions, session_id, "Durable MCP resource")
    assert resource.role == MessageRole.TOOL
    assert json.loads(message_text(resource))["contents"][0]["text"] == "Durable MCP resource"
    await send_user_message(
        sessions,
        session_id,
        '/tool mcp_get_prompt {"server":"test","name":"greeting","arguments":{"name":"Dex"}}',
    )
    await wait_for_message_text(sessions, session_id, "Greet Dex")

    await send_user_message(sessions, session_id, '/tool test__missing {"query":"x"}')
    await wait_for_message_text(sessions, session_id, "unknown tool test__missing")
    snapshot = await sessions.get_agent_snapshot(session_id)
    assert not snapshot.pending_interrupts


async def test_ai_agent_asks_a_question_inside_an_executing_plan(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-plan-question")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "/ask What date should I use?", plan_mode=True)
    revision = await wait_for_snapshot(
        sessions,
        session_id,
        "draft plan",
        draft_revision,
    )
    assert await sessions.execute_plan(session_id, revision)
    question = await wait_for_snapshot(
        sessions,
        session_id,
        "question",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.QUESTION),
    )
    assert question.question == "What date should I use?"
    assert await sessions.answer_question(session_id, question.interrupt_id, "September 12")
    assert not await sessions.answer_question(session_id, question.interrupt_id, "again")

    await wait_for_snapshot(
        sessions,
        session_id,
        "completed plan",
        lambda snapshot: snapshot.plan is not None
        and snapshot.plan.status == PlanStatus.COMPLETED
        and not snapshot.pending_interrupts,
    )
    await wait_for_message_text(sessions, session_id, "Local demo response: September 12")
    messages = await list_all_messages(sessions, session_id)
    assert [message.sequence for message in messages] == list(range(1, len(messages) + 1))
    request = next(message for message in messages if message.tool_name == "ask_user")
    answer = next(
        message
        for message in messages
        if message.role == MessageRole.TOOL and message.tool_call_id == request.tool_call_id
    )
    assert answer.sequence > request.sequence
    assert json.loads(message_text(answer))["answer"] == "September 12"


async def test_ai_agent_gives_every_parallel_tool_call_a_result(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-parallel-question")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "/ask-many What date should I use?")
    question = await wait_for_snapshot(
        sessions,
        session_id,
        "question",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.QUESTION),
    )
    assert await sessions.answer_question(session_id, question.interrupt_id, "September 12")
    reply = await wait_for_message_text(
        sessions, session_id, "Local demo response: September 12"
    )
    messages = await list_all_messages(sessions, session_id)
    requested = {message.tool_call_id for message in messages if message.tool_arguments is not None}
    answered = {message.tool_call_id for message in messages if message.role == MessageRole.TOOL}
    assert len(requested) == 2
    assert requested == answered
    assert '"status":"completed"' in reply.text.replace(" ", "")


async def test_ai_agent_demo_model_answers_a_malformed_command_with_its_usage(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-demo-usage")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "/wait soon")
    await wait_for_message_text(sessions, session_id, "Usage: /tool <name> <json>")
    snapshot = await wait_for_snapshot(
        sessions, session_id, "idle", lambda item: item if is_idle(item) else None
    )
    assert snapshot.state is not None and snapshot.state.last_turn_error is None


async def test_ai_agent_offers_question_choices(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-choices")
    await sessions.start_session(session_id)
    await send_user_message(
        sessions, session_id, "/choose Where should I deploy? | Staging | Production"
    )
    question = await wait_for_snapshot(
        sessions,
        session_id,
        "question with choices",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.QUESTION),
    )
    assert question.question == "Where should I deploy?"
    assert question.choices == ["Staging", "Production"]
    assert not question.allows_free_text
    assert not await sessions.answer_question(session_id, question.interrupt_id, "Elsewhere")
    assert await sessions.answer_question(session_id, question.interrupt_id, "Production")
    await wait_for_message_text(sessions, session_id, "Local demo response: Production")


async def test_ai_agent_plans_before_execution(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-plan")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "Research Dex and report back", plan_mode=True)
    plan = await wait_for_snapshot(
        sessions,
        session_id,
        "draft plan",
        lambda snapshot: snapshot.plan if draft_revision(snapshot) is not None else None,
    )
    assert all(task.status == PlanTaskStatus.PENDING for task in plan.tasks)
    assert not await sessions.execute_plan(session_id, plan.revision + 1)
    assert await sessions.execute_plan(session_id, plan.revision)
    assert not await sessions.execute_plan(session_id, plan.revision)
    await wait_for_snapshot(
        sessions,
        session_id,
        "completed plan",
        lambda snapshot: is_idle(snapshot)
        and snapshot.plan is not None
        and snapshot.plan.status == PlanStatus.COMPLETED
        and all(task.status == PlanTaskStatus.COMPLETED for task in snapshot.plan.tasks),
    )
    assert not await sessions.execute_plan(session_id, plan.revision + 1)


async def test_ai_agent_draft_offers_only_the_planning_tool(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-plan-gating")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "Plan a lookup", plan_mode=True)
    await wait_for_snapshot(
        sessions,
        session_id,
        "draft plan",
        lambda snapshot: is_idle(snapshot)
        and snapshot.plan is not None
        and snapshot.plan.status == PlanStatus.DRAFT,
    )
    await send_user_message(sessions, session_id, '/tool test__lookup {"query":"blocked"}')
    await wait_for_message_text(sessions, session_id, PLANNING_REJECTION)
    messages = await list_all_messages(sessions, session_id)
    assert not any("found:blocked" in message_text(message) for message in messages)

    await send_user_message(sessions, session_id, "/plan-clear")
    await wait_for_snapshot(
        sessions,
        session_id,
        "cleared plan",
        lambda snapshot: is_idle(snapshot)
        and (snapshot.plan is None or snapshot.plan.status == PlanStatus.CLEARED),
    )


async def test_ai_agent_keeps_an_unfinished_plan_active(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-plan-active")
    await sessions.start_session(session_id)
    await send_user_message(
        sessions, session_id, "/plan-stop demonstrate an unfinished plan", plan_mode=True
    )
    draft = await wait_for_snapshot(
        sessions,
        session_id,
        "draft plan",
        draft_revision,
    )
    assert await sessions.execute_plan(session_id, draft)
    await wait_for_message_text(
        sessions, session_id, "Local demo response: /plan-stop demonstrate an unfinished plan"
    )
    snapshot = await wait_for_snapshot(
        sessions, session_id, "idle", lambda item: item if is_idle(item) else None
    )
    assert snapshot.plan is not None
    assert snapshot.plan.status == PlanStatus.ACTIVE
    assert any(task.status != PlanTaskStatus.COMPLETED for task in snapshot.plan.tasks)

    await send_user_message(sessions, session_id, "Waiting is not the same as finishing")
    await wait_for_message_text(
        sessions, session_id, "Local demo response: Waiting is not the same as finishing"
    )
    # Only a draft plan limits a turn to write_todos; an active plan does not.
    await send_user_message(sessions, session_id, '/tool test__lookup {"query":"active plan"}')
    await wait_for_message_text(sessions, session_id, "Local demo response: found:active plan")
    await wait_for_snapshot(sessions, session_id, "idle", is_idle)
    assert (await sessions.get_agent_snapshot(session_id)).plan.status == PlanStatus.ACTIVE
    assert await sessions.execute_plan(session_id, draft)


async def test_ai_agent_revises_and_clears_a_draft_before_execution(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-plan-revision")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "Plan the first objective", plan_mode=True)
    first_revision = await wait_for_snapshot(
        sessions,
        session_id,
        "first draft",
        lambda snapshot: snapshot.plan.revision
        if is_idle(snapshot) and snapshot.plan is not None
        else None,
    )
    await send_user_message(sessions, session_id, "Plan the revised objective", plan_mode=True)
    await wait_for_snapshot(
        sessions,
        session_id,
        "revised draft",
        lambda snapshot: is_idle(snapshot)
        and snapshot.plan is not None
        and snapshot.plan.revision > first_revision
        and any("revised objective" in task.content for task in snapshot.plan.tasks),
    )
    assert not await sessions.execute_plan(session_id, first_revision)

    await send_user_message(sessions, session_id, "/plan-clear", plan_mode=True)
    await wait_for_snapshot(
        sessions,
        session_id,
        "cleared plan",
        lambda snapshot: is_idle(snapshot)
        and (snapshot.plan is None or snapshot.plan.status == PlanStatus.CLEARED),
    )


async def test_ai_agent_queues_messages_and_steers_at_a_safe_boundary(
    app: ExampleApp,
    sessions: AgentSessionClient,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    session_id = new_flow_id("ai-agent-steer")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "/wait 30 integration timer")
    await wait_for_snapshot(sessions, session_id, "durable wait", is_waiting_for_timer)
    await send_user_message(sessions, session_id, "replace the current objective")
    await send_user_message(sessions, session_id, "delete this message")
    snapshot = await sessions.get_agent_snapshot(session_id)
    assert [item.message.text for item in snapshot.queued_messages] == [
        "replace the current objective",
        "delete this message",
    ]
    assert is_waiting_for_timer(snapshot)
    assert snapshot.steered_messages == []

    deleted, steered = snapshot.queued_messages[1], snapshot.queued_messages[0]
    assert await sessions.delete_queued_message(session_id, deleted.message_id)
    assert not await sessions.delete_queued_message(session_id, deleted.message_id)
    assert await sessions.steer_user_message(session_id, message_id=steered.message_id)
    assert not await sessions.steer_user_message(session_id, message_id=steered.message_id)

    await wait_for_message_text(
        sessions, session_id, "Local demo response: replace the current objective"
    )
    snapshot = await sessions.get_agent_snapshot(session_id)
    assert not snapshot.queued_messages
    messages = await list_all_messages(sessions, session_id)
    assert not any(message.text == "delete this message" for message in messages)
    wait_result = next(message for message in messages if message.role == MessageRole.TOOL)
    assert json.loads(message_text(wait_result)) == {
        "status": "cancelled",
        "reason": "superseded_by_steered_user_message",
    }

    await client.stop_flow(session_id)
    await client.wait_for_flow(session_id, WAIT_TIMEOUT)

    async def reject_lifecycle_probe(*arguments: object, **keywords: object) -> None:
        raise AssertionError("snapshot reads must not wait for the Flow to close")

    # Dex Agents describes the Flow to report a failed session; a stopped one needs no wait.
    monkeypatch.setattr(client, "wait_for_flow", reject_lifecycle_probe)
    http_client = create_app(app).test_client()
    response = await http_client.get(
        "/products/ai-agent/snapshot",
        query_string={"session_id": session_id},
        headers={SESSION_TOKEN_HEADER: sign_session_id(app.config.agent_session_key, session_id)},
    )
    assert response.status_code == 200
    payload = await response.get_json()
    assert payload["snapshot"]["messages"]
    assert payload["snapshot"]["state"] is not None
    missing_session_id = new_flow_id("missing-ai-agent")
    missing_response = await http_client.get(
        "/products/ai-agent/snapshot",
        query_string={"session_id": missing_session_id},
        headers={
            SESSION_TOKEN_HEADER: sign_session_id(app.config.agent_session_key, missing_session_id)
        },
    )
    assert missing_response.status_code == 404


async def test_ai_agent_starts_one_turn_with_every_steered_message(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-steer-batch")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "/wait 30 integration timer")
    await wait_for_snapshot(sessions, session_id, "durable wait", is_waiting_for_timer)
    assert await sessions.steer_user_message(session_id, text="first replacement objective")
    assert await sessions.steer_user_message(session_id, text="final replacement objective")
    await wait_for_message_text(
        sessions, session_id, "Local demo response: final replacement objective"
    )
    messages = await list_all_messages(sessions, session_id)
    user_texts = [message.text for message in messages if message.role == MessageRole.USER]
    assert user_texts[-2:] == ["first replacement objective", "final replacement objective"]
    assert not (await sessions.get_agent_snapshot(session_id)).steered_messages


async def test_ai_agent_stops_a_turn_without_starting_another(
    sessions: AgentSessionClient,
    new_flow_id: Callable[[str], str],
) -> None:
    session_id = new_flow_id("ai-agent-cancel")
    await sessions.start_session(session_id)
    await send_user_message(sessions, session_id, "/wait 30 integration timer")
    snapshot = await wait_for_snapshot(
        sessions,
        session_id,
        "durable wait",
        lambda item: item if is_waiting_for_timer(item) else None,
    )
    assert await sessions.cancel_turn(session_id, snapshot.state.active_turn_id)
    await wait_for_snapshot(sessions, session_id, "stopped turn", is_idle)
    messages = await list_all_messages(sessions, session_id)
    assert messages[-1].role == MessageRole.TOOL
    assert json.loads(message_text(messages[-1]))["reason"] == "turn_cancelled_by_user"

    await send_user_message(sessions, session_id, "next objective")
    await wait_for_message_text(sessions, session_id, "Local demo response: next objective")


def _available_port() -> int:
    with socket.socket() as server_socket:
        server_socket.bind(("127.0.0.1", 0))
        return int(server_socket.getsockname()[1])


async def _wait_for_port(port: int, process: asyncio.subprocess.Process) -> None:
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


async def test_ai_agent_routes_need_the_token_of_the_session_they_name(
    app: ExampleApp,
    sessions: AgentSessionClient,
) -> None:
    http_client = create_app(app).test_client()
    started = await (await http_client.post("/products/ai-agent/sessions", json={})).get_json()
    other = await (await http_client.post("/products/ai-agent/sessions", json={})).get_json()
    session_id, token = started["session_id"], started["session_token"]
    assert session_id != other["session_id"]
    await send_user_message(sessions, session_id, '/tool test__publish {"message":"token"}')
    interrupt = await wait_for_snapshot(
        sessions,
        session_id,
        "MCP write approval",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.TOOL_APPROVAL),
    )
    approval = {"session_id": session_id, "interrupt_id": interrupt.interrupt_id, "approved": True}
    query = {"session_id": session_id}

    for headers in ({}, {SESSION_TOKEN_HEADER: "wrong"}, {SESSION_TOKEN_HEADER: other["session_token"]}):
        for path in ("snapshot", "history", "events"):
            response = await http_client.get(
                f"/products/ai-agent/{path}", query_string=query, headers=headers
            )
            assert response.status_code == 403, path
        response = await http_client.post(
            "/products/ai-agent/interrupts/resolve", json=approval, headers=headers
        )
        assert response.status_code == 403
        response = await http_client.post(
            "/products/ai-agent/messages", json={**query, "text": "hello"}, headers=headers
        )
        assert response.status_code == 403
    assert pending_interrupt(
        await sessions.get_agent_snapshot(session_id), InterruptKind.TOOL_APPROVAL
    )

    response = await http_client.post(
        "/products/ai-agent/interrupts/resolve",
        json=approval,
        headers={SESSION_TOKEN_HEADER: token},
    )
    assert response.status_code == 200
    assert (await response.get_json())["accepted"] is True
    response = await http_client.get(
        "/products/ai-agent/snapshot", query_string=query, headers={SESSION_TOKEN_HEADER: token}
    )
    assert response.status_code == 200
    assert "Access-Control-Allow-Origin" not in response.headers
    preflight = await http_client.options("/products/ai-agent/interrupts/resolve")
    assert "Access-Control-Allow-Origin" not in preflight.headers
    await wait_for_message_text(sessions, session_id, "Local demo response: published:token")


async def test_ai_agent_session_runs_the_model_its_start_form_picked(
    app: ExampleApp,
    sessions: AgentSessionClient,
) -> None:
    http_client = create_app(app).test_client()
    settings = await (await http_client.get("/products/ai-agent/settings")).get_json()
    assert settings["models"] == ["mock/dex", "mock/dex-mini"]
    assert settings["model"] == "mock/dex"
    for body in ({"model": "openai/gpt-5"}, ["mock/dex"], "mock/dex"):
        refused = await http_client.post("/products/ai-agent/sessions", json=body)
        assert refused.status_code == 400, body

    picked = await (
        await http_client.post("/products/ai-agent/sessions", json={"model": "mock/dex-mini"})
    ).get_json()
    default = await (await http_client.post("/products/ai-agent/sessions", json={})).get_json()
    for started, model in ((picked, "mock/dex-mini"), (default, "mock/dex")):
        session_id = started["session_id"]
        await send_user_message(sessions, session_id, "/model")
        await wait_for_message_text(sessions, session_id, f"Local demo response: {model}")
        snapshot = await sessions.get_agent_snapshot(session_id)
        # Only the model name is stored; the Worker owns the client and its key.
        assert snapshot.session_settings == {"model": model}
