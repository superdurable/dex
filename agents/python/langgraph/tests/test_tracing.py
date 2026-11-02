# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The model and response that the LangChain chat model reports name each ``chat`` span."""

from __future__ import annotations

import asyncio
import contextlib
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest
from dex import (
    AsyncWorker,
    BlobCacheConfig,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from dex_agents import AgentSessionFlow, AgentSessionOptions, AgentTracingOptions
from dex_agents.testing import AgentTestStack, wait_for_agent_snapshot, wait_until
from opentelemetry.sdk.trace import ReadableSpan, TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.trace import SpanKind

from dex_agents_langgraph import (
    LangGraphAgentRunner,
    langchain_agent_session_flow,
    langgraph_agent_session_flow,
)
from dex_agents_langgraph.runner import _gen_ai_provider_name
from tests.conftest import PACKAGE_DIR, WORKER_MODULE
from tests.scripted_agents import (
    STATE_DIR_ENVIRONMENT_VARIABLE,
    TOOL_POLICIES,
    build_create_agent,
    build_state_graph,
)
from tests.scripted_model import (
    SCRIPTED_MODEL_PROVIDER,
    SCRIPTED_REQUEST_MODEL,
    SCRIPTED_RESPONSE_MODEL,
)


@pytest.mark.parametrize(
    ("model_provider", "gen_ai_provider_name"),
    [
        ("openai", "openai"),
        ("anthropic", "anthropic"),
        ("google_genai", "gcp.gen_ai"),
        ("google_vertexai", "gcp.vertex_ai"),
        ("google_anthropic_vertex", "gcp.vertex_ai"),
        ("bedrock", "aws.bedrock"),
        ("bedrock_converse", "aws.bedrock"),
        ("anthropic_bedrock", "aws.bedrock"),
        ("bedrock_mantle_anthropic", "aws.bedrock"),
        ("bedrock_mantle_openai", "aws.bedrock"),
        ("azure", "azure.ai.openai"),
        ("azure_openai", "azure.ai.openai"),
        ("xai", "x_ai"),
        ("mistralai", "mistral"),
        ("ibm", "ibm.watsonx.ai"),
        ("cohere", "cohere"),
        ("deepseek", "deepseek"),
        ("groq", "groq"),
        ("perplexity", "perplexity"),
        ("ollama", "ollama"),
        ("azure_ai", "azure_ai"),
        ("scripted", "scripted"),
        (None, None),
    ],
)
def test_model_providers_map_to_well_known_gen_ai_provider_names(
    model_provider: str | None, gen_ai_provider_name: str | None
) -> None:
    assert _gen_ai_provider_name(model_provider) == gen_ai_provider_name


def test_session_flow_helpers_pass_tracing_options_to_the_session(tmp_path: Path) -> None:
    tracing = AgentTracingOptions(agent_name="Support", is_enabled=False)

    agent_flow = langchain_agent_session_flow(
        build_create_agent(tmp_path), flow_type="TracingOptionsAgent", tracing=tracing
    )
    graph_flow = langgraph_agent_session_flow(
        build_state_graph(tmp_path), flow_type="TracingOptionsGraph", tracing=tracing
    )

    assert agent_flow.options.tracing is tracing
    assert graph_flow.options.tracing is tracing


async def test_each_chat_span_is_named_after_the_model_the_chat_model_reports(
    tmp_path: Path,
) -> None:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    flow = AgentSessionFlow(
        LangGraphAgentRunner(build_create_agent(state_dir)),
        flow_type="TracedLangChainAgent",
        options=AgentSessionOptions(
            tool_policies=TOOL_POLICIES,
            tracing=AgentTracingOptions(tracer_provider=provider),
        ),
    )
    session_id = f"session-{uuid4().hex}"
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        blob_cache = open_blob_cache(
            BlobCacheConfig(str(tmp_path / "worker-blob-cache"), 64 * 1024 * 1024)
        )
        worker = AsyncWorker(
            Registry([flow], allow_async_handlers=True),
            blob_cache,
            WorkerOptions(
                server_address=stack.engine.address,
                pull=PullWorkerOptions(
                    max_concurrent_tasks=8, stop_drain_timeout=timedelta(seconds=5)
                ),
            ),
        )
        serving = asyncio.create_task(worker.start())
        try:
            await worker.wait_pull_registered(timeout=30)
            sessions = stack.sessions(flow)
            await sessions.start_session(session_id, 'call lookup {"query": "docs"}')
            await wait_for_agent_snapshot(
                sessions,
                session_id,
                lambda snapshot: snapshot
                if snapshot.state is not None and snapshot.state.turn_count == 1
                else None,
                description="the finished turn",
            )

            async def probe() -> list[ReadableSpan] | None:
                chat_spans = [
                    span
                    for span in exporter.get_finished_spans()
                    if dict(span.attributes or {}).get("gen_ai.operation.name") == "chat"
                ]
                return chat_spans if len(chat_spans) == 2 else None

            chat_spans = sorted(
                await wait_until("both chat spans", probe), key=lambda span: span.start_time or 0
            )
        finally:
            await worker.stop()
            with contextlib.suppress(asyncio.CancelledError):
                await serving
            blob_cache.close()
    provider.shutdown()

    attributes: list[dict[str, Any]] = [dict(span.attributes or {}) for span in chat_spans]
    assert [span.name for span in chat_spans] == [f"chat {SCRIPTED_REQUEST_MODEL}"] * 2
    assert [span.kind for span in chat_spans] == [SpanKind.CLIENT] * 2
    for span_attributes in attributes:
        assert span_attributes["gen_ai.conversation.id"] == session_id
        assert span_attributes["gen_ai.request.model"] == SCRIPTED_REQUEST_MODEL
        assert span_attributes["gen_ai.response.model"] == SCRIPTED_RESPONSE_MODEL
        assert span_attributes["gen_ai.provider.name"] == SCRIPTED_MODEL_PROVIDER
    assert [span_attributes["gen_ai.response.id"] for span_attributes in attributes] == [
        "scripted-response-0-0",
        "scripted-response-0-1",
    ]
    assert [
        tuple(span_attributes["gen_ai.response.finish_reasons"]) for span_attributes in attributes
    ] == [("tool_calls",), ("stop",)]
