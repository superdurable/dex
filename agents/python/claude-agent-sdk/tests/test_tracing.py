# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ``chat`` span of every turn segment carries the model metadata the CLI reports."""

from __future__ import annotations

import asyncio
import contextlib
import shlex
import sys
from collections.abc import AsyncIterator, Mapping
from dataclasses import dataclass, replace
from datetime import timedelta
from pathlib import Path
from typing import Any

import pytest
from claude_agent_sdk import ClaudeAgentOptions
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

from dex_agents import AgentSessionFlow, AgentTracingOptions, ToolEffect, ToolPolicy
from dex_agents.testing import AgentTestStack
from dex_agents_claude_agent_sdk import claude_agent_session_flow
from dex_agents_claude_agent_sdk.runner import _gen_ai_provider_name_of_claude_provider
from tests.claude_test_agent import (
    FAKE_CLI_PATH_ENVIRONMENT_VARIABLE,
    build_claude_test_agent_flow,
)
from tests.conftest import FAKE_CLI_SCRIPT, PACKAGE_DIR, WORKER_MODULE, ClaudeAgentHarness
from tests.fake_claude_cli import MODEL_NAME, RESPONSE_MODEL_NAME, STATE_DIR_ENVIRONMENT_VARIABLE

# Every ModelUsage.provider value the SDK types list; a gateway relays to an unnamed backend.
CLAUDE_PROVIDER_CASES = [
    ("firstParty", "anthropic"),
    ("bedrock", "aws.bedrock"),
    ("mantle", "aws.bedrock"),
    ("vertex", "gcp.vertex_ai"),
    ("foundry", "foundry"),
    ("anthropicAws", "anthropicAws"),
    ("anthropicGoogleCloud", "anthropicGoogleCloud"),
    ("gateway", None),
    ("", None),
]


@dataclass(frozen=True)
class TracedClaudeAgent:
    harness: ClaudeAgentHarness
    exporter: InMemorySpanExporter

    def chat_spans(self) -> list[ReadableSpan]:
        return sorted(
            (span for span in self.exporter.get_finished_spans() if span.name.startswith("chat")),
            key=lambda span: span.start_time or 0,
        )


async def test_chat_spans_name_the_cli_model_and_drop_per_response_fields_for_several_calls(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    async with _traced_claude_agent(tmp_path, monkeypatch) as agent:
        harness = agent.harness
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, "say hi")
        await harness.wait_for_turns(session_id, 1)
        assert await harness.sessions.send_user_message(session_id, 'call lookup {"query": "docs"}')
        await harness.wait_for_turns(session_id, 2)
        assert await harness.sessions.send_user_message(session_id, 'call charge {"amount": 5}')
        snapshot = await harness.wait_for_turns(session_id, 3)
    assert snapshot.messages[-1].text is not None
    assert snapshot.messages[-1].text.startswith("charge: charged 5")

    chat_spans = agent.chat_spans()
    span_shapes = [
        (span.name, span.kind, _attributes(span)["dex.agent.turn.id"]) for span in chat_spans
    ]
    assert span_shapes == [
        (f"chat {MODEL_NAME}", SpanKind.CLIENT, "turn-1"),
        (f"chat {MODEL_NAME}", SpanKind.INTERNAL, "turn-2"),
        (f"chat {MODEL_NAME}", SpanKind.CLIENT, "turn-3"),
        (f"chat {MODEL_NAME}", SpanKind.CLIENT, "turn-3"),
    ]
    for span in chat_spans:
        attributes = _attributes(span)
        assert attributes["gen_ai.request.model"] == MODEL_NAME
        assert attributes["gen_ai.response.model"] == RESPONSE_MODEL_NAME
        assert attributes["gen_ai.provider.name"] == "anthropic"
    answer, read_tool_turn, deferral, resumed_answer = (_attributes(span) for span in chat_spans)
    assert answer["gen_ai.response.finish_reasons"] == ("end_turn",)
    assert deferral["gen_ai.response.finish_reasons"] == ("tool_use",)
    assert resumed_answer["gen_ai.response.finish_reasons"] == ("end_turn",)
    response_ids = [
        attributes["gen_ai.response.id"] for attributes in (answer, deferral, resumed_answer)
    ]
    assert all(str(response_id).startswith("msg_") for response_id in response_ids)
    assert len(set(response_ids)) == 3
    assert answer["gen_ai.usage.output_tokens"] == 5
    # Two model calls: no single response ID, finish reason, or usage describes the segment.
    assert "gen_ai.response.id" not in read_tool_turn
    assert "gen_ai.response.finish_reasons" not in read_tool_turn
    assert not [name for name in read_tool_turn if name.startswith("gen_ai.usage.")]


async def test_a_segment_with_a_subagent_model_call_is_internal_and_names_no_model(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # The subagent runs inline, then charge defers: one main call and one subagent call.
    async with _traced_claude_agent(
        tmp_path, monkeypatch, {"Agent": ToolPolicy(ToolEffect.READ)}
    ) as agent:
        harness = agent.harness
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id, 'call Agent {"prompt": "the docs"} && call charge {"amount": 5}'
        )
        snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text is not None
    assert "charge: charged 5" in snapshot.messages[-1].text
    assert snapshot.usage.model_calls == 3

    chat_spans = agent.chat_spans()
    assert [(span.name, span.kind) for span in chat_spans] == [
        ("chat", SpanKind.INTERNAL),
        (f"chat {MODEL_NAME}", SpanKind.CLIENT),
    ]
    subagent_segment = _attributes(chat_spans[0])
    assert not [
        name
        for name in subagent_segment
        if name.startswith("gen_ai.usage.")
        or name
        in (
            "gen_ai.request.model",
            "gen_ai.response.model",
            "gen_ai.provider.name",
            "gen_ai.response.id",
            "gen_ai.response.finish_reasons",
        )
    ]


@pytest.mark.parametrize(("claude_provider", "gen_ai_provider_name"), CLAUDE_PROVIDER_CASES)
def test_claude_providers_map_to_well_known_gen_ai_provider_names(
    claude_provider: str, gen_ai_provider_name: str | None
) -> None:
    assert _gen_ai_provider_name_of_claude_provider(claude_provider) == gen_ai_provider_name


def test_the_session_flow_helper_passes_its_tracing_options_to_the_flow() -> None:
    tracing = AgentTracingOptions(agent_name="Support agent", is_enabled=False)

    def build_options(context: object) -> ClaudeAgentOptions:
        return ClaudeAgentOptions()

    traced_flow = claude_agent_session_flow(
        build_options, flow_type="TracedHelperAgent", tracing=tracing
    )
    default_flow = claude_agent_session_flow(build_options, flow_type="DefaultHelperAgent")

    assert traced_flow.options.tracing is tracing
    assert default_flow.options.tracing == AgentTracingOptions()


@contextlib.asynccontextmanager
async def _traced_claude_agent(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    extra_tool_policies: Mapping[str, ToolPolicy] | None = None,
) -> AsyncIterator[TracedClaudeAgent]:
    state_dir = tmp_path / "agent-state"
    (state_dir / "workspace").mkdir(parents=True)
    (state_dir / "claude-config").mkdir()
    fake_cli_path = tmp_path / "fake-claude"
    fake_cli_path.write_text(
        "#!/bin/sh\n"
        f'exec {shlex.quote(sys.executable)} {shlex.quote(str(FAKE_CLI_SCRIPT))} "$@"\n'
    )
    fake_cli_path.chmod(0o755)
    # The in-process Worker builds CLI options from this process's environment.
    monkeypatch.setenv(STATE_DIR_ENVIRONMENT_VARIABLE, str(state_dir))
    monkeypatch.setenv(FAKE_CLI_PATH_ENVIRONMENT_VARIABLE, str(fake_cli_path))
    exporter = InMemorySpanExporter()
    tracer_provider = TracerProvider()
    tracer_provider.add_span_processor(SimpleSpanProcessor(exporter))
    untraced_flow = build_claude_test_agent_flow(state_dir)
    flow = AgentSessionFlow(
        untraced_flow.runner,
        flow_type="TracedClaudeAgent",
        options=replace(
            untraced_flow.options,
            tool_policies={**untraced_flow.options.tool_policies, **(extra_tool_policies or {})},
            tracing=AgentTracingOptions(tracer_provider=tracer_provider),
        ),
    )
    async with AgentTestStack(tmp_path, [flow], WORKER_MODULE, worker_cwd=PACKAGE_DIR) as stack:
        harness = ClaudeAgentHarness(stack, flow, stack.sessions(flow), state_dir)
        async with _in_process_worker(stack, flow, tmp_path / "worker-blob-cache"):
            yield TracedClaudeAgent(harness, exporter)
    tracer_provider.shutdown()


@contextlib.asynccontextmanager
async def _in_process_worker(
    stack: AgentTestStack, flow: AgentSessionFlow, blob_cache_dir: Path
) -> AsyncIterator[None]:
    blob_cache = open_blob_cache(BlobCacheConfig(str(blob_cache_dir), 64 * 1024 * 1024))
    worker = AsyncWorker(
        Registry([flow], allow_async_handlers=True),
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


def _attributes(span: ReadableSpan) -> dict[str, Any]:
    return dict(span.attributes or {})
