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
from types import SimpleNamespace

import pytest
from werkzeug.exceptions import BadRequest

from dex_examples.products.ai_agent.mcp_registry import MCPRegistry
from dex_examples.products.ai_agent.http_routes import _provider_model
from dex_examples.products.ai_agent.model_client import (
    AgentCredentialStore,
    LiteLLMModelClient,
    _to_litellm_messages,
    _to_responses_input,
)
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    AgentEvent,
    AgentMessage,
    ProviderContextItem,
    ToolCall,
)
from dex_examples.products.ai_agent.ai_agent_flow import (
    _plan_tasks,
    _tool_safe_compaction_cutoff,
    _user_input_choices,
    _write_todos_definition,
)


def test_agent_config_requires_ordered_compaction_thresholds() -> None:
    with pytest.raises(ValueError, match="0 < keep < trigger < 1"):
        AgentConfig(
            compaction_trigger_fraction=0.5,
            compaction_keep_fraction=0.6,
        ).validate()


def test_agent_credentials_remain_outside_durable_config() -> None:
    credentials = AgentCredentialStore()

    credentials.set_api_key("flow-1", "secret-key")
    assert credentials.get_api_key("flow-1") == "secret-key"
    assert "secret-key" not in repr(AgentConfig())

    credentials.set_api_key("flow-1", None)
    assert credentials.get_api_key("flow-1") is None


@pytest.mark.parametrize("api_key", ["密钥", "secret\nkey"])
def test_agent_credentials_reject_invalid_http_header_values(api_key: str) -> None:
    credentials = AgentCredentialStore()

    with pytest.raises(ValueError, match="printable ASCII characters"):
        credentials.set_api_key("flow-1", api_key)

    assert credentials.get_api_key("flow-1") is None


def test_provider_model_adds_and_validates_litellm_prefix() -> None:
    assert _provider_model("openai", "gpt-example") == "openai/gpt-example"
    assert _provider_model("mock", "ignored") == "mock/dex"
    with pytest.raises(BadRequest, match="must start with openai/"):
        _provider_model("openai", "anthropic/model")


def test_compaction_does_not_split_tool_interactions() -> None:
    messages = [
        AgentMessage("user", "Search"),
        AgentMessage(
            "assistant",
            "",
            [
                ToolCall("call-one", "search", '{}'),
                ToolCall("call-two", "lookup", '{}'),
            ],
        ),
        AgentMessage("tool", "first", tool_call_id="call-one"),
        AgentMessage("tool", "second", tool_call_id="call-two"),
        AgentMessage("assistant", "Done"),
    ]

    assert _tool_safe_compaction_cutoff(messages[:3], 10, 12) == 10
    assert _tool_safe_compaction_cutoff(messages[:4], 10, 13) == 13


def test_model_context_omits_orphan_tool_outputs() -> None:
    messages = [
        AgentMessage("system", "Summary through the missing tool call"),
        AgentMessage("tool", "orphan", tool_call_id="missing-call"),
        AgentMessage(
            "assistant",
            "",
            [ToolCall("retained-call", "search", '{}')],
        ),
        AgentMessage("tool", "retained", tool_call_id="retained-call"),
    ]

    responses_input = _to_responses_input(messages)
    assert not any(item.get("output") == "orphan" for item in responses_input)
    assert any(item.get("output") == "retained" for item in responses_input)
    chat_messages = _to_litellm_messages("", messages)
    assert not any(item.get("content") == "orphan" for item in chat_messages)
    assert any(item.get("content") == "retained" for item in chat_messages)


async def test_mock_model_returns_a_durable_wait_tool() -> None:
    progress: list[str] = []

    def write_progress(chunk: str) -> None:
        progress.append(chunk)

    reply = await LiteLLMModelClient().complete(
        AgentConfig(),
        [AgentMessage("user", "/wait 12 reserve tickets")],
        [],
        write_progress,
        lambda chunk: None,
        lambda event: None,
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
        lambda chunk: None,
        lambda event: None,
        forced_tool_name="write_todos",
    )

    assert [call.name for call in reply.tool_calls] == ["write_todos"]
    assert "Investigate the incident" in reply.tool_calls[0].arguments_json


async def test_openai_responses_separates_reasoning_text_and_tool_activity(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import litellm

    request: dict[str, object] = {}

    async def response_stream():
        yield {
            "type": "response.reasoning_summary_text.delta",
            "delta": "Checking constraints.",
        }
        yield {"type": "response.output_text.delta", "delta": "I can help."}
        yield {
            "type": "response.output_item.done",
            "output_index": 0,
            "item": {
                "id": "rs-new",
                "type": "reasoning",
                "summary": [
                    {"type": "summary_text", "text": "Checking constraints."}
                ],
                "encrypted_content": "encrypted-new",
                "status": "completed",
            },
        }
        yield {
            "type": "response.output_item.done",
            "output_index": 1,
            "item": {
                "type": "function_call",
                "call_id": "call-plan",
                "name": "write_todos",
                "arguments": '{"todos":[]}',
            },
        }

    async def fake_aresponses(**kwargs: object):
        request.update(kwargs)
        return response_stream()

    async def fail_acompletion(**kwargs: object):
        raise AssertionError(f"unexpected Chat Completions request: {kwargs}")

    monkeypatch.setattr(litellm, "aresponses", fake_aresponses)
    monkeypatch.setattr(litellm, "acompletion", fail_acompletion)
    assistant_text: list[str] = []
    reasoning_summary: list[str] = []
    activity: list[AgentEvent] = []

    reply = await LiteLLMModelClient().complete(
        AgentConfig(model="openai/gpt-5-mini"),
        [
            AgentMessage(
                "assistant",
                "",
                [ToolCall("call-old", "search", '{"query":"Dex"}')],
                provider_context_items=[
                    ProviderContextItem(
                        "openai",
                        '{"id":"rs-old","type":"reasoning","summary":[],'
                        '"encrypted_content":"encrypted-old"}',
                    )
                ],
            ),
            AgentMessage(
                "tool",
                '{"results":[]}',
                tool_call_id="call-old",
                tool_name="search",
            ),
            AgentMessage("user", "Make a plan"),
        ],
        [_write_todos_definition()],
        assistant_text.append,
        reasoning_summary.append,
        activity.append,
    )

    assert request["reasoning"] == {"summary": "auto"}
    assert request["include"] == ["reasoning.encrypted_content"]
    assert request["store"] is False
    assert request["input"] == [
        {
            "id": "rs-old",
            "type": "reasoning",
            "summary": [],
            "encrypted_content": "encrypted-old",
        },
        {
            "type": "function_call",
            "call_id": "call-old",
            "name": "search",
            "arguments": '{"query":"Dex"}',
        },
        {
            "type": "function_call_output",
            "call_id": "call-old",
            "output": '{"results":[]}',
        },
        {"type": "message", "role": "user", "content": "Make a plan"}
    ]
    assert assistant_text == ["I can help."]
    assert reasoning_summary == ["Checking constraints."]
    assert [event.kind for event in activity] == ["model_tool_call"]
    assert reply.content == "I can help."
    assert reply.tool_calls == [
        ToolCall("call-plan", "write_todos", '{"todos":[]}')
    ]
    assert reply.provider_context_items == [
        ProviderContextItem(
            "openai",
            '{"id":"rs-new","summary":[{"type":"summary_text",'
            '"text":"Checking constraints."}],"type":"reasoning",'
            '"encrypted_content":"encrypted-new","status":"completed"}',
        )
    ]


async def test_non_openai_provider_does_not_infer_reasoning_summary(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import litellm

    async def completion_stream():
        yield SimpleNamespace(
            choices=[
                SimpleNamespace(
                    delta=SimpleNamespace(
                        content="Visible response",
                        reasoning_content="Provider-private reasoning",
                        tool_calls=[],
                    )
                )
            ]
        )

    async def fake_acompletion(**kwargs: object):
        return completion_stream()

    monkeypatch.setattr(litellm, "acompletion", fake_acompletion)
    assistant_text: list[str] = []
    reasoning_summary: list[str] = []

    reply = await LiteLLMModelClient().complete(
        AgentConfig(model="anthropic/claude-sonnet-4-5"),
        [AgentMessage("user", "Hello")],
        [],
        assistant_text.append,
        reasoning_summary.append,
        lambda event: None,
    )

    assert reply.content == "Visible response"
    assert assistant_text == ["Visible response"]
    assert reasoning_summary == []


async def test_compaction_uses_the_flow_api_key(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import litellm

    request: dict[str, object] = {}

    async def fake_acompletion(**kwargs: object):
        request.update(kwargs)
        return SimpleNamespace(
            choices=[SimpleNamespace(message=SimpleNamespace(content="Summary"))]
        )

    monkeypatch.setattr(litellm, "acompletion", fake_acompletion)
    credentials = AgentCredentialStore()
    credentials.set_api_key("flow-1", "session-api-key")

    summary = await LiteLLMModelClient(credentials).summarize(
        AgentConfig(model="openai/gpt-5-mini"),
        "Earlier summary",
        [AgentMessage("user", "New context")],
        flow_id="flow-1",
    )

    assert summary == "Summary"
    assert request["api_key"] == "session-api-key"


def test_plan_tasks_reject_invalid_status() -> None:
    call = ToolCall(
        "call-plan",
        "write_todos",
        '{"todos":[{"content":"Inspect","status":"blocked"}]}',
    )

    with pytest.raises(ValueError, match="status is invalid"):
        _plan_tasks(call)


def test_user_input_choices_validate_selectable_answers() -> None:
    assert _user_input_choices([" Staging ", "Production"]) == [
        "Staging",
        "Production",
    ]
    with pytest.raises(ValueError, match="zero or 2-8"):
        _user_input_choices(["only one"])
    with pytest.raises(ValueError, match="unique"):
        _user_input_choices(["same", "same"])
