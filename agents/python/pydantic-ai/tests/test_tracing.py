# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The model and response that Pydantic AI reports name each ``chat`` span."""

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
from opentelemetry.sdk.trace import ReadableSpan, TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.trace import SpanKind
from pydantic_ai import ModelResponse
from pydantic_ai.agent import WrapperAgent

from dex_agents import AgentSessionFlow, AgentSessionOptions, AgentTracingOptions
from dex_agents.testing import AgentTestStack, wait_for_agent_snapshot, wait_until
from dex_agents_pydantic_ai import PydanticAIRunner
from dex_agents_pydantic_ai._capability import gen_ai_provider_name
from tests.conftest import PACKAGE_DIR, WORKER_MODULE
from tests.scripted_agent import (
    STATE_DIR_ENVIRONMENT_VARIABLE,
    TOOL_POLICIES,
    build_scripted_agent,
)

# FunctionModel names itself after its functions and reports no provider, response ID, or reason.
SCRIPTED_MODEL_NAME = "function:respond:stream_response"


@pytest.mark.parametrize(
    ("provider_name", "canonical_system", "expected_provider_name"),
    [
        ("openai", "openai", "openai"),
        ("openai", "azure", "azure.ai.openai"),
        ("azure", "azure", "azure.ai.openai"),
        ("anthropic", None, "anthropic"),
        ("google", "google", "gcp.gemini"),
        ("google", "google-cloud", "gcp.vertex_ai"),
        ("google-cloud", "google-cloud", "gcp.vertex_ai"),
        ("google-cloud", "google", "gcp.gemini"),
        ("bedrock", None, "aws.bedrock"),
        ("bedrock-mantle", "bedrock-mantle", "aws.bedrock"),
        ("xai", "xai", "x_ai"),
        ("mistral", None, "mistral"),
        ("moonshotai", None, "moonshot_ai"),
        ("groq", None, "groq"),
        ("cohere", None, "cohere"),
        ("deepseek", None, "deepseek"),
        ("openai-codex", "openai", "openai-codex"),
        ("openrouter", "openrouter", "openrouter"),
        (None, None, None),
    ],
)
def test_provider_names_map_to_well_known_gen_ai_provider_names(
    provider_name: str | None, canonical_system: str | None, expected_provider_name: str | None
) -> None:
    response = ModelResponse(
        parts=[], provider_name=provider_name, canonical_system=canonical_system
    )

    assert gen_ai_provider_name(response) == expected_provider_name


async def test_each_chat_span_is_named_after_the_model_pydantic_ai_reports(
    tmp_path: Path,
) -> None:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    flow = AgentSessionFlow(
        PydanticAIRunner(
            WrapperAgent(build_scripted_agent(state_dir)),
            deps_factory=lambda session_id: f"deps-{session_id}",
        ),
        flow_type="TracedPydanticAIAgent",
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

            chat_spans = await wait_until("both chat spans", probe)
        finally:
            await worker.stop()
            with contextlib.suppress(asyncio.CancelledError):
                await serving
            blob_cache.close()
    provider.shutdown()

    for span in chat_spans:
        attributes: dict[str, Any] = dict(span.attributes or {})
        assert span.name == f"chat {SCRIPTED_MODEL_NAME}"
        assert span.kind is SpanKind.CLIENT
        assert attributes["gen_ai.conversation.id"] == session_id
        assert attributes["gen_ai.request.model"] == SCRIPTED_MODEL_NAME
        assert attributes["gen_ai.response.model"] == SCRIPTED_MODEL_NAME
        # FunctionModel's system is "function", which names no provider; none is reported.
        assert "gen_ai.provider.name" not in attributes
        assert "gen_ai.response.id" not in attributes
        assert "gen_ai.response.finish_reasons" not in attributes
    assert sorted(
        "dex.agent.requested_tool_call.ids" in dict(span.attributes or {}) for span in chat_spans
    ) == [False, True]
