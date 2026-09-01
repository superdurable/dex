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

"""Tests pure context, model, and MCP configuration behavior."""

from __future__ import annotations

from pathlib import Path

import pytest

from dex_examples.products.ai_agent.mcp_registry import MCPRegistry
from dex_examples.products.ai_agent.model_client import LiteLLMModelClient
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    AgentMessage,
    ToolCall,
)
from dex_examples.products.ai_agent.ai_agent_flow import (
    _plan_tasks,
    _write_todos_definition,
)


def test_agent_config_requires_ordered_compaction_thresholds() -> None:
    with pytest.raises(ValueError, match="0 < keep < trigger < 1"):
        AgentConfig(
            compaction_trigger_fraction=0.5,
            compaction_keep_fraction=0.6,
        ).validate()


async def test_mock_model_returns_a_durable_wait_tool() -> None:
    progress: list[str] = []

    async def write_progress(chunk: str) -> None:
        progress.append(chunk)

    reply = await LiteLLMModelClient().complete(
        AgentConfig(),
        [AgentMessage("user", "/wait 12 reserve tickets")],
        [],
        write_progress,
    )

    assert reply.tool_calls[0].name == "durable_wait"
    assert '"duration_seconds": 12' in reply.tool_calls[0].arguments_json
    assert progress == []


def test_mcp_config_reads_secret_names_without_secret_values(
    tmp_path: Path,
) -> None:
    config_path = tmp_path / "mcp.yaml"
    config_path.write_text(
        """
servers:
  - name: docs
    transport: streamable_http
    url: https://mcp.example.test
    headers:
      Authorization: DOCS_MCP_AUTHORIZATION
    tools:
      search:
        read_only: true
        maximum_attempts: 4
""".strip()
    )

    registry = MCPRegistry.from_file(config_path)

    assert registry.server_names == ["docs"]
    assert registry.tool_names == []


def test_mock_token_count_is_provider_independent() -> None:
    count = LiteLLMModelClient().count_tokens(
        "mock/dex",
        [AgentMessage("user", "a" * 40)],
    )

    assert count == 10


async def test_mock_model_creates_a_structured_plan_when_forced() -> None:
    reply = await LiteLLMModelClient().complete(
        AgentConfig(),
        [AgentMessage("user", "Investigate the incident")],
        [_write_todos_definition()],
        lambda chunk: None,
        "write_todos",
    )

    assert [call.name for call in reply.tool_calls] == ["write_todos"]
    assert "Investigate the incident" in reply.tool_calls[0].arguments_json


def test_plan_tasks_reject_invalid_status() -> None:
    call = ToolCall(
        "call-plan",
        "write_todos",
        '{"todos":[{"content":"Inspect","status":"blocked"}]}',
    )

    with pytest.raises(ValueError, match="status is invalid"):
        _plan_tasks(call)
