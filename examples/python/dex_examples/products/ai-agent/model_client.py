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

"""Provider-neutral LiteLLM boundary for the AI Agent example."""

from __future__ import annotations

import json
from collections.abc import Callable, Sequence
from typing import Any, Protocol
from uuid import uuid4

from dex_examples.products.ai_agent.models import (
    AgentConfig,
    AgentMessage,
    ModelReply,
    ToolCall,
    ToolDefinition,
)

ProgressWriter = Callable[[str], None]


class ModelClient(Protocol):
    async def complete(
        self,
        config: AgentConfig,
        messages: Sequence[AgentMessage],
        tools: Sequence[ToolDefinition],
        write_progress: ProgressWriter,
    ) -> ModelReply: ...

    async def summarize(
        self,
        config: AgentConfig,
        previous_summary: str,
        messages: Sequence[AgentMessage],
    ) -> str: ...

    def count_tokens(self, model: str, messages: Sequence[AgentMessage]) -> int: ...


class LiteLLMModelClient:
    async def complete(
        self,
        config: AgentConfig,
        messages: Sequence[AgentMessage],
        tools: Sequence[ToolDefinition],
        write_progress: ProgressWriter,
    ) -> ModelReply:
        if config.model == "mock/dex":
            return await _mock_completion(messages, write_progress)

        import litellm

        request: dict[str, Any] = {
            "model": config.model,
            "messages": _to_litellm_messages(config.system_prompt, messages),
            "stream": True,
        }
        if tools:
            request["tools"] = [_to_litellm_tool(tool) for tool in tools]
        stream = await litellm.acompletion(**request)
        content_parts: list[str] = []
        tool_parts: dict[int, dict[str, str]] = {}
        async for chunk in stream:
            choices = getattr(chunk, "choices", None)
            if not choices:
                continue
            delta = choices[0].delta
            content = getattr(delta, "content", None)
            if isinstance(content, str) and content:
                content_parts.append(content)
                write_progress(content)
            for tool_delta in getattr(delta, "tool_calls", None) or []:
                index = int(getattr(tool_delta, "index", 0))
                current = tool_parts.setdefault(
                    index,
                    {"id": "", "name": "", "arguments": ""},
                )
                call_id = getattr(tool_delta, "id", None)
                if isinstance(call_id, str):
                    current["id"] += call_id
                function = getattr(tool_delta, "function", None)
                if function is not None:
                    name = getattr(function, "name", None)
                    arguments = getattr(function, "arguments", None)
                    if isinstance(name, str):
                        current["name"] += name
                    if isinstance(arguments, str):
                        current["arguments"] += arguments

        tool_calls = [
            ToolCall(
                id=parts["id"] or f"call-{uuid4().hex}",
                name=parts["name"],
                arguments_json=parts["arguments"] or "{}",
            )
            for _, parts in sorted(tool_parts.items())
        ]
        return ModelReply("".join(content_parts), tool_calls)

    async def summarize(
        self,
        config: AgentConfig,
        previous_summary: str,
        messages: Sequence[AgentMessage],
    ) -> str:
        if config.model == "mock/dex":
            return _local_summary(previous_summary, messages)

        import litellm

        transcript = json.dumps(
            [_message_as_json(message) for message in messages],
            ensure_ascii=False,
        )
        response = await litellm.acompletion(
            model=config.compaction_model or config.model,
            messages=[
                {
                    "role": "system",
                    "content": (
                        "Compact the conversation faithfully. Preserve decisions, "
                        "user preferences, unresolved work, tool outcomes, identifiers, "
                        "and facts needed by future turns."
                    ),
                },
                {
                    "role": "user",
                    "content": f"Previous summary:\n{previous_summary}\n\nMessages:\n{transcript}",
                },
            ],
        )
        content = response.choices[0].message.content
        if not isinstance(content, str) or not content.strip():
            raise RuntimeError("the compaction model returned an empty summary")
        return content

    def count_tokens(self, model: str, messages: Sequence[AgentMessage]) -> int:
        if model == "mock/dex":
            return sum(max(1, len(message.content) // 4) for message in messages)
        import litellm

        try:
            return int(
                litellm.token_counter(
                    model=model,
                    messages=_to_litellm_messages("", messages),
                )
            )
        except Exception:
            return sum(max(1, len(message.content) // 4) for message in messages)


async def _mock_completion(
    messages: Sequence[AgentMessage],
    write_progress: ProgressWriter,
) -> ModelReply:
    if not messages:
        content = "How can I help?"
    elif messages[-1].role == "tool":
        content = f"The tool finished with this result: {messages[-1].content}"
    else:
        request = messages[-1].content.strip()
        if request.lower().startswith("/wait "):
            parts = request.split(maxsplit=2)
            duration = int(parts[1])
            reason = parts[2] if len(parts) > 2 else "Requested wait"
            return ModelReply(
                "I will wait durably.",
                [
                    ToolCall(
                        id=f"call-{uuid4().hex}",
                        name="durable_wait",
                        arguments_json=json.dumps(
                            {"duration_seconds": duration, "reason": reason}
                        ),
                    )
                ],
            )
        if request.lower().startswith("/tool "):
            parts = request.split(maxsplit=2)
            if len(parts) != 3:
                raise ValueError("local /tool syntax is /tool <name> <json-object>")
            arguments = json.loads(parts[2])
            if not isinstance(arguments, dict):
                raise ValueError("local /tool arguments must be a JSON object")
            return ModelReply(
                f"I will call {parts[1]}.",
                [
                    ToolCall(
                        id=f"call-{uuid4().hex}",
                        name=parts[1],
                        arguments_json=json.dumps(arguments),
                    )
                ],
            )
        content = f"Local demo response: {request}"
    midpoint = len(content) // 2
    write_progress(content[:midpoint])
    write_progress(content[midpoint:])
    return ModelReply(content)


def _to_litellm_messages(
    system_prompt: str,
    messages: Sequence[AgentMessage],
) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    if system_prompt:
        result.append({"role": "system", "content": system_prompt})
    for message in messages:
        item: dict[str, Any] = {"role": message.role, "content": message.content}
        if message.tool_calls:
            item["tool_calls"] = [
                {
                    "id": call.id,
                    "type": "function",
                    "function": {
                        "name": call.name,
                        "arguments": call.arguments_json,
                    },
                }
                for call in message.tool_calls
            ]
        if message.tool_call_id:
            item["tool_call_id"] = message.tool_call_id
        if message.tool_name:
            item["name"] = message.tool_name
        result.append(item)
    return result


def _to_litellm_tool(tool: ToolDefinition) -> dict[str, Any]:
    return {
        "type": "function",
        "function": {
            "name": tool.name,
            "description": tool.description,
            "parameters": tool.input_schema,
        },
    }


def _message_as_json(message: AgentMessage) -> dict[str, Any]:
    return {
        "role": message.role,
        "content": message.content,
        "tool_calls": [
            {
                "id": call.id,
                "name": call.name,
                "arguments": call.arguments_json,
            }
            for call in message.tool_calls
        ],
        "tool_call_id": message.tool_call_id,
        "tool_name": message.tool_name,
    }


def _local_summary(
    previous_summary: str,
    messages: Sequence[AgentMessage],
) -> str:
    parts = [previous_summary] if previous_summary else []
    parts.extend(f"{message.role}: {message.content[:500]}" for message in messages)
    return "\n".join(parts)[-12_000:]
