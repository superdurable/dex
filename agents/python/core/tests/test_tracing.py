# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""OpenTelemetry GenAI spans: one trace per activation, linked across durable waits."""

from __future__ import annotations

import asyncio
import contextlib
import json
import re
from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass, replace
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest_asyncio
from dex import (
    AsyncWorker,
    BlobCacheConfig,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from opentelemetry import trace
from opentelemetry.context import Context as OpenTelemetryContext
from opentelemetry.sdk.trace import ReadableSpan, Span, SpanProcessor, TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.sdk.trace.sampling import Decision, ParentBased, Sampler, SamplingResult
from opentelemetry.trace import SpanKind, StatusCode

from dex_agents import (
    AGENT_TRACE_CONTEXT,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTracingOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    GenAISemanticConventionsVersion,
    ModelCallMetadata,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    FileGate,
    InvocationLog,
    wait_for_agent_snapshot,
    wait_until,
)
from tests.scripted_agent import (
    INVOCATION_LOG_NAME,
    SCRIPTED_AGENT_FLOW_TYPE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    TOOL_POLICIES,
    ScriptedRunner,
    build_scripted_agent_flow,
)
from tests.worker_without_opentelemetry import BLOCKED_IMPORT_LOG_ENVIRONMENT_VARIABLE

CORE_DIR = Path(__file__).resolve().parents[1]
TRACED_USAGE = AgentUsage(
    model_calls=1, input_tokens=10, output_tokens=5, cached_input_tokens=4, reasoning_tokens=2
)
LATEST_SCHEMA_URL = "https://opentelemetry.io/schemas/gen-ai-dev/1.42.0-dev"
# Spans of Flows without a tracer_provider; the global provider can be set once per process.
GLOBAL_EXPORTER = InMemorySpanExporter()


class ScriptedRunnerWithModelMetadata(ScriptedRunner):
    def __init__(self, state_dir: Path, usage: AgentUsage = TRACED_USAGE) -> None:
        super().__init__(state_dir)
        self._usage = usage

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        outcome = await super().run_model_step(turn)
        return replace(
            outcome,
            usage=self._usage,
            model_call_metadata=ModelCallMetadata(
                request_model="scripted-model",
                response_model="scripted-model-2026-11",
                provider_name="scripted",
                response_id=f"{turn.turn_id}-attempt-{turn.attempt}",
                finish_reasons=("stop",),
            ),
        )


class OpenSpanCounter(SpanProcessor):
    """Counts spans that started and have not ended."""

    def __init__(self) -> None:
        self.open_span_count = 0

    def on_start(self, span: Span, parent_context: OpenTelemetryContext | None = None) -> None:
        self.open_span_count += 1

    def on_end(self, span: ReadableSpan) -> None:
        self.open_span_count -= 1


class AlternatingRootSampler(Sampler):
    """Samples every other root span: a deterministic 50% rate."""

    def __init__(self) -> None:
        self.root_count = 0

    def should_sample(self, *args: Any, **kwargs: Any) -> SamplingResult:
        self.root_count += 1
        is_sampled = self.root_count % 2 == 1
        return SamplingResult(Decision.RECORD_AND_SAMPLE if is_sampled else Decision.DROP)

    def get_description(self) -> str:
        return "AlternatingRootSampler"


class RaisingSpanProcessor(SpanProcessor):
    """A broken processor: it raises when any span starts or ends."""

    def on_start(self, span: Span, parent_context: OpenTelemetryContext | None = None) -> None:
        raise RuntimeError("span processor failed on start")

    def on_end(self, span: ReadableSpan) -> None:
        raise RuntimeError("span processor failed on end")


@dataclass
class TracingHarness:
    stack: AgentTestStack
    exporter: InMemorySpanExporter
    sampled_exporter: InMemorySpanExporter
    open_spans: OpenSpanCounter
    state_dir: Path
    traced_flow: AgentSessionFlow
    content_flow: AgentSessionFlow
    legacy_flow: AgentSessionFlow
    untraced_flow: AgentSessionFlow
    global_flow: AgentSessionFlow
    faulty_processor_flow: AgentSessionFlow
    zero_usage_flow: AgentSessionFlow
    harness_flow: AgentSessionFlow
    sampled_flow: AgentSessionFlow

    def sessions(self, flow: AgentSessionFlow) -> AgentSessionClient:
        return self.stack.sessions(flow)

    def spans(self, session_id: str) -> list[ReadableSpan]:
        return [
            span
            for exporter in (self.exporter, GLOBAL_EXPORTER)
            for span in exporter.get_finished_spans()
            if _attributes(span).get("gen_ai.conversation.id") == session_id
        ]

    async def wait_for_spans(self, session_id: str, count: int) -> list[ReadableSpan]:
        async def probe() -> list[ReadableSpan] | None:
            spans = self.spans(session_id)
            return spans if len(spans) >= count else None

        spans = await wait_until(f"{count} spans of {session_id}", probe)
        assert len(spans) == count, [span.name for span in spans]
        return spans

    async def executed_steps(self, session_id: str) -> set[tuple[str, str]]:
        history = await self.stack.read_step_history(session_id)
        return {
            (entry.step_type, entry.step_execution_id)
            for entry in history
            if entry.event_type == EXECUTE_COMPLETED_EVENT
        }


@pytest_asyncio.fixture
async def tracing(tmp_path: Path) -> AsyncIterator[TracingHarness]:
    """Serves scripted agents from an in-process Worker that exports to memory."""
    _install_global_tracer_provider()
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    exporter = InMemorySpanExporter()
    open_spans = OpenSpanCounter()
    provider = TracerProvider()
    provider.add_span_processor(open_spans)
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    faulty_provider = TracerProvider()
    faulty_provider.add_span_processor(RaisingSpanProcessor())
    sampled_exporter = InMemorySpanExporter()
    sampled_provider = TracerProvider(sampler=ParentBased(AlternatingRootSampler()))
    sampled_provider.add_span_processor(SimpleSpanProcessor(sampled_exporter))

    def build_flow(
        flow_type: str,
        tracing: AgentTracingOptions,
        usage: AgentUsage = TRACED_USAGE,
    ) -> AgentSessionFlow:
        return AgentSessionFlow(
            ScriptedRunnerWithModelMetadata(state_dir, usage),
            flow_type=flow_type,
            options=AgentSessionOptions(
                tool_policies={
                    **TOOL_POLICIES,
                    "charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=True),
                },
                tracing=tracing,
            ),
        )

    flows = [
        build_flow("TracedAgent", AgentTracingOptions(tracer_provider=provider)),
        build_flow(
            "ContentTracedAgent",
            AgentTracingOptions(tracer_provider=provider, should_capture_message_content=True),
        ),
        build_flow(
            "LegacyTracedAgent",
            AgentTracingOptions(
                tracer_provider=provider,
                agent_name="Legacy agent",
                should_capture_message_content=True,
                semantic_conventions_version=GenAISemanticConventionsVersion.V1_36_0,
            ),
        ),
        build_flow(
            "UntracedAgent", AgentTracingOptions(tracer_provider=provider, is_enabled=False)
        ),
        build_flow("GlobalTracedAgent", AgentTracingOptions()),
        build_flow(
            "FaultyProcessorAgent",
            AgentTracingOptions(
                tracer_provider=faulty_provider, should_capture_message_content=True
            ),
        ),
        build_flow(
            "ZeroUsageAgent",
            AgentTracingOptions(tracer_provider=provider),
            AgentUsage(model_calls=1),
        ),
        build_flow(
            "HarnessAgent",
            AgentTracingOptions(tracer_provider=provider),
            AgentUsage(model_calls=2, input_tokens=30, output_tokens=12),
        ),
        build_flow("SampledAgent", AgentTracingOptions(tracer_provider=sampled_provider)),
    ]
    async with AgentTestStack(tmp_path, flows, "tests.scripted_worker") as stack:
        async with _in_process_worker(stack, flows, tmp_path / "worker-blob-cache"):
            yield TracingHarness(stack, exporter, sampled_exporter, open_spans, state_dir, *flows)
    provider.shutdown()


async def test_a_turn_with_a_tool_call_and_an_approval_wait_emits_one_trace_per_activation(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.traced_flow)
    session_id = _new_session_id()
    await sessions.start_session(
        session_id, 'call lookup {"query": "docs"} && call publish {"message": "x"}'
    )
    waiting = await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if [(call.tool_name, call.status) for call in snapshot.tool_calls]
        == [("lookup", ToolCallStatus.SUCCEEDED), ("publish", ToolCallStatus.AWAITING_APPROVAL)]
        else None,
        description="the read finished while the write awaits approval",
    )

    # The session parks on the approval: the first activation's trace is complete.
    first_activation = await tracing.wait_for_spans(session_id, 3)
    assert tracing.open_spans.open_span_count == 0
    first_root, first_chat, lookup = _by_name(
        first_activation, "invoke_agent TracedAgent", "chat scripted-model", "execute_tool lookup"
    )
    _assert_activation_root(first_root, previous_root=None)
    _assert_children_of(first_root, [first_chat, lookup])
    assert _attributes(first_root) | {
        "dex.agent.activation.trigger": "user_message",
        "dex.agent.user_message.key": "000000000001",
        "dex.step.type": "StartAgentSession",
    } == _attributes(first_root)
    values = await tracing.stack.client.get_attributes(session_id, AGENT_TRACE_CONTEXT)
    assert values[AGENT_TRACE_CONTEXT] == _traceparent(first_root)

    assert first_chat.kind is SpanKind.CLIENT
    assert _attributes(first_chat) | {
        "gen_ai.operation.name": "chat",
        "gen_ai.provider.name": "scripted",
        "gen_ai.request.model": "scripted-model",
        "gen_ai.response.model": "scripted-model-2026-11",
        "gen_ai.response.id": "turn-1-attempt-1",
        "gen_ai.response.finish_reasons": ("stop",),
        "gen_ai.usage.input_tokens": 10,
        "gen_ai.usage.output_tokens": 5,
        "gen_ai.usage.cache_read.input_tokens": 4,
        "gen_ai.usage.reasoning.output_tokens": 2,
        "dex.step.type": "RunModel",
        "dex.agent.input_messages.last_key": "000000000001",
        "dex.agent.requested_tool_call.ids": ("turn-1-call-0", "turn-1-call-1"),
    } == _attributes(first_chat)
    assert lookup.kind is SpanKind.INTERNAL
    assert _attributes(lookup) | {
        "gen_ai.operation.name": "execute_tool",
        "gen_ai.agent.name": "TracedAgent",
        "gen_ai.tool.name": "lookup",
        "gen_ai.tool.call.id": "turn-1-call-0",
        "dex.step.type": "CallTool",
        "dex.agent.tool.effect": "read",
        "dex.agent.tool_call.status": "succeeded",
    } == _attributes(lookup)

    assert await sessions.resolve_interrupt(
        session_id, waiting.pending_interrupts[0].interrupt_id, approved=True
    )
    await _wait_for_turns(sessions, session_id, 1)
    second_activation = [
        span for span in await tracing.wait_for_spans(session_id, 6) if span not in first_activation
    ]
    second_root, publish, second_chat = _by_name(
        second_activation, "invoke_agent TracedAgent", "execute_tool publish", "chat scripted-model"
    )
    _assert_activation_root(second_root, previous_root=first_root)
    _assert_children_of(second_root, [publish, second_chat])
    assert _attributes(second_root) | {
        "dex.agent.activation.trigger": "interrupt_resolution",
        "dex.agent.interrupt.id": "turn-1-call-1",
        "dex.agent.interrupt.approved": True,
        "dex.step.type": "AwaitToolApproval",
    } == _attributes(second_root)
    assert _attributes(publish) | {
        "dex.step.type": "CallSingleAttemptTool",
        "dex.agent.tool.effect": "non_idempotent_write",
        "dex.agent.tool_call.status": "succeeded",
    } == _attributes(publish)
    # The final model call produced only the answer; the earlier messages came from the tools.
    assert _attributes(second_chat)["dex.agent.output_messages.keys"] == ("000000000006",)

    # The next turn is a third activation, linked to the second.
    assert await sessions.send_user_message(session_id, "say done")
    await _wait_for_turns(sessions, session_id, 2)
    third_activation = [
        span
        for span in await tracing.wait_for_spans(session_id, 8)
        if span not in first_activation and span not in second_activation
    ]
    third_root, third_chat = _by_name(
        third_activation, "invoke_agent TracedAgent", "chat scripted-model"
    )
    _assert_activation_root(third_root, previous_root=second_root)
    _assert_children_of(third_root, [third_chat])
    assert _attributes(third_root)["dex.step.type"] == "AwaitUserMessage"
    assert tracing.open_spans.open_span_count == 0

    spans = tracing.spans(session_id)
    assert len({span.context.trace_id for span in spans}) == 3
    executed_steps = await tracing.executed_steps(session_id)
    for span in spans:
        attributes = _attributes(span)
        assert attributes["session.id"] == session_id
        assert attributes["dex.flow.id"] == session_id
        assert attributes["dex.run.id"]
        assert attributes["dex.step.attempt"] == 1
        assert (attributes["dex.step.type"], attributes["dex.step.execution_id"]) in executed_steps
        assert span.instrumentation_scope is not None
        assert span.instrumentation_scope.name == "dex_agents"
        assert span.instrumentation_scope.schema_url == LATEST_SCHEMA_URL
        assert not any(name.startswith("gen_ai.input") for name in attributes)
        assert not any(name.startswith("gen_ai.output.messages") for name in attributes)
        assert "gen_ai.tool.call.arguments" not in attributes


async def test_retries_emit_one_span_per_attempt(tracing: TracingHarness) -> None:
    sessions = tracing.sessions(tracing.traced_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id, 'call lookup {"query": "a", "fail_attempts": 2}')
    await _wait_for_turns(sessions, session_id, 1)
    spans = await tracing.wait_for_spans(session_id, 6)
    root = _by_name(spans, "invoke_agent TracedAgent")[0]
    lookups = sorted(
        (span for span in spans if span.name == "execute_tool lookup"),
        key=lambda span: _attributes(span)["dex.step.attempt"],
    )
    assert [_attributes(span)["dex.step.attempt"] for span in lookups] == [1, 2, 3]
    assert len({_attributes(span)["dex.step.execution_id"] for span in lookups}) == 1
    assert [span.status.status_code for span in lookups] == [
        StatusCode.ERROR,
        StatusCode.ERROR,
        StatusCode.UNSET,
    ]
    assert [_attributes(span).get("error.type") for span in lookups] == [
        "RuntimeError",
        "RuntimeError",
        None,
    ]
    _assert_children_of(root, lookups)

    assert await sessions.send_user_message(session_id, "fail")
    await _wait_for_turns(sessions, session_id, 2)
    failed_turn = [
        span for span in await tracing.wait_for_spans(session_id, 10) if span not in spans
    ]
    failed_root = _by_name(failed_turn, "invoke_agent TracedAgent")[0]
    model_calls = sorted(
        (span for span in failed_turn if span.name == "chat"),
        key=lambda span: _attributes(span)["dex.step.attempt"],
    )
    assert [_attributes(span)["dex.step.attempt"] for span in model_calls] == [1, 2, 3]
    assert {span.status.status_code for span in model_calls} == {StatusCode.ERROR}
    assert {_attributes(span)["error.type"] for span in model_calls} == {"RuntimeError"}
    assert len({_attributes(span)["dex.step.execution_id"] for span in model_calls}) == 1
    _assert_children_of(failed_root, model_calls)
    assert tracing.open_spans.open_span_count == 0


async def test_message_content_is_captured_only_when_enabled(tracing: TracingHarness) -> None:
    sessions = tracing.sessions(tracing.content_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    await _wait_for_turns(sessions, session_id, 1)
    root, first_chat, lookup, second_chat = _by_name(
        await tracing.wait_for_spans(session_id, 4),
        "invoke_agent ContentTracedAgent",
        "chat scripted-model",
        "execute_tool lookup",
        "chat scripted-model",
    )
    user_message = {
        "role": "user",
        "parts": [{"type": "text", "content": 'call lookup {"query": "docs"}'}],
    }
    tool_call = {
        "type": "tool_call",
        "id": "turn-1-call-0",
        "name": "lookup",
        "arguments": {"query": "docs"},
    }
    assert _json_attribute(root, "gen_ai.input.messages") == [user_message]
    assert _json_attribute(first_chat, "gen_ai.input.messages") == [user_message]
    assert _json_attribute(first_chat, "gen_ai.output.messages") == [
        {"role": "assistant", "parts": [tool_call]}
    ]
    assert _attributes(lookup)["gen_ai.tool.call.arguments"] == '{"query": "docs"}'
    assert _attributes(lookup)["gen_ai.tool.call.result"] == "found docs"
    assert _json_attribute(second_chat, "gen_ai.input.messages") == [
        user_message,
        {"role": "assistant", "parts": [tool_call]},
        {
            "role": "tool",
            "parts": [
                {"type": "tool_call_response", "id": "turn-1-call-0", "response": "found docs"}
            ],
        },
    ]
    assert _json_attribute(second_chat, "gen_ai.output.messages") == [
        {"role": "assistant", "parts": [{"type": "text", "content": "lookup: found docs"}]}
    ]


async def test_semantic_conventions_1_36_use_gen_ai_system_and_capture_no_content(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.legacy_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    await _wait_for_turns(sessions, session_id, 1)
    spans = await tracing.wait_for_spans(session_id, 4)
    chat = _by_name(spans, "chat scripted-model")[0]
    assert _attributes(chat) | {
        "gen_ai.system": "scripted",
        "gen_ai.usage.input_tokens": 10,
        "gen_ai.usage.output_tokens": 5,
    } == _attributes(chat)
    assert "gen_ai.provider.name" not in _attributes(chat)
    assert "gen_ai.usage.cache_read.input_tokens" not in _attributes(chat)
    assert "gen_ai.usage.reasoning.output_tokens" not in _attributes(chat)
    assert _by_name(spans, "invoke_agent Legacy agent", "execute_tool lookup")
    for span in spans:
        assert span.instrumentation_scope is not None
        assert span.instrumentation_scope.schema_url == "https://opentelemetry.io/schemas/1.36.0"
        assert not any(".messages" in name or ".call.result" in name for name in _attributes(span))


async def test_disabled_tracing_emits_no_spans_and_stores_no_trace_context(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.untraced_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    await _wait_for_turns(sessions, session_id, 1)
    assert tracing.spans(session_id) == []
    values = await tracing.stack.client.get_attributes(session_id, AGENT_TRACE_CONTEXT)
    assert AGENT_TRACE_CONTEXT not in values


async def test_the_global_tracer_provider_receives_spans_by_default(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.global_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id, "say hi")
    await _wait_for_turns(sessions, session_id, 1)
    root, chat = _by_name(
        await tracing.wait_for_spans(session_id, 2),
        "invoke_agent GlobalTracedAgent",
        "chat scripted-model",
    )
    global_spans = GLOBAL_EXPORTER.get_finished_spans()
    assert root in global_spans
    assert chat in global_spans
    _assert_children_of(root, [chat])


async def test_a_raising_span_processor_never_fails_or_repeats_a_step(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.faulty_processor_flow)
    session_id = _new_session_id()
    await sessions.start_session(
        session_id, 'call lookup {"query": "a"} && call slow_write {"message": "m"}'
    )
    snapshot = await _wait_for_turns(sessions, session_id, 1)
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "lookup: found a; slow_write: wrote m"
    invocations = InvocationLog(tracing.state_dir / INVOCATION_LOG_NAME)
    for name in ("model:turn-1:1", "lookup", "slow_write", "model:turn-1:2"):
        assert invocations.count(name) == 1, name
    executed = [step_type for step_type, _ in await tracing.executed_steps(session_id)]
    assert "RecordToolOutcomeUnknown" not in executed
    assert "RecordTurnFailure" not in executed


async def test_deeply_nested_tool_arguments_never_fail_a_traced_model_call(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.content_flow)
    session_id = _new_session_id()
    # Deep enough that json.loads raises RecursionError while the session builds span content.
    await sessions.start_session(session_id, "call lookup " + "[" * 100_000 + "]" * 100_000)
    snapshot = await _wait_for_turns(sessions, session_id, 1)
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert (snapshot.messages[-1].text or "").startswith("lookup: The tool call failed")
    spans = tracing.spans(session_id)
    model_calls = [span for span in spans if span.name == "chat scripted-model"]
    assert [span.status.status_code for span in model_calls] == [StatusCode.UNSET] * 2


async def test_a_model_call_that_reports_no_tokens_sets_no_usage(tracing: TracingHarness) -> None:
    sessions = tracing.sessions(tracing.zero_usage_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id, "say hi")
    await _wait_for_turns(sessions, session_id, 1)
    (chat,) = _by_name(await tracing.wait_for_spans(session_id, 2), "chat scripted-model")
    assert chat.kind is SpanKind.CLIENT
    assert not [name for name in _attributes(chat) if name.startswith("gen_ai.usage.")]


async def test_a_run_model_with_several_model_calls_is_internal_and_reports_no_usage(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.harness_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id, "say hi")
    await _wait_for_turns(sessions, session_id, 1)
    (chat,) = _by_name(await tracing.wait_for_spans(session_id, 2), "chat scripted-model")
    assert chat.kind is SpanKind.INTERNAL
    assert _attributes(chat)["gen_ai.request.model"] == "scripted-model"
    assert not [name for name in _attributes(chat) if name.startswith("gen_ai.usage.")]


async def test_spans_follow_the_sampling_decision_of_their_activation_root(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.sampled_flow)
    session_id = _new_session_id()
    await sessions.start_session(session_id)
    for turn in range(1, 7):
        assert await sessions.send_user_message(session_id, f'call lookup {{"query": "q{turn}"}}')
        await _wait_for_turns(sessions, session_id, turn)
    # The sixth root was sampled out, yet its context is stored, unsampled, for its children.
    values = await tracing.stack.client.get_attributes(session_id, AGENT_TRACE_CONTEXT)
    trace_context = values[AGENT_TRACE_CONTEXT]
    assert isinstance(trace_context, str)
    match = re.fullmatch(r"00-[0-9a-f]{32}-[0-9a-f]{16}-([0-9a-f]{2})", trace_context)
    assert match is not None, trace_context
    assert int(match.group(1), 16) & 0x01 == 0

    spans = tracing.sampled_exporter.get_finished_spans()
    roots = [span for span in spans if span.name == "invoke_agent SampledAgent"]
    children = [span for span in spans if span not in roots]
    assert [_attributes(root)["dex.agent.turn.id"] for root in roots] == [
        "turn-1",
        "turn-3",
        "turn-5",
    ]
    # ParentBased sampling drops every child of a dropped root: no child is a trace of its own.
    assert [span for span in children if span.parent is None] == []
    assert {span.context.trace_id for span in children} == {root.context.trace_id for root in roots}
    assert (
        sorted(span.name for span in children)
        == ["chat scripted-model"] * 6 + ["execute_tool lookup"] * 3
    )


async def test_two_approvals_in_one_turn_keep_each_tool_span_in_its_activation(
    tracing: TracingHarness,
) -> None:
    sessions = tracing.sessions(tracing.traced_flow)
    session_id = _new_session_id()
    await sessions.start_session(
        session_id,
        'call charge {"amount": 2, "gate": "charge-gate", "fail_attempts": 1}'
        ' && call publish {"message": "x"}',
    )
    await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot if len(snapshot.pending_interrupts) == 2 else None,
        description="both writes await approval",
    )
    charge_gate = FileGate(tracing.state_dir, "charge-gate")
    assert await sessions.resolve_interrupt(session_id, "turn-1-call-0", approved=True)
    await charge_gate.wait_until_reached()
    # The second answer starts a newer activation while the charge's first attempt still runs.
    assert await sessions.resolve_interrupt(session_id, "turn-1-call-1", approved=True)
    await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if any(
            call.tool_name == "publish" and call.status is ToolCallStatus.SUCCEEDED
            for call in snapshot.tool_calls
        )
        else None,
        description="the publish call succeeded",
    )
    charge_gate.release()
    await _wait_for_turns(sessions, session_id, 1)

    spans = await tracing.wait_for_spans(session_id, 8)
    roots = {
        _attributes(span).get("dex.agent.interrupt.id"): span
        for span in spans
        if span.name == "invoke_agent TracedAgent"
    }
    first_root, charge_root, publish_root = (
        roots[None],
        roots["turn-1-call-0"],
        roots["turn-1-call-1"],
    )
    _assert_activation_root(charge_root, previous_root=first_root)
    _assert_activation_root(publish_root, previous_root=charge_root)
    charges = sorted(
        (span for span in spans if span.name == "execute_tool charge"),
        key=lambda span: _attributes(span)["dex.step.attempt"],
    )
    assert [span.status.status_code for span in charges] == [StatusCode.ERROR, StatusCode.UNSET]
    # The retry started after the newer answer, yet stays in the charge approval's trace.
    _assert_children_of(charge_root, charges)
    _assert_children_of(publish_root, _by_name(spans, "execute_tool publish"))
    first_chat, final_chat = _by_name(spans, "chat scripted-model", "chat scripted-model")
    _assert_children_of(first_root, [first_chat])
    _assert_children_of(publish_root, [final_chat])
    assert len({span.context.trace_id for span in spans}) == 3


async def test_a_root_step_on_a_worker_without_an_sdk_clears_the_stored_trace_context(
    tmp_path: Path,
) -> None:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    flow = AgentSessionFlow(
        ScriptedRunner(state_dir),
        flow_type=SCRIPTED_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(
            tool_policies=TOOL_POLICIES, tracing=AgentTracingOptions(tracer_provider=provider)
        ),
    )
    async with AgentTestStack(
        tmp_path,
        [flow],
        "tests.scripted_worker",
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        sessions = stack.sessions(flow)
        session_id = _new_session_id()
        async with _in_process_worker(stack, [flow], tmp_path / "traced-worker-1"):
            await sessions.start_session(session_id, "say one")
            await _wait_for_turns(sessions, session_id, 1)
        (first_root,) = _by_name(exporter.get_finished_spans(), "invoke_agent ScriptedAgent")

        # The Worker child has the OpenTelemetry API but no SDK, so its root span does not record.
        untraced_worker = await stack.start_worker()
        assert await sessions.send_user_message(session_id, "stall model-gate")
        model_gate = FileGate(state_dir, "model-gate")
        await model_gate.wait_until_reached()
        untraced_worker.kill()
        model_gate.release()
        async with _in_process_worker(stack, [flow], tmp_path / "traced-worker-2"):
            await _wait_for_turns(sessions, session_id, 2)
        values = await stack.client.get_attributes(session_id, AGENT_TRACE_CONTEXT)
    assert AGENT_TRACE_CONTEXT not in values
    (second_turn_chat,) = [
        span
        for span in exporter.get_finished_spans()
        if span.name == "chat" and _attributes(span)["dex.agent.turn.id"] == "turn-2"
    ]
    assert second_turn_chat.parent is None
    assert second_turn_chat.context.trace_id != first_root.context.trace_id
    provider.shutdown()


async def test_a_worker_without_opentelemetry_runs_sessions_and_never_loads_it(
    tmp_path: Path,
) -> None:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    blocked_import_log_path = state_dir / "blocked-imports.log"
    flow = build_scripted_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        "tests.worker_without_opentelemetry",
        worker_cwd=CORE_DIR,
        worker_env={
            STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir),
            BLOCKED_IMPORT_LOG_ENVIRONMENT_VARIABLE: str(blocked_import_log_path),
        },
    ) as stack:
        await stack.start_worker()
        sessions = stack.sessions(flow)
        session_id = _new_session_id()
        await sessions.start_session(session_id, 'call lookup {"query": "docs"}')
        snapshot = await _wait_for_turns(sessions, session_id, 1)
        assert snapshot.messages[-1].text == "lookup: found docs"
        assert await sessions.send_user_message(session_id, "say again")
        await _wait_for_turns(sessions, session_id, 2)
        values = await stack.client.get_attributes(session_id, AGENT_TRACE_CONTEXT)
        assert AGENT_TRACE_CONTEXT not in values
    # The session tried the API once, at its first traced Step, and never again.
    assert InvocationLog(blocked_import_log_path).names() == ["opentelemetry"]


@contextlib.asynccontextmanager
async def _in_process_worker(
    stack: AgentTestStack,
    flows: Sequence[AgentSessionFlow],
    blob_cache_dir: Path,
) -> AsyncIterator[None]:
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


async def _wait_for_turns(sessions: AgentSessionClient, session_id: str, turn_count: int) -> Any:
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


def _install_global_tracer_provider() -> None:
    if isinstance(trace.get_tracer_provider(), TracerProvider):
        return
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(GLOBAL_EXPORTER))
    trace.set_tracer_provider(provider)


def _new_session_id() -> str:
    return f"session-{uuid4().hex}"


def _attributes(span: ReadableSpan) -> dict[str, Any]:
    return dict(span.attributes or {})


def _json_attribute(span: ReadableSpan, name: str) -> Any:
    return json.loads(str(_attributes(span)[name]))


def _by_name(spans: Sequence[ReadableSpan], *names: str) -> list[ReadableSpan]:
    """Returns one span per name, in start order, consuming each match once."""
    remaining = sorted(spans, key=lambda span: span.start_time or 0)
    matches: list[ReadableSpan] = []
    for name in names:
        match = next((span for span in remaining if span.name == name), None)
        assert match is not None, f"no span {name!r} in {[span.name for span in remaining]}"
        remaining.remove(match)
        matches.append(match)
    return matches


def _assert_activation_root(root: ReadableSpan, previous_root: ReadableSpan | None) -> None:
    assert root.parent is None
    assert root.kind is SpanKind.INTERNAL
    assert _attributes(root)["gen_ai.operation.name"] == "invoke_agent"
    if previous_root is None:
        assert list(root.links) == []
        return
    assert [(link.context.trace_id, link.context.span_id) for link in root.links] == [
        (previous_root.context.trace_id, previous_root.context.span_id)
    ]
    assert root.context.trace_id != previous_root.context.trace_id


def _assert_children_of(root: ReadableSpan, children: Sequence[ReadableSpan]) -> None:
    for child in children:
        assert child.context.trace_id == root.context.trace_id, child.name
        assert child.parent is not None, child.name
        assert child.parent.span_id == root.context.span_id, child.name


def _traceparent(span: ReadableSpan) -> str:
    return (
        f"00-{span.context.trace_id:032x}-{span.context.span_id:016x}-"
        f"{int(span.context.trace_flags):02x}"
    )
