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
import contextlib
import json
import os
import sys
import time
from collections.abc import AsyncIterator
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
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
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentEvent,
    AgentEventType,
    AgentInterrupt,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentSnapshot,
    AgentTracingOptions,
    ToolEffect,
    ToolExecutionError,
    ToolPolicy,
    resolve_tool_policy,
)
from dex_agents.mcp import (
    MCP_TASK_STATUS_EVENT_TYPE,
    MCP_TOOL_PROGRESS_EVENT_TYPE,
    McpStdioServerConfig,
    McpStreamableHttpServerConfig,
    McpToolAnnotations,
    McpToolCallInterruptedError,
    McpToolSource,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    FileGate,
    InvocationLog,
    KillableProcess,
    find_free_port,
    wait_for_agent_snapshot,
)
from tests.fake_mcp_server import INVOCATION_LOG_NAME, READY_LINE, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.mcp_agent import (
    CORE_DIR,
    GATEWAY_URL_ENVIRONMENT_VARIABLE,
    HTTP_URL_ENVIRONMENT_VARIABLE,
    TOOL_POLICIES,
    McpScriptedRunner,
    build_mcp_agent_flow,
    build_mcp_tool_source,
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
        McpScriptedRunner(build_mcp_tool_source(mcp_harness.state_dir, mcp_harness.http_url)),
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


async def test_task_handle_is_polled_inside_the_tool_step(mcp_harness: McpAgentHarness) -> None:
    await mcp_harness.stack.start_worker()
    session_id = mcp_harness.new_session_id()
    await mcp_harness.sessions.start_session(session_id, 'call remote__long_report {"topic": "q3"}')
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__long_report: report on q3"
    assert mcp_harness.tool_runs("long_report") == [f"{session_id}/turn-1/turn-1-call-0"]
    statuses = [
        json.loads(event.payload)["status"]
        for event in await _agent_events(mcp_harness, session_id)
        if event.type == MCP_TASK_STATUS_EVENT_TYPE
    ]
    assert statuses == ["working", "working", "completed"]
    assert (await mcp_harness.executed_step_types(session_id)).count("CallTool") == 1


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
    # Two polls: the attempt saved the task ID as its heartbeat checkpoint before the first.
    await gate.wait_until_reached(2)

    worker.kill()
    await mcp_harness.stack.start_worker()
    gate.release()
    snapshot = await mcp_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "remote__long_report: report on q4"
    assert mcp_harness.tool_runs("long_report") == [f"{session_id}/turn-1/turn-1-call-0"]
    history = await mcp_harness.stack.read_step_history(session_id)
    assert [entry.final_attempt for entry in history if entry.step_type == "CallTool"] == [2]


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


async def _agent_events(harness: McpAgentHarness, session_id: str) -> list[AgentEvent]:
    page = await harness.stack.client.list_stream_messages(
        session_id, harness.flow.agent_events, 500
    )
    return [message.value for message in reversed(page.messages)]
