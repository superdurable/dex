# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""MCP tools as agent tools: one tool Step per call, against local stdio and HTTP MCP servers."""

from __future__ import annotations

import asyncio
import base64
import contextlib
import hashlib
import json
import os
import sys
import time
from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
from types import SimpleNamespace
from typing import Any
from uuid import uuid4

import pytest
import pytest_asyncio
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from mcp import MCPError
from mcp_types import CONNECTION_CLOSED, INVALID_PARAMS, REQUEST_TIMEOUT, CallToolResult, Tool
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TURN_CANCELLED_NOTE_HEADER,
    AgentEvent,
    AgentEventType,
    AgentEventWriter,
    AgentInterrupt,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentSnapshot,
    AgentToolCall,
    AgentTracingOptions,
    AgentTurnContext,
    DeferredToolExecution,
    MessageRole,
    QuestionAnswer,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    ToolTaskStarted,
    resolve_tool_policy,
)
from dex_agents.builtin_loop import (
    ScriptedModelClient,
)
from dex_agents.mcp import (
    IDEMPOTENCY_KEY_META_KEY,
    MCP_GET_PROMPT_TOOL_NAME,
    MCP_LIST_RESOURCES_AND_PROMPTS_TOOL_NAME,
    MCP_READ_RESOURCE_TOOL_NAME,
    MCP_TASK_STATUS_EVENT_TYPE,
    MCP_TOOL_PROGRESS_EVENT_TYPE,
    TOOL_APPROVAL_META_KEY,
    TOOL_APPROVAL_RECOMMENDED,
    TOOL_EFFECT_META_KEY,
    TOOL_OUTCOME_META_KEY,
    TOOL_OUTCOME_REJECTED,
    TOOL_OUTCOME_UNKNOWN,
    McpStdioServerConfig,
    McpStreamableHttpServerConfig,
    McpToolAnnotations,
    McpToolCallInterruptedError,
    McpToolSource,
)
from dex_agents.mcp._tasks import GetMcpTaskRequest, PolledMcpTask, UpdateMcpTaskRequest
from dex_agents.mcp._tool_source import (
    _cancel_task,
    _McpToolCall,
    _McpToolCallReporter,
    _McpToolTarget,
    _outcome_from_mcp_error,
    _outcome_from_tool_result,
    _task_question,
    _TaskAsked,
    _TaskPending,
    _ToolCallFailed,
    _ToolCallInterrupted,
    _ToolCallOutcome,
    _ToolCallOutcomeUnknown,
    _ToolCallSucceeded,
)
from dex_agents.mcp._tools import mcp_tool_from_listing
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    FileGate,
    InvocationLog,
    KillableProcess,
    find_free_port,
    wait_for_agent_snapshot,
)
from tests.fake_mcp_server import (
    INVOCATION_LOG_NAME,
    READY_LINE,
    REQUEST_META_LOG_NAME,
    SCREENSHOT_PNG_BASE64,
    STATE_DIR_ENVIRONMENT_VARIABLE,
)
from tests.mcp_agent import (
    CORE_DIR,
    GATEWAY_URL_ENVIRONMENT_VARIABLE,
    HTTP_URL_ENVIRONMENT_VARIABLE,
    TOOL_POLICIES,
    McpScriptedRunner,
    build_mcp_agent_flow,
    build_mcp_tool_source,
    build_media_store,
)
from tests.mcp_stepwise_agent import (
    build_mcp_stepwise_definition,
    build_mcp_stepwise_flow,
)

WORKER_MODULE = "tests.mcp_worker"


@dataclass
class McpAgentHarness:
    stack: AgentTestStack
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path
    http_server: KillableProcess
    http_port: int
    gateway: KillableProcess

    @property
    def http_url(self) -> str:
        return f"http://127.0.0.1:{self.http_port}/mcp"

    def tool_runs(self, tool_name: str) -> list[str]:
        """Idempotency keys of every real run of ``tool_name``, in run order."""
        names = InvocationLog(self.state_dir / INVOCATION_LOG_NAME).names()
        return [name.split(" ", 1)[1] for name in names if name.split(" ", 1)[0] == tool_name]

    def gate(self, name: str) -> FileGate:
        return FileGate(self.state_dir, name)

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

    async def start_http_server(self) -> None:
        await self.http_server.start(
            [sys.executable, "-m", "tests.fake_mcp_server", "http", str(self.http_port)],
            READY_LINE,
            cwd=CORE_DIR,
            env={**os.environ, STATE_DIR_ENVIRONMENT_VARIABLE: str(self.state_dir)},
        )

    async def start_gateway(self, mode: str) -> str:
        """Starts a gateway in front of the HTTP server and returns its URL."""
        port = find_free_port()
        await self.gateway.start(
            [sys.executable, "-m", "tests.fake_mcp_gateway", str(port), self.http_url, mode],
            READY_LINE,
            cwd=CORE_DIR,
        )
        return f"http://127.0.0.1:{port}/mcp"

    async def executed_step_types(self, session_id: str) -> list[str]:
        history = await self.stack.read_step_history(session_id)
        return [entry.step_type for entry in history if entry.event_type == EXECUTE_COMPLETED_EVENT]

    async def wait_for_interrupt(self, session_id: str) -> AgentInterrupt:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: next(iter(snapshot.pending_interrupts), None),
            description="a pending approval",
        )

    async def wait_for_turns(self, session_id: str, turn_count: int) -> AgentSnapshot:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            else None,
            description=f"{turn_count} finished turn(s)",
        )


@pytest_asyncio.fixture
async def mcp_harness(tmp_path: Path) -> AsyncIterator[McpAgentHarness]:
    """Starts a private engine and the HTTP MCP server; each test starts its own Workers."""
    state_dir = tmp_path / "mcp-state"
    state_dir.mkdir()
    http_port = find_free_port()
    http_url = f"http://127.0.0.1:{http_port}/mcp"
    http_server = KillableProcess("fake MCP server", tmp_path / "fake-mcp-server.log")
    flow = build_mcp_agent_flow(state_dir, http_url)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={
            STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir),
            HTTP_URL_ENVIRONMENT_VARIABLE: http_url,
        },
    ) as stack:
        gateway = KillableProcess("fake MCP gateway", tmp_path / "fake-mcp-gateway.log")
        harness = McpAgentHarness(
            stack, flow, stack.sessions(flow), state_dir, http_server, http_port, gateway
        )
        await harness.start_http_server()
        try:
            yield harness
        finally:
            gateway.kill()
            http_server.kill()


async def test_annotations_choose_effects_and_policies_win(
    mcp_harness: McpAgentHarness, caplog: pytest.LogCaptureFixture
) -> None:
    tools = build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)
    listed = {tool.name: tool for tool in await tools.list_tools()}

    assert listed["local__lookup"].framework_effect is ToolEffect.READ
    assert listed["local__lookup"].annotations == McpToolAnnotations(read_only_hint=True)
    assert listed["remote__record_note"].framework_effect is ToolEffect.IDEMPOTENT_WRITE
    # idempotentHint wins over destructiveHint, as in the TypeScript core.
    assert listed["remote__purge_cache"].framework_effect is ToolEffect.IDEMPOTENT_WRITE
    assert listed["local__audit_lookup"].mcp_tool_name == "audit_lookup"
    assert "left MCP tool local/audit.lookup out" in caplog.text
    assert listed["remote__send_message"].framework_effect is None
    assert listed["remote__send_message"].annotations is None
    assert listed["local__send_message"].mcp_tool_name == "send_message"
    assert listed["local__send_message"].description == "Send a message to a person."
    assert listed["local__send_message"].input_schema["required"] == ["text"]
    assert tools.tool_policies()["remote__record_note"] == ToolPolicy(ToolEffect.IDEMPOTENT_WRITE)
    assert "remote__send_message" not in tools.tool_policies()
    audit_lookup = listed["local__audit_lookup"]
    assert resolve_tool_policy(
        audit_lookup.name, TOOL_POLICIES, ToolEffect.UNCLASSIFIED, audit_lookup.framework_effect
    ) == ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=True)

    untrusting = McpToolSource(
        [
            McpStdioServerConfig(
                name="untrusted",
                command=sys.executable,
                args=["-m", "tests.fake_mcp_server", "stdio"],
                env={STATE_DIR_ENVIRONMENT_VARIABLE: str(mcp_harness.state_dir)},
                cwd=str(CORE_DIR),
                trusts_tool_annotations=False,
            )
        ]
    )
    untrusted = {tool.name: tool for tool in await untrusting.list_tools()}
    assert untrusted["untrusted__lookup"].framework_effect is None
    assert untrusted["untrusted__lookup"].annotations == McpToolAnnotations(read_only_hint=True)
    assert untrusting.tool_policies() == {}


async def test_mcp_tool_calls_emit_execute_tool_spans_in_their_activation(
    mcp_harness: McpAgentHarness, tmp_path: Path
) -> None:
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    flow = AgentSessionFlow(
        McpScriptedRunner(
            build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url),
            build_media_store(mcp_harness.state_dir),
        ),
        flow_type="TracedMcpScriptedAgent",
        options=AgentSessionOptions(
            tool_policies=TOOL_POLICIES, tracing=AgentTracingOptions(tracer_provider=provider)
        ),
    )
    async with _in_process_sessions(
        mcp_harness.stack.engine.address, flow, tmp_path / "traced-blob-cache"
    ) as sessions:
        session_id = mcp_harness.new_session_id()
        await sessions.start_session(
            session_id,
            'call local__lookup {"query": "q"} && call remote__send_message {"text": "hi"}',
        )
        interrupt = await wait_for_agent_snapshot(
            sessions,
            session_id,
            lambda snapshot: next(iter(snapshot.pending_interrupts), None),
            description="a pending approval",
        )
        assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
        await wait_for_agent_snapshot(
            sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None and snapshot.state.turn_count == 1
            else None,
            description="the finished turn",
        )
    provider.shutdown()

    spans = [
        span
        for span in exporter.get_finished_spans()
        if (span.attributes or {}).get("gen_ai.conversation.id") == session_id
    ]
    roots = {
        (span.attributes or {}).get("dex.agent.interrupt.id"): span
        for span in spans
        if span.name == "invoke_agent TracedMcpScriptedAgent"
    }
    for name, step_type, effect, root in (
        ("execute_tool local__lookup", "CallTool", "read", roots[None]),
        (
            "execute_tool remote__send_message",
            "CallSingleAttemptTool",
            "unclassified",
            roots[interrupt.interrupt_id],
        ),
    ):
        (span,) = [candidate for candidate in spans if candidate.name == name]
        assert span.parent is not None
        assert span.parent.span_id == root.context.span_id, name
        attributes = dict(span.attributes or {})
        assert attributes["dex.step.type"] == step_type, name
        assert attributes["dex.agent.tool.effect"] == effect, name
        assert attributes["dex.agent.tool_call.status"] == "succeeded", name


async def test_read_tool_reissues_a_call_whose_stream_broke(mcp_harness: McpAgentHarness) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call local__lookup {"query": "q", "crash_first": true}'
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "local__lookup: found q"
    key = f"{session_id}/turn-1/turn-1-call-0"
    assert mcp_harness.tool_runs("lookup") == [key, key]
    assert await mcp_harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    history = await mcp_harness.stack.read_step_history(session_id)
    assert [entry.final_attempt for entry in history if entry.step_type == "CallTool"] == [2]


@pytest.mark.parametrize("server", ["local", "remote"])
async def test_mcp_image_content_becomes_a_reference_the_model_loads(
    mcp_harness: McpAgentHarness, server: str
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(session_id, f"call {server}__take_screenshot {{}}")
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    png = base64.b64decode(SCREENSHOT_PNG_BASE64)
    sha256 = hashlib.sha256(png).hexdigest()
    tool_message = next(
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    )
    assert tool_message.tool_output == "captured the page\n[image 1]"
    (image,) = tool_message.images
    assert (image.sha256, image.mime_type, image.size_bytes) == (sha256, "image/png", len(png))
    assert await build_media_store(mcp_harness.state_dir).get_image(image) == png
    assert SCREENSHOT_PNG_BASE64 not in tool_message.framework_item
    assert snapshot.messages[-1].text == (
        f"{server}__take_screenshot: captured the page\n[image 1] image {sha256} {len(png)}"
    )


async def test_mcp_image_content_that_is_not_an_image_fails_the_call(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__take_screenshot {"broken": true}'
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    tool_message = next(
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    )
    assert tool_message.tool_output == (
        "The tool call failed: MCP tool remote__take_screenshot returned image 1, which is "
        "not a supported image: image is not PNG, JPEG, GIF, or WebP"
    )
    assert tool_message.images == []


async def test_unclassified_write_waits_for_approval_and_runs_once(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__send_message {"text": "hello"}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert interrupt.tool_name == "remote__send_message"
    assert mcp_harness.tool_runs("send_message") == []

    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__send_message: sent hello"
    assert mcp_harness.tool_runs("send_message") == [f"{session_id}/turn-1/turn-1-call-0"]
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps[2:4] == ["AwaitToolApproval", "CallSingleAttemptTool"]
    progress = [
        json.loads(event.payload)
        for event in await _agent_events(mcp_harness, session_id)
        if event.type == MCP_TOOL_PROGRESS_EVENT_TYPE
    ]
    assert progress == [
        {
            "tool_call_id": "turn-1-call-0",
            "tool_name": "remote__send_message",
            "progress": 1,
            "total": 1,
            "message": "sent hello",
        }
    ]


async def test_worker_killed_during_an_mcp_write_records_outcome_unknown(
    mcp_harness: McpAgentHarness,
) -> None:
    worker = await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__send_message {"text": "once", "gate": "send-gate"}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    gate = mcp_harness.gate("send-gate")
    await gate.wait_until_reached()

    worker.kill()
    # The HTTP server finishes the write; a wrongful re-send would log a second run.
    gate.release()
    await mcp_harness.stack.start_worker()
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"remote__send_message: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert mcp_harness.tool_runs("send_message") == [f"{session_id}/turn-1/turn-1-call-0"]
    assert gate.reached_count() == 1
    assert "RecordToolOutcomeUnknown" in await mcp_harness.executed_step_types(session_id)


async def test_broken_stream_during_an_unclassified_write_is_never_reissued(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call local__send_message {"text": "once", "crash": true}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"local__send_message: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert mcp_harness.tool_runs("send_message") == [f"{session_id}/turn-1/turn-1-call-0"]
    steps = await mcp_harness.executed_step_types(session_id)
    assert "RecordToolOutcomeUnknown" in steps
    assert "CallSingleAttemptTool" not in steps


async def test_mcp_server_restart_between_listing_and_call(mcp_harness: McpAgentHarness) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__send_message {"text": "after restart"}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)

    mcp_harness.http_server.kill()
    await mcp_harness.start_http_server()
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__send_message: sent after restart"
    assert mcp_harness.tool_runs("send_message") == [f"{session_id}/turn-1/turn-1-call-0"]


async def test_policy_overrides_annotations_and_annotations_pick_the_step(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id,
        'call local__audit_lookup {"query": "q"} && call remote__record_note {"note": "n"}',
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert interrupt.tool_name == "local__audit_lookup"
    waiting = await mcp_harness.sessions.get_agent_snapshot(session_id)
    assert {call.tool_name: call.effect for call in waiting.tool_calls} == {
        "local__audit_lookup": ToolEffect.NON_IDEMPOTENT_WRITE,
        "remote__record_note": ToolEffect.IDEMPOTENT_WRITE,
    }

    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="not now"
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "local__audit_lookup: not now; remote__record_note: noted n"
    )
    assert mcp_harness.tool_runs("audit_lookup") == []
    assert mcp_harness.tool_runs("record_note") == [f"{session_id}/turn-1/turn-1-call-1"]
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps.count("CallTool") == 1
    assert "CallSingleAttemptTool" not in steps


async def test_an_http_task_is_polled_by_poll_tool_task_on_a_timer(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(session_id, 'call remote__long_report {"topic": "q3"}')
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__long_report: report on q3"
    assert mcp_harness.tool_runs("long_report") == [f"{session_id}/turn-1/turn-1-call-0"]
    events = await _agent_events(mcp_harness, session_id)
    statuses = [
        json.loads(event.payload)["status"]
        for event in events
        if event.type == MCP_TASK_STATUS_EVENT_TYPE
    ]
    # A status that did not change since the last poll writes no event.
    assert statuses == ["working", "completed"]
    started = [
        json.loads(event.payload)
        for event in events
        if event.type == AgentEventType.TOOL_TASK_STARTED
    ]
    assert [payload["tool_name"] for payload in started] == ["remote__long_report"]
    assert await mcp_harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "PollToolTask",
        "PollToolTask",
        "ResumeTurn",
        "RunModel",
    ]


async def test_cancel_turn_cancels_a_running_http_task_and_ends_the_turn_at_once(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__long_report {"topic": "q4", "gate": "report-gate"}'
    )
    await mcp_harness.gate("report-gate").wait_until_reached()

    cancelled_at = time.monotonic()
    assert await mcp_harness.sessions.cancel_turn(session_id)
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)
    assert time.monotonic() - cancelled_at < 15

    (task_id,) = [
        json.loads(event.payload)["task_id"]
        for event in await _agent_events(mcp_harness, session_id)
        if event.type == AgentEventType.TOOL_TASK_STARTED
    ]
    names = InvocationLog(mcp_harness.state_dir / INVOCATION_LOG_NAME).names()
    assert names.count(f"cancel {task_id}") == 1
    assert (snapshot.messages[-1].text or "").startswith(TURN_CANCELLED_NOTE_HEADER)
    assert OUTCOME_UNKNOWN_TOOL_RESULT in (snapshot.messages[-1].text or "")
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps[-3:] == ["CancelToolTask", "ResumeTurn", "ApplyTurnControl"]


async def test_a_stdio_task_is_polled_inside_its_tool_step(mcp_harness: McpAgentHarness) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(session_id, 'call local__long_report {"topic": "q6"}')
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    # The stdio server's task lives in the process that the tool Step holds.
    assert snapshot.messages[-1].text == "local__long_report: report on q6"
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps.count("CallTool") == 1
    assert "PollToolTask" not in steps


async def test_a_task_that_asks_for_input_gets_the_answer_through_tasks_update(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__long_report {"topic": "q7", "ask": true}'
    )
    question = await mcp_harness.wait_for_interrupt(session_id)
    assert (question.interrupt_id, question.question, question.choices) == (
        "turn-1-call-0-input-1",
        "Which format?",
        ["pdf", "html"],
    )
    assert question.allows_free_text is False

    assert await mcp_harness.sessions.answer_question(session_id, question.interrupt_id, "html")
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__long_report: report on q7 as html"
    assert mcp_harness.tool_runs("long_report") == [f"{session_id}/turn-1/turn-1-call-0"]
    updates = [
        name
        for name in InvocationLog(mcp_harness.state_dir / INVOCATION_LOG_NAME).names()
        if name.startswith("update ")
    ]
    assert len(updates) == 1
    assert updates[0].endswith(" html")
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps[2:6] == ["CallTool", "PollToolTask", "AwaitQuestionAnswer", "AwaitQuestionAnswer"]
    assert "PollToolTask" in steps[6:]


async def test_worker_killed_after_the_task_took_an_answer_asks_its_next_identical_question(
    mcp_harness: McpAgentHarness,
) -> None:
    worker = await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id,
        'call remote__long_report {"topic": "q15", "ask": true, "batches": 2, '
        '"update_gate": "update-gate"}',
    )
    first = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.answer_question(session_id, first.interrupt_id, "pdf")
    gate = mcp_harness.gate("update-gate")
    # The server stored "pdf" and asks the identical request for its second batch; the poll that
    # sent it never commits.
    await gate.wait_until_reached()
    await asyncio.sleep(1)
    worker.kill()
    gate.release()
    await mcp_harness.stack.start_worker()

    second = await wait_for_agent_snapshot(
        mcp_harness.sessions,
        session_id,
        lambda snapshot: next(
            (
                interrupt
                for interrupt in snapshot.pending_interrupts
                if interrupt.interrupt_id == "turn-1-call-0-input-2"
            ),
            None,
        ),
        description="the second batch's question",
    )
    assert second.question == "Which format?"
    log = InvocationLog(mcp_harness.state_dir / INVOCATION_LOG_NAME)
    assert [name.split()[-1] for name in log.names() if name.startswith("update ")] == ["pdf"]
    assert await mcp_harness.sessions.answer_question(session_id, second.interrupt_id, "html")
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__long_report: report on q15 as pdf, html"
    assert [name.split()[-1] for name in log.names() if name.startswith("update ")] == [
        "pdf",
        "html",
    ]
    assert mcp_harness.tool_runs("long_report") == [f"{session_id}/turn-1/turn-1-call-0"]
    history = await mcp_harness.stack.read_step_history(session_id)
    assert 2 in [entry.final_attempt for entry in history if entry.step_type == "PollToolTask"]


async def test_a_declined_task_question_is_sent_to_the_task_as_a_decline(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__long_report {"topic": "q13", "ask": true}'
    )
    question = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, question.interrupt_id, approved=False
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    # The task decides what a decline means; its result, not "rejected", ends the call.
    assert snapshot.messages[-1].text == (
        "remote__long_report: report on q13 without a format (decline)"
    )
    updates = [
        name
        for name in InvocationLog(mcp_harness.state_dir / INVOCATION_LOG_NAME).names()
        if name.startswith("update ")
    ]
    assert len(updates) == 1
    assert updates[0].endswith(" decline")


async def test_without_a_poller_the_tool_step_polls_the_task_to_its_end(
    mcp_harness: McpAgentHarness,
) -> None:
    tools = build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)

    result = await tools.execute_deferred_tool_call(
        _deferred_execution(
            "remote__long_report",
            '{"topic": "q14"}',
            "remote",
            "long_report",
            supports_tool_tasks=False,
        )
    )

    assert result == ToolExecutionResult(output="report on q14")


async def test_request_meta_reaches_the_server_but_never_replaces_the_idempotency_key(
    mcp_harness: McpAgentHarness,
) -> None:
    tools = build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)

    result = await tools.execute_deferred_tool_call(
        _deferred_execution(
            "remote__lookup", '{"query": "q21"}', "remote", "lookup", supports_tool_tasks=False
        ),
        request_meta={IDEMPOTENCY_KEY_META_KEY: "forged-key", "com.example/expected": "work"},
    )

    assert result == ToolExecutionResult(output="found q21")
    entries = [
        json.loads(line)
        for line in (mcp_harness.state_dir / REQUEST_META_LOG_NAME).read_text().splitlines()
    ]
    (sent,) = [entry["meta"] for entry in entries if entry["tool"] == "lookup"]
    assert sent[IDEMPOTENCY_KEY_META_KEY] == "session-direct/turn-1/call-1"
    assert sent["com.example/expected"] == "work"


async def test_only_a_session_polled_task_with_its_checkpoint_resumes_without_sending(
    mcp_harness: McpAgentHarness,
) -> None:
    tools = build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)
    saved_checkpoints: list[str] = []

    async def save_heartbeat_checkpoint(checkpoint: str) -> None:
        saved_checkpoints.append(checkpoint)

    first = _deferred_execution(
        "remote__long_report",
        '{"topic": "q22"}',
        "remote",
        "long_report",
        save_heartbeat_checkpoint=save_heartbeat_checkpoint,
    )
    assert not tools.will_resume_tool_task_without_sending(first)
    assert isinstance(await tools.execute_deferred_tool_call(first), ToolTaskStarted)
    (checkpoint,) = saved_checkpoints
    retried = _deferred_execution(
        "remote__long_report",
        '{"topic": "q22"}',
        "remote",
        "long_report",
        heartbeat_checkpoint=checkpoint,
    )
    assert tools.will_resume_tool_task_without_sending(retried)
    other_tool = _deferred_execution(
        "remote__lookup", '{"query": "q22"}', "remote", "lookup", heartbeat_checkpoint=checkpoint
    )
    assert not tools.will_resume_tool_task_without_sending(other_tool)
    # A task polled inside the call is sent again when the server lost it.
    polled_in_call = _deferred_execution(
        "remote__long_report",
        '{"topic": "q22"}',
        "remote",
        "long_report",
        heartbeat_checkpoint=checkpoint,
        supports_tool_tasks=False,
    )
    assert not tools.will_resume_tool_task_without_sending(polled_in_call)


async def test_a_retried_attempt_resumes_its_task_without_calling_the_tool_again(
    mcp_harness: McpAgentHarness,
) -> None:
    tools = build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)
    saved_checkpoints: list[str] = []

    async def save_heartbeat_checkpoint(checkpoint: str) -> None:
        saved_checkpoints.append(checkpoint)

    first = await tools.execute_deferred_tool_call(
        _deferred_execution(
            "remote__long_report",
            '{"topic": "q8"}',
            "remote",
            "long_report",
            save_heartbeat_checkpoint=save_heartbeat_checkpoint,
        )
    )
    assert isinstance(first, ToolTaskStarted)
    assert len(saved_checkpoints) == 1
    retried = await tools.execute_deferred_tool_call(
        _deferred_execution(
            "remote__long_report",
            '{"topic": "q8"}',
            "remote",
            "long_report",
            heartbeat_checkpoint=saved_checkpoints[0],
        )
    )

    assert retried == ToolTaskStarted(task_id=first.task_id, poll_interval_seconds=1)
    long_report_runs = [
        name
        for name in InvocationLog(mcp_harness.state_dir / INVOCATION_LOG_NAME).names()
        if name.startswith("long_report ")
    ]
    assert len(long_report_runs) == 1


async def test_input_required_and_tool_errors_reach_the_model_as_failures(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__ask_account {} && call local__reject_order {"order_id": "7"}'
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    ask_account, reject_order = (snapshot.messages[-1].text or "").split("; ")
    assert ask_account == (
        "remote__ask_account: The tool call failed: the MCP server asked for input that this "
        "agent session cannot provide yet (an answer to 'Which account?'), so the tool did not "
        "finish"
    )
    assert reject_order.startswith("local__reject_order: The tool call failed: ")
    assert "order 7 is already shipped" in reject_order
    finished = [
        json.loads(event.payload)["status"]
        for event in await _agent_events(mcp_harness, session_id)
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
    assert finished == ["failed", "failed"]


@pytest.mark.parametrize("gateway_mode", ["502", "badjson"])
async def test_gateway_error_after_the_server_ran_a_write_records_outcome_unknown(
    mcp_harness: McpAgentHarness, gateway_mode: str
) -> None:
    gateway_url = await mcp_harness.start_gateway(gateway_mode)
    await mcp_harness.stack.start_worker({GATEWAY_URL_ENVIRONMENT_VARIABLE: gateway_url})
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call gateway__send_message {"text": "through"}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    # The server ran the write, so a "failed" record would invite the model to repeat it.
    assert snapshot.messages[-1].text == f"gateway__send_message: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert mcp_harness.tool_runs("send_message") == [f"{session_id}/turn-1/turn-1-call-0"]
    assert "RecordToolOutcomeUnknown" in await mcp_harness.executed_step_types(session_id)


async def test_endless_state_only_input_required_leaves_the_outcome_unknown(
    mcp_harness: McpAgentHarness,
) -> None:
    tools = build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)

    with pytest.raises(McpToolCallInterruptedError, match="kept asking to repeat tools/call"):
        await tools.call_tool("remote__repeat_request", {})


async def test_worker_killed_while_polling_a_task_resumes_the_same_task(
    mcp_harness: McpAgentHarness,
) -> None:
    worker = await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__long_report {"topic": "q4", "gate": "report-gate"}'
    )
    gate = mcp_harness.gate("report-gate")
    # Two polls by PollToolTask, after the tool Step ended.
    await gate.wait_until_reached(2)

    worker.kill()
    await mcp_harness.stack.start_worker()
    gate.release()
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__long_report: report on q4"
    assert mcp_harness.tool_runs("long_report") == [f"{session_id}/turn-1/turn-1-call-0"]
    history = await mcp_harness.stack.read_step_history(session_id)
    assert [entry.final_attempt for entry in history if entry.step_type == "CallTool"] == [1]
    assert gate.reached_count() >= 3


async def test_an_unreachable_server_leaves_the_other_servers_tools_working(
    mcp_harness: McpAgentHarness, caplog: pytest.LogCaptureFixture
) -> None:
    tools = McpToolSource(
        [
            McpStreamableHttpServerConfig(
                name="down", url=f"http://127.0.0.1:{find_free_port()}/mcp"
            ),
            McpStreamableHttpServerConfig(name="remote", url=mcp_harness.http_url),
        ],
        connection_attempts=1,
    )

    listed = {tool.name for tool in await tools.list_tools()}
    assert "remote__lookup" in listed
    assert not any(name.startswith("down__") for name in listed)
    assert "left MCP server 'down' out of the tool listing" in caplog.text
    result = await tools.call_tool("remote__lookup", {"query": "q"})
    assert result.output == "found q"
    with pytest.raises(ToolExecutionError, match="no listed MCP server offers"):
        await tools.call_tool("down__lookup", {"query": "q"})


async def test_a_write_that_was_never_sent_is_recorded_failed(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__send_message {"text": "never sent"}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)

    mcp_harness.http_server.kill()
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert (snapshot.messages[-1].text or "").startswith(
        "remote__send_message: The tool call failed: could not connect to MCP server 'remote', "
        "so send_message did not run"
    )
    assert mcp_harness.tool_runs("send_message") == []
    steps = await mcp_harness.executed_step_types(session_id)
    assert "CallSingleAttemptTool" in steps
    assert "RecordToolOutcomeUnknown" not in steps


async def test_a_server_that_never_answers_the_handshake_is_bounded(
    mcp_harness: McpAgentHarness,
) -> None:
    tools = McpToolSource(
        [
            McpStdioServerConfig(
                name="silent", command=sys.executable, args=["-c", "import time; time.sleep(60)"]
            )
        ],
        connection_timeout=timedelta(seconds=1),
        connection_attempts=1,
    )
    started = time.monotonic()

    with pytest.raises(ToolExecutionError, match="did not run"):
        await tools.call_tool("silent__anything", {})
    assert time.monotonic() - started < 15


async def test_a_server_slower_than_one_poll_gets_the_same_question_asked_again() -> None:
    continuation = _question_continuation(_FORMAT_REQUEST)
    answer = _format_answer()
    # Poll 1 after the answer: tasks/get, tasks/update, tasks/get (server has not consumed it yet).
    session = _ScriptedTaskSession([_input_required(_FORMAT_REQUEST)] * 3)
    client = SimpleNamespace(session=session)
    first = await _scripted_call().poll_task_once(
        client,  # type: ignore[arg-type]
        "t1",
        _answered_record(continuation, is_answer_pending=True),
        _scripted_poll(answer),  # type: ignore[arg-type]
    )
    assert isinstance(first, _TaskPending)
    assert first.poll_interval_seconds == 1
    assert len(session.updates) == 1
    # The poll's commit took the pending answer and stored the continuation it returned; the
    # server still lists the answered request.
    second = await _scripted_call().poll_task_once(
        client,  # type: ignore[arg-type]
        "t1",
        _answered_record(first.continuation, is_answer_pending=False),
        _scripted_poll(None),  # type: ignore[arg-type]
    )
    assert not isinstance(
        second, _TaskAsked
    ), "the user is asked the question they already answered"
    assert len(session.updates) == 1


async def test_an_identical_follow_up_question_is_asked_after_the_task_took_the_answer() -> None:
    """A server asks the same request again under the same key, e.g. "Proceed?" once per batch."""
    continuation = _question_continuation(_FORMAT_REQUEST)
    # Poll 1 (answer pending): get -> input_required; tasks/update; get -> working (answer taken).
    session = _ScriptedTaskSession([_input_required(_FORMAT_REQUEST), _working()])
    client = SimpleNamespace(session=session)
    first = await _scripted_call().poll_task_once(
        client,  # type: ignore[arg-type]
        "t1",
        _answered_record(continuation, is_answer_pending=True),
        _scripted_poll(_format_answer()),  # type: ignore[arg-type]
    )
    assert isinstance(first, _TaskPending) and len(session.updates) == 1
    assert first.continuation is None, "the task left input_required, so nothing stays answered"
    # Polls 2..N: the task asks the identical request for its next batch and waits for the user.
    outcomes = []
    for _ in range(5):
        session.add_snapshot(_input_required(_FORMAT_REQUEST))
        outcomes.append(
            await _scripted_call().poll_task_once(
                client,  # type: ignore[arg-type]
                "t1",
                _answered_record(continuation, is_answer_pending=False),
                _scripted_poll(None),  # type: ignore[arg-type]
            )
        )
    assert any(
        isinstance(outcome, _TaskAsked) for outcome in outcomes
    ), "the task waits for input forever: every poll treats its new question as the answered one"
    assert len(session.updates) == 1


async def test_a_listed_answered_request_is_asked_again_once_the_grace_period_passed() -> None:
    first_poll_session = _ScriptedTaskSession([_input_required(_FORMAT_REQUEST)] * 2)
    first = await _scripted_call().poll_task_once(
        SimpleNamespace(session=first_poll_session),  # type: ignore[arg-type]
        "t1",
        _answered_record(_question_continuation(_FORMAT_REQUEST), is_answer_pending=True),
        _scripted_poll(_format_answer()),  # type: ignore[arg-type]
    )
    assert isinstance(first, _TaskPending) and first.continuation is not None
    saved = json.loads(first.continuation)
    saved["mcp_delivered_at"] -= 11
    session = _ScriptedTaskSession([_input_required(_FORMAT_REQUEST)])
    later = await _scripted_call().poll_task_once(
        SimpleNamespace(session=session),  # type: ignore[arg-type]
        "t1",
        _answered_record(json.dumps(saved), is_answer_pending=False),
        _scripted_poll(None),  # type: ignore[arg-type]
    )
    assert isinstance(later, _TaskAsked)
    assert later.question.question == "Which format?"
    assert session.updates == []


async def test_a_retried_poll_asks_again_instead_of_delivering_an_answer_it_may_have_sent() -> None:
    # Attempt 1 may have delivered "pdf" before the Worker died, and the task may have asked the
    # identical request again since; attempt 2 cannot tell, so the user is asked again.
    session = _ScriptedTaskSession([_input_required(_FORMAT_REQUEST)])
    outcome = await _scripted_call().poll_task_once(
        SimpleNamespace(session=session),  # type: ignore[arg-type]
        "t1",
        _answered_record(_question_continuation(_FORMAT_REQUEST), is_answer_pending=True),
        _scripted_poll(_format_answer(), attempt=2),  # type: ignore[arg-type]
    )
    assert session.updates == []
    assert isinstance(outcome, _TaskAsked)
    assert outcome.question.question == "Which format?"


async def test_a_retried_poll_never_delivers_an_old_answer_to_a_new_question() -> None:
    # Attempt 1 delivered "pdf" and the Worker died before the commit. Meanwhile the server took
    # the answer and asked its NEXT question under the same input key. Attempt 2 reruns with the
    # same record.
    session = _ScriptedTaskSession([_input_required(_CONFIRM_DELETE_REQUEST)] * 2)
    client = SimpleNamespace(session=session)
    outcome = await _scripted_call().poll_task_once(
        client,  # type: ignore[arg-type]
        "t1",
        _answered_record(_question_continuation(_FORMAT_REQUEST), is_answer_pending=True),
        _scripted_poll(_format_answer()),  # type: ignore[arg-type]
    )
    assert (
        session.updates == []
    ), "the answer to 'Which format?' was sent to 'Delete the production table too?'"
    assert isinstance(outcome, _TaskAsked)
    assert outcome.question.question == "Delete the production table too?"


@pytest.mark.parametrize(
    ("code", "is_answer"),
    [(CONNECTION_CLOSED, False), (REQUEST_TIMEOUT, False), (INVALID_PARAMS, True)],
)
async def test_tasks_cancel_counts_only_a_server_answer_as_done(code: int, is_answer: bool) -> None:
    """A dropped connection or timeout is retried; an error the server answered ends it."""

    class _FailingSession:
        async def send_request(self, request: object, result_type: object) -> object:
            raise MCPError(code, "tasks/cancel failed")

    class _Client:
        session = _FailingSession()

    outcome = await _cancel_task(_Client(), "t1")  # type: ignore[arg-type]

    assert isinstance(outcome, _ToolCallSucceeded if is_answer else _ToolCallInterrupted)


_FORMAT_REQUEST: dict[str, Any] = {
    "method": "elicitation/create",
    "params": {
        "mode": "form",
        "message": "Which format?",
        "requestedSchema": {
            "type": "object",
            "properties": {"format": {"type": "string", "enum": ["pdf", "html"]}},
        },
    },
}
_CONFIRM_DELETE_REQUEST: dict[str, Any] = {
    "method": "elicitation/create",
    "params": {
        "mode": "form",
        "message": "Delete the production table too?",
        "requestedSchema": {
            "type": "object",
            "properties": {"format": {"type": "string", "enum": ["pdf", "html"]}},
        },
    },
}


class _ScriptedTaskSession:
    """An MCP client session that answers tasks/get from a script and records tasks/update."""

    def __init__(self, task_snapshots: list[dict[str, Any]]) -> None:
        self._snapshots = list(task_snapshots)
        self.updates: list[dict[str, Any]] = []

    def add_snapshot(self, task_snapshot: dict[str, Any]) -> None:
        self._snapshots.append(task_snapshot)

    async def send_request(self, request: Any, result_type: Any, **_: Any) -> Any:
        if isinstance(request, GetMcpTaskRequest):
            return PolledMcpTask.model_validate(self._snapshots.pop(0))
        if isinstance(request, UpdateMcpTaskRequest):
            self.updates.append(request.params.input_responses)
            return result_type.model_validate({})
        raise AssertionError(f"unexpected {request}")


def _input_required(request: dict[str, Any]) -> dict[str, Any]:
    return {"taskId": "t1", "status": "input_required", "inputRequests": {"format": request}}


def _working() -> dict[str, Any]:
    return {"taskId": "t1", "status": "working"}


def _scripted_call() -> _McpToolCall:
    return _McpToolCall(
        source=SimpleNamespace(_request_timeout_seconds=5.0),  # type: ignore[arg-type]
        target=SimpleNamespace(tool_name="srv__report", mcp_tool_name="report"),  # type: ignore[arg-type]
        arguments={},
        meta={},
        reporter=_McpToolCallReporter("srv__report", "turn-1-call-0", None),
        heartbeat=None,
        save_heartbeat_checkpoint=None,
    )


def _question_continuation(request: dict[str, Any]) -> str:
    question = _task_question("t1", {"format": request})
    assert question is not None and question.continuation is not None
    return question.continuation


def _format_answer() -> QuestionAnswer:
    return QuestionAnswer(
        interrupt_id="turn-1-call-0-input-1", question="Which format?", answer="pdf"
    )


def _answered_record(continuation: str | None, *, is_answer_pending: bool) -> AgentToolCall:
    return AgentToolCall(
        tool_call_id="turn-1-call-0",
        turn_id="turn-1",
        tool_name="srv__report",
        arguments="{}",
        effect=ToolEffect.READ,
        requires_approval=False,
        status=ToolCallStatus.WAITING_FOR_TASK,
        task_id="t1",
        task_poll_interval_seconds=2,
        input_continuation=continuation,
        input_answers=[_format_answer()],
        task_answer_pending=is_answer_pending,
    )


def _scripted_poll(pending_answer: QuestionAnswer | None, *, attempt: int = 1) -> SimpleNamespace:
    return SimpleNamespace(
        input_answers=[_format_answer()], pending_answer=pending_answer, attempt=attempt
    )


@contextlib.asynccontextmanager
async def _in_process_sessions(
    server_address: str, flow: AgentSessionFlow, blob_cache_dir: Path
) -> AsyncIterator[AgentSessionClient]:
    """Serves one Flow from a Worker in the test process and yields its session Client."""
    blob_cache = open_blob_cache(BlobCacheConfig(str(blob_cache_dir), 64 * 1024 * 1024))
    registry = Registry([flow], allow_async_handlers=True)
    worker = AsyncWorker(
        registry,
        blob_cache,
        WorkerOptions(
            server_address=server_address,
            pull=PullWorkerOptions(max_concurrent_tasks=8, stop_drain_timeout=timedelta(seconds=5)),
        ),
    )
    client = AsyncClient(registry, blob_cache, ClientOptions(server_address=server_address))
    serving = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        yield AgentSessionClient(client, flow)
    finally:
        await client.close()
        await worker.stop()
        with contextlib.suppress(asyncio.CancelledError):
            await serving
        blob_cache.close()


def _deferred_execution(
    tool_name: str,
    arguments: str,
    server_name: str,
    mcp_tool_name: str,
    *,
    heartbeat_checkpoint: str | None = None,
    save_heartbeat_checkpoint: Callable[[str], Awaitable[None]] | None = None,
    supports_tool_tasks: bool = True,
) -> DeferredToolExecution:
    """Builds the execution a tool Step passes for one approved MCP call."""

    async def heartbeat() -> None:
        return None

    return DeferredToolExecution(
        flow_id="session-direct",
        tool_call=AgentToolCall(
            tool_call_id="call-1",
            turn_id="turn-1",
            tool_name=tool_name,
            arguments=arguments,
            effect=ToolEffect.READ,
            requires_approval=False,
            status=ToolCallStatus.APPROVED,
            tool_source_reference=json.dumps(
                {"mcp_server": server_name, "mcp_tool": mcp_tool_name}
            ),
        ),
        idempotency_key="session-direct/turn-1/call-1",
        attempt=1 if heartbeat_checkpoint is None else 2,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=heartbeat,
        heartbeat_checkpoint=heartbeat_checkpoint,
        save_heartbeat_checkpoint=save_heartbeat_checkpoint,
        supports_tool_tasks=supports_tool_tasks,
    )


async def _agent_events(harness: McpAgentHarness, session_id: str) -> list[AgentEvent]:
    page = await harness.stack.client.list_stream_messages(
        session_id, harness.flow.agent_events, 500
    )
    return [message.value for message in reversed(page.messages)]


async def test_a_stepwise_agent_offers_mcp_tools_and_waits_for_approval_of_an_mcp_write(
    mcp_harness: McpAgentHarness, tmp_path: Path
) -> None:
    definition = build_mcp_stepwise_definition(mcp_harness.state_dir, mcp_harness.http_url)
    async with _in_process_sessions(
        mcp_harness.stack.engine.address,
        build_mcp_stepwise_flow(definition),
        tmp_path / "stepwise-blob-cache",
    ) as sessions:
        session_id = mcp_harness.new_session_id()
        await sessions.start_session(session_id, 'call remote__send_message {"text": "hello"}')
        interrupt = await wait_for_agent_snapshot(
            sessions,
            session_id,
            lambda snapshot: next(iter(snapshot.pending_interrupts), None),
            description="the approval of an MCP write",
        )
        assert interrupt.tool_name == "remote__send_message"
        assert mcp_harness.tool_runs("send_message") == []
        (record,) = (await sessions.get_agent_snapshot(session_id)).tool_calls
        assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
        snapshot = await _wait_for_stepwise_turns(sessions, session_id, 1)

    assert snapshot.messages[-1].text == "remote__send_message: sent hello"
    assert record.effect is ToolEffect.UNCLASSIFIED
    envelope = json.loads(record.tool_source_reference or "{}")
    assert envelope["stepwise_tool_source"] == "mcp"
    assert json.loads(envelope["reference"]) == {"mcp_server": "remote", "mcp_tool": "send_message"}
    assert mcp_harness.tool_runs("send_message") == [f"{session_id}/turn-1/{record.tool_call_id}"]
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps[2:4] == ["AwaitToolApproval", "CallSingleAttemptTool"]
    assert isinstance(definition.model, ScriptedModelClient)
    offered = [tool.name for tool in definition.model.requests[0].tools]
    # Function tools, then source tools in source order, then built-in tools.
    assert offered[0] == "take_note"
    assert offered[1:3] == ["local__lookup", "local__audit_lookup"]
    assert offered[-4:] == [
        MCP_LIST_RESOURCES_AND_PROMPTS_TOOL_NAME,
        MCP_READ_RESOURCE_TOOL_NAME,
        MCP_GET_PROMPT_TOOL_NAME,
        "ask_user",
    ]
    assert "remote__send_message" in offered


async def test_a_stepwise_agent_polls_an_mcp_tool_task_and_runs_function_tools(
    mcp_harness: McpAgentHarness, tmp_path: Path
) -> None:
    definition = build_mcp_stepwise_definition(mcp_harness.state_dir, mcp_harness.http_url)
    async with _in_process_sessions(
        mcp_harness.stack.engine.address,
        build_mcp_stepwise_flow(definition),
        tmp_path / "stepwise-blob-cache",
    ) as sessions:
        session_id = mcp_harness.new_session_id()
        await sessions.start_session(session_id, 'call remote__long_report {"topic": "q3"}')
        await _wait_for_stepwise_turns(sessions, session_id, 1)
        await sessions.send_user_message(session_id, 'call take_note {"text": "q3 done"}')
        snapshot = await _wait_for_stepwise_turns(sessions, session_id, 2)

    texts = [
        message.text
        for message in snapshot.messages
        if message.role is MessageRole.ASSISTANT and message.text
    ]
    assert texts[-2:] == ["remote__long_report: report on q3", "take_note: kept q3 done"]
    assert len(mcp_harness.tool_runs("long_report")) == 1
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps[:6] == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "PollToolTask",
        "PollToolTask",
        "ResumeTurn",
    ]


async def test_a_stepwise_agent_reads_mcp_resources_and_prompts(
    mcp_harness: McpAgentHarness, tmp_path: Path
) -> None:
    definition = build_mcp_stepwise_definition(mcp_harness.state_dir, mcp_harness.http_url)
    commands = [
        'call mcp_list_resources_and_prompts {"server": "remote"}',
        'call mcp_read_resource {"server": "local", "uri": "memo://greeting"}',
        'call mcp_get_prompt {"server": "remote", "name": "summarize", '
        '"arguments": {"topic": "q3"}}',
        'call mcp_read_resource {"server": "nowhere", "uri": "memo://greeting"}',
    ]
    async with _in_process_sessions(
        mcp_harness.stack.engine.address,
        build_mcp_stepwise_flow(definition),
        tmp_path / "stepwise-blob-cache",
    ) as sessions:
        session_id = mcp_harness.new_session_id()
        await sessions.start_session(session_id, commands[0])
        for turn_count, command in enumerate(commands[1:], start=1):
            await _wait_for_stepwise_turns(sessions, session_id, turn_count)
            await sessions.send_user_message(session_id, command)
        snapshot = await _wait_for_stepwise_turns(sessions, session_id, len(commands))

    outputs = [
        message.tool_output for message in snapshot.messages if message.role is MessageRole.TOOL
    ]
    listing = json.loads(outputs[0] or "")
    assert listing["resources"] == [
        {
            "annotations": {"priority": 1},
            "description": "A greeting memo.",
            "mimeType": "text/plain",
            "name": "greeting",
            "uri": "memo://greeting",
        }
    ]
    # Whole numbers are written as JavaScript writes them, so both cores show the same text.
    assert '"annotations": {"priority": 1}, "description": "A greeting memo."' in (outputs[0] or "")
    assert listing["resource_templates"] == []
    assert [prompt["name"] for prompt in listing["prompts"]] == ["summarize"]
    assert outputs[1] == (
        '{"contents": [{"mimeType": "text/plain", "text": "hello from the memo", '
        '"uri": "memo://greeting"}]}'
    )
    assert outputs[2] == (
        '{"description": "Summarize a topic.", "messages": [{"content": '
        '{"text": "Summarize q3 in one line.", "type": "text"}, "role": "user"}]}'
    )
    assert outputs[3] == "The tool call failed: MCP server nowhere is not configured"
    steps = await mcp_harness.executed_step_types(session_id)
    # Read tools run in CallTool with no approval.
    assert steps.count("CallTool") == len(commands)
    assert "AwaitToolApproval" not in steps


def test_server_config_repr_redacts_environment_and_header_values() -> None:
    stdio = McpStdioServerConfig(
        name="files",
        command="npx",
        args=["server"],
        env={"API_KEY": "stdio-secret", "REGION": "eu-secret"},
    )
    http = McpStreamableHttpServerConfig(
        name="tickets",
        url="https://tickets.example.com/mcp",
        headers={"Authorization": "Bearer http-secret", "X-Api-Key": "key-secret"},
    )
    target = _McpToolTarget(server=http, mcp_tool_name="search", tool_name="tickets__search")

    assert repr(stdio) == (
        "McpStdioServerConfig(name='files', command='npx', args=('server',), "
        "env={'API_KEY': '***', 'REGION': '***'}, cwd=None, trusts_tool_annotations=True)"
    )
    assert repr(http) == (
        "McpStreamableHttpServerConfig(name='tickets', url='https://tickets.example.com/mcp', "
        "headers={'Authorization': '***', 'X-Api-Key': '***'}, trusts_tool_annotations=True)"
    )
    for text in (repr(stdio), str(stdio), repr(http), str(http), repr(target), f"{[stdio, http]}"):
        assert "secret" not in text
    assert "env=None" in repr(McpStdioServerConfig(name="bare", command="npx"))
    assert "headers=None" in repr(
        McpStreamableHttpServerConfig(name="bare", url="http://localhost")
    )
    # The values themselves are unchanged.
    assert stdio.env == {"API_KEY": "stdio-secret", "REGION": "eu-secret"}
    assert http.headers == {"Authorization": "Bearer http-secret", "X-Api-Key": "key-secret"}
    assert stdio == McpStdioServerConfig(
        name="files",
        command="npx",
        args=["server"],
        env={"API_KEY": "stdio-secret", "REGION": "eu-secret"},
    )


async def _wait_for_stepwise_turns(
    sessions: AgentSessionClient, session_id: str, turn_count: int
) -> AgentSnapshot:
    return await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if snapshot.state is not None
        and snapshot.state.turn_count >= turn_count
        and snapshot.state.active_turn_id is None
        else None,
        description=f"{turn_count} finished turn(s)",
    )


# Dex MCP conventions

DEX_CONVENTIONS = json.loads(
    (Path(__file__).resolve().parents[3] / "conformance" / "mcp_dex_conventions.json").read_text()
)


def test_the_dex_meta_keys_are_the_conformance_suite_keys() -> None:
    assert DEX_CONVENTIONS["metaKeys"] == {
        "idempotencyKey": IDEMPOTENCY_KEY_META_KEY,
        "toolEffect": TOOL_EFFECT_META_KEY,
        "toolApproval": TOOL_APPROVAL_META_KEY,
        "toolOutcome": TOOL_OUTCOME_META_KEY,
    }
    assert DEX_CONVENTIONS["values"] == {
        "toolEffects": ["read", "idempotent_write", "non_idempotent_write"],
        "toolApprovalRecommended": TOOL_APPROVAL_RECOMMENDED,
        "toolOutcomeUnknown": TOOL_OUTCOME_UNKNOWN,
        "toolOutcomeRejected": TOOL_OUTCOME_REJECTED,
    }


@pytest.mark.parametrize(
    "case", DEX_CONVENTIONS["definitionCases"], ids=lambda case: str(case["name"])
)
def test_dex_tool_definitions_map_as_the_conformance_suite_says(case: dict[str, Any]) -> None:
    tool = mcp_tool_from_listing(
        "srv", Tool.model_validate(case["tool"]), case["trustsToolAnnotations"]
    )
    session_policy = case["sessionPolicy"]
    tool_policies = (
        {}
        if session_policy is None
        else {
            tool.name: ToolPolicy(
                ToolEffect(session_policy["effect"]), session_policy.get("requiresApproval")
            )
        }
    )
    source = McpToolSource([])
    call = source.deferred_tool_call(
        _dex_turn(tool_policies),
        tool_call_id="call-1",
        tool_name=tool.name,
        arguments="{}",
        listed_tool=tool,
    )
    # A framework adapter resolves the tool against the merged policies instead.
    source._tools = {tool.name: tool}
    whole_turn_policy = resolve_tool_policy(
        tool.name,
        source.merge_tool_policies(tool_policies),
        ToolEffect.UNCLASSIFIED,
        tool.framework_effect,
    )

    expected = case["expected"]
    assert tool.framework_effect == expected["frameworkEffect"]
    assert tool.dex_effect == expected["dexEffect"]
    assert tool.is_approval_recommended is expected["isApprovalRecommended"]
    assert call.policy.effect == expected["policy"]["effect"]
    assert call.policy.is_approval_required is expected["policy"]["requiresApproval"]
    assert call.ordered is expected["ordered"]
    assert whole_turn_policy == ToolPolicy(
        ToolEffect(expected["policy"]["effect"]), expected["policy"]["requiresApproval"]
    )


@pytest.mark.parametrize("case", DEX_CONVENTIONS["resultCases"], ids=lambda case: str(case["name"]))
def test_dex_tool_results_map_as_the_conformance_suite_says(case: dict[str, Any]) -> None:
    outcome = _outcome_from_tool_result(CallToolResult.model_validate(case["result"]))

    assert _describe_dex_outcome(outcome) == case["expected"]


@pytest.mark.parametrize("case", DEX_CONVENTIONS["errorCases"], ids=lambda case: str(case["name"]))
def test_dex_error_responses_map_as_the_conformance_suite_says(case: dict[str, Any]) -> None:
    error = case["error"]
    outcome = _outcome_from_mcp_error(MCPError(error["code"], error["message"], error.get("data")))

    assert _describe_dex_outcome(outcome) == case["expected"]


async def test_dex_meta_declares_effect_and_approval_and_a_policy_only_raises_safety(
    mcp_harness: McpAgentHarness,
) -> None:
    tools = build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)
    listed = {tool.name: tool for tool in await tools.list_tools()}
    assert listed["remote__post_note"].dex_effect is ToolEffect.NON_IDEMPOTENT_WRITE
    assert listed["remote__post_note"].annotations == McpToolAnnotations(read_only_hint=True)
    assert listed["remote__send_payment"].is_approval_recommended
    assert tools.tool_policies()["remote__send_payment"] == ToolPolicy(
        ToolEffect.IDEMPOTENT_WRITE, requires_approval=True
    )
    assert tools.tool_policies()["remote__post_note"] == ToolPolicy(
        ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False
    )
    # An application policy never lowers a declared effect, and keeps a recommended approval.
    merged = tools.merge_tool_policies(
        {
            "remote__post_note": ToolPolicy(ToolEffect.READ),
            "remote__send_payment": ToolPolicy(ToolEffect.READ),
            "remote__lookup": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
            "local_only": ToolPolicy(ToolEffect.READ),
        }
    )
    assert merged["remote__post_note"] == ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, False)
    assert merged["remote__send_payment"] == ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, True)
    assert merged["remote__get_balance"] == ToolPolicy(ToolEffect.READ, False)
    assert merged["remote__lookup"] == ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE)
    assert merged["local_only"] == ToolPolicy(ToolEffect.READ)

    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id,
        'call remote__get_balance {"account": "a"} && call remote__post_note {"text": "hi"} '
        '&& call remote__send_payment {"amount": 5}',
    )
    first = await _wait_for_interrupt_of(mcp_harness, session_id, "remote__get_balance")
    waiting = await mcp_harness.sessions.get_agent_snapshot(session_id)
    # A non-idempotent write policy raises the declared read; its default asks for approval.
    assert {
        call.tool_name: (call.effect, call.requires_approval, call.ordered_write)
        for call in waiting.tool_calls
    } == {
        "remote__get_balance": (ToolEffect.NON_IDEMPOTENT_WRITE, True, True),
        "remote__post_note": (ToolEffect.NON_IDEMPOTENT_WRITE, False, True),
        "remote__send_payment": (ToolEffect.IDEMPOTENT_WRITE, True, True),
    }
    # post_note waits for the earlier ordered write, get_balance.
    assert mcp_harness.tool_runs("post_note") == []
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, first.interrupt_id, approved=True
    )
    # post_note runs without approval; send_payment's approval is raised once its turn comes.
    second = await _wait_for_interrupt_of(mcp_harness, session_id, "remote__send_payment")
    assert mcp_harness.tool_runs("get_balance") == [f"{session_id}/turn-1/turn-1-call-0"]
    assert mcp_harness.tool_runs("post_note") == [f"{session_id}/turn-1/turn-1-call-1"]
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, second.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "remote__get_balance: balance of a is 5; remote__post_note: posted hi; "
        "remote__send_payment: paid 5"
    )
    steps = await mcp_harness.executed_step_types(session_id)
    assert steps.count("CallTool") == 1
    assert steps.count("CallSingleAttemptTool") == 2


@pytest.mark.parametrize("outcome", ["unknown", "unknown_error"])
async def test_a_dex_outcome_unknown_is_recorded_once_and_never_retried(
    mcp_harness: McpAgentHarness, outcome: str
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, f'call remote__send_payment {{"amount": 5, "outcome": "{outcome}"}}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    # An idempotent write runs in CallTool, which would retry a Step failure.
    assert snapshot.messages[-1].text == f"remote__send_payment: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert mcp_harness.tool_runs("send_payment") == [f"{session_id}/turn-1/turn-1-call-0"]
    history = await mcp_harness.stack.read_step_history(session_id)
    assert [entry.final_attempt for entry in history if entry.step_type == "CallTool"] == [1]
    assert "RecordToolOutcomeUnknown" not in await mcp_harness.executed_step_types(session_id)
    finished = [
        json.loads(event.payload)["status"]
        for event in await _agent_events(mcp_harness, session_id)
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
    assert finished == ["outcome_unknown"]


@pytest.mark.parametrize("outcome", ["rejected", "rejected_error"])
async def test_a_dex_outcome_rejected_is_recorded_failed_and_never_retried(
    mcp_harness: McpAgentHarness, outcome: str
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, f'call remote__send_payment {{"amount": 5, "outcome": "{outcome}"}}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    message = (
        "the card was declined"
        if outcome == "rejected"
        else "MCP error -32603: the card was declined"
    )
    assert snapshot.messages[-1].text == f"remote__send_payment: The tool call failed: {message}"
    assert mcp_harness.tool_runs("send_payment") == [f"{session_id}/turn-1/turn-1-call-0"]
    history = await mcp_harness.stack.read_step_history(session_id)
    assert [entry.final_attempt for entry in history if entry.step_type == "CallTool"] == [1]


async def test_retries_of_a_dex_write_carry_one_idempotency_key_so_the_server_applies_it_once(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call local__send_payment {"amount": 5, "crash_after_apply": true}'
    )
    interrupt = await mcp_harness.wait_for_interrupt(session_id)
    assert await mcp_harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "local__send_payment: payment of 5 already applied"
    key = f"{session_id}/turn-1/turn-1-call-0"
    assert mcp_harness.tool_runs("send_payment") == [key, key]
    assert mcp_harness.tool_runs("payment-applied") == [key]
    history = await mcp_harness.stack.read_step_history(session_id)
    assert [entry.final_attempt for entry in history if entry.step_type == "CallTool"] == [2]


async def test_a_task_result_with_the_dex_outcome_unknown_is_recorded_by_poll_tool_task(
    mcp_harness: McpAgentHarness,
) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(
        session_id, 'call remote__long_report {"topic": "q3", "outcome": "unknown"}'
    )
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"remote__long_report: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert mcp_harness.tool_runs("long_report") == [f"{session_id}/turn-1/turn-1-call-0"]
    history = await mcp_harness.stack.read_step_history(session_id)
    polls = [entry.final_attempt for entry in history if entry.step_type == "PollToolTask"]
    assert polls and all(attempt == 1 for attempt in polls)
    assert "RecordToolOutcomeUnknown" not in await mcp_harness.executed_step_types(session_id)


async def _wait_for_interrupt_of(
    harness: McpAgentHarness, session_id: str, tool_name: str
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(
            (
                interrupt
                for interrupt in snapshot.pending_interrupts
                if interrupt.tool_name == tool_name
            ),
            None,
        ),
        description=f"the approval of {tool_name}",
    )


def _dex_turn(tool_policies: dict[str, ToolPolicy]) -> AgentTurnContext:
    async def heartbeat() -> None:
        return None

    return AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=(),
        framework_run_state=None,
        tool_calls={},
        tool_policies=tool_policies,
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=heartbeat,
    )


def _describe_dex_outcome(outcome: _ToolCallOutcome) -> dict[str, str | None]:
    if isinstance(outcome, _ToolCallSucceeded):
        return {"status": "succeeded", "text": outcome.output}
    if isinstance(outcome, _ToolCallFailed):
        return {"status": "failed", "text": outcome.message}
    if isinstance(outcome, _ToolCallOutcomeUnknown):
        return {"status": "outcome_unknown", "text": outcome.message}
    if isinstance(outcome, _ToolCallInterrupted):
        return {"status": "interrupted", "text": None}
    raise AssertionError(f"unexpected {outcome}")
