# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turn Step shapes, history replay, snapshots, queued messages, and live events."""

from __future__ import annotations

import asyncio
import contextlib
import json
from collections.abc import AsyncIterator, Sequence
from datetime import timedelta
from pathlib import Path

from agents import Agent, Model, ModelProvider, RunConfig, function_tool, handoff
from dex import (
    AsyncWorker,
    BlobCacheConfig,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from opentelemetry.sdk.trace import ReadableSpan, TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.trace import SpanKind

from dex_agents import (
    AGENT_STATE,
    FRAMEWORK_RUN_STATE,
    AgentEventType,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentSessionStatus,
    AgentTracingOptions,
    AgentUsage,
    MessageRole,
    ToolEffect,
    ToolPolicy,
)
from dex_agents.testing import AgentTestStack, InvocationLog, wait_for_agent_snapshot, wait_until
from dex_agents_openai_agents import OpenAIAgentsRunner
from tests.conftest import WORKER_MODULE, SupportAgentHarness
from tests.scripted_model import CommandScriptedModel

MODEL_CALL_USAGE = AgentUsage(
    model_calls=1, input_tokens=10, output_tokens=5, cached_input_tokens=2, reasoning_tokens=1
)
SCRIPTED_MODEL_NAME = "scripted-model"
# The SDK's ScriptedModel answers with this response ID unless a step sets another.
SCRIPTED_RESPONSE_ID = "resp-789"
_MODEL_ATTRIBUTE_PREFIXES = (
    "gen_ai.request.",
    "gen_ai.response.",
    "gen_ai.provider.",
    "gen_ai.system",
)


async def test_read_tool_turn_runs_model_tool_and_resume_steps_and_replays_history(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    first = await harness.wait_for_turns(session_id, 1)

    assert [
        (message.role, message.tool_call_id, message.tool_name) for message in first.messages
    ] == [
        (MessageRole.USER, None, None),
        (MessageRole.ASSISTANT, "call-1-0-0", "lookup"),
        (MessageRole.TOOL, "call-1-0-0", "lookup"),
        (MessageRole.ASSISTANT, None, None),
    ]
    assert first.messages[1].tool_arguments == '{"query": "docs"}'
    assert first.messages[2].tool_output == "found docs"
    assert first.messages[-1].text == "lookup: found docs"
    assert first.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    assert harness.invocations.names() == ["model:1:1", "lookup", "model:1:2"]

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == ('history: call lookup {"query": "docs"}; tools: found docs')
    assert [message.turn_id for message in second.messages] == ["turn-1"] * 4 + ["turn-2"] * 2
    assert harness.tool_runs() == ["lookup"]


async def test_snapshot_returns_the_conversation_after_a_completed_turn(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.last_turn_error is None
    assert [(message.sequence, message.role, message.text) for message in snapshot.messages] == [
        (1, MessageRole.USER, "say hi"),
        (2, MessageRole.ASSISTANT, "said: hi"),
    ]
    assert json.loads(snapshot.messages[0].framework_item) == {"role": "user", "content": "say hi"}
    answer_item = json.loads(snapshot.messages[1].framework_item)
    assert (answer_item["type"], answer_item["role"]) == ("message", "assistant")
    assert answer_item["content"][0]["text"] == "said: hi"
    assert snapshot.usage == MODEL_CALL_USAGE
    assert snapshot.pending_interrupts == []
    assert snapshot.tool_calls == []
    assert snapshot.queued_message_count == 0
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "AwaitUserMessage",
        "RunModel",
    ]

    values = await harness.stack.client.get_attributes(session_id, AGENT_STATE, FRAMEWORK_RUN_STATE)
    assert values[AGENT_STATE] == snapshot.state
    assert FRAMEWORK_RUN_STATE not in values


async def test_a_message_sent_during_a_turn_waits_in_the_queue(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall busy-gate")
    gate = harness.gate("busy-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.send_user_message(session_id, "say later")

    async def queued_count() -> int:
        return (await harness.sessions.get_agent_snapshot(session_id)).queued_message_count

    assert await wait_until("one queued message", queued_count) == 1
    during = await harness.sessions.get_agent_snapshot(session_id)
    assert during.state is not None
    assert during.state.status is AgentSessionStatus.RUNNING_MODEL

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert [message.text for message in snapshot.messages] == [
        "stall busy-gate",
        "thinking before busy-gate",
        "stalled at busy-gate",
        "say later",
        "said: later",
    ]
    assert snapshot.queued_message_count == 0


async def test_text_and_reasoning_summary_deltas_reach_agent_events(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "think hard")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.text) for message in snapshot.messages] == [
        (MessageRole.USER, "think hard"),
        (MessageRole.ASSISTANT, None),
        (MessageRole.ASSISTANT, "thought: hard"),
    ]
    assert json.loads(snapshot.messages[1].framework_item)["type"] == "reasoning"
    events = await harness.list_agent_events(session_id)
    deltas = [
        (event.type, json.loads(event.payload)["text"], event.turn_id, event.attempt)
        for event in events
        if event.type in (AgentEventType.TEXT_DELTA, AgentEventType.REASONING_DELTA)
    ]
    assert deltas == [
        (AgentEventType.REASONING_DELTA, "pondering hard", "turn-1", 1),
        (AgentEventType.TEXT_DELTA, "thought: hard", "turn-1", 1),
    ]
    assert all(event.step_execution_id for event in events)
    usage_events = [event for event in events if event.type == AgentEventType.USAGE_RECORDED]
    assert json.loads(usage_events[-1].payload)["recorded"]["reasoning_tokens"] == 1


async def test_chat_spans_carry_the_requested_model_name_and_the_response_id(
    tmp_path: Path,
) -> None:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    flow = _build_named_model_flow(state_dir, provider)
    async with (
        AgentTestStack(tmp_path, [flow], WORKER_MODULE) as stack,
        _in_process_worker(stack, [flow], tmp_path / "worker-blob-cache"),
    ):
        sessions = stack.sessions(flow)
        tool_session_id = "session-named-model-tool-call"
        handoff_session_id = "session-named-model-handoff"
        await sessions.start_session(tool_session_id, 'call lookup {"query": "docs"}')
        await sessions.start_session(handoff_session_id, "handoff billing")
        for session_id in (tool_session_id, handoff_session_id):
            await wait_for_agent_snapshot(
                sessions,
                session_id,
                lambda snapshot: snapshot
                if snapshot.state is not None and snapshot.state.turn_count == 1
                else None,
                description="the first finished turn",
            )
    provider.shutdown()

    spans = exporter.get_finished_spans()
    # ToolCallsRequested, then TurnCompleted: one model call each.
    assert [_describe_chat_span(span) for span in _chat_spans(spans, tool_session_id)] == [
        (
            "chat scripted-model",
            SpanKind.CLIENT,
            {
                "gen_ai.request.model": SCRIPTED_MODEL_NAME,
                "gen_ai.response.id": SCRIPTED_RESPONSE_ID,
            },
        )
    ] * 2
    # The handoff makes the Billing agent's model call in the same RunModel.
    assert [_describe_chat_span(span) for span in _chat_spans(spans, handoff_session_id)] == [
        ("chat scripted-model", SpanKind.INTERNAL, {"gen_ai.request.model": SCRIPTED_MODEL_NAME})
    ]


class _ScriptedModelProvider(ModelProvider):
    def __init__(self, model: Model) -> None:
        self._model = model

    def get_model(self, model_name: str | None) -> Model:
        if model_name != SCRIPTED_MODEL_NAME:
            raise ValueError(f"no scripted model named {model_name!r}")
        return self._model


def _build_named_model_flow(state_dir: Path, provider: TracerProvider) -> AgentSessionFlow:
    @function_tool
    def lookup(query: str) -> str:
        """Search the knowledge base."""
        return f"found {query}"

    billing = Agent(name="Billing", instructions="Handle billing.", model=SCRIPTED_MODEL_NAME)
    assistant = Agent(
        name="Assistant",
        instructions="Help the user.",
        model=SCRIPTED_MODEL_NAME,
        tools=[lookup],
        handoffs=[handoff(billing)],
    )
    model = CommandScriptedModel(state_dir, InvocationLog(state_dir / "invocations.log"))
    return AgentSessionFlow(
        OpenAIAgentsRunner(
            assistant,
            run_config=RunConfig(
                model_provider=_ScriptedModelProvider(model), tracing_disabled=True
            ),
        ),
        flow_type="NamedModelSupportAgent",
        options=AgentSessionOptions(
            tool_policies={"lookup": ToolPolicy(ToolEffect.READ)},
            tracing=AgentTracingOptions(tracer_provider=provider),
        ),
    )


def _chat_spans(spans: Sequence[ReadableSpan], session_id: str) -> list[ReadableSpan]:
    return sorted(
        (
            span
            for span in spans
            if (span.attributes or {}).get("gen_ai.operation.name") == "chat"
            and (span.attributes or {}).get("gen_ai.conversation.id") == session_id
        ),
        key=lambda span: span.start_time or 0,
    )


def _describe_chat_span(span: ReadableSpan) -> tuple[str, SpanKind, dict[str, object]]:
    model_attributes: dict[str, object] = {
        name: value
        for name, value in (span.attributes or {}).items()
        if name.startswith(_MODEL_ATTRIBUTE_PREFIXES)
    }
    return span.name, span.kind, model_attributes


@contextlib.asynccontextmanager
async def _in_process_worker(
    stack: AgentTestStack, flows: Sequence[AgentSessionFlow], blob_cache_dir: Path
) -> AsyncIterator[None]:
    """Serve ``flows`` from this process, so the test's TracerProvider records RunModel."""
    blob_cache = open_blob_cache(BlobCacheConfig(str(blob_cache_dir), 64 * 1024 * 1024))
    worker = AsyncWorker(
        Registry(list(flows), allow_async_handlers=True),
        blob_cache,
        WorkerOptions(
            server_address=stack.engine.address,
            pull=PullWorkerOptions(max_concurrent_tasks=8, stop_drain_timeout=timedelta(seconds=5)),
        ),
    )
    serving = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        yield
    finally:
        await worker.stop()
        with contextlib.suppress(asyncio.CancelledError):
            await serving
        blob_cache.close()
