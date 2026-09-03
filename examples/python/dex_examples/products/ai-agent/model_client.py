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

import asyncio
import json
from collections.abc import Callable, Sequence
from typing import Any, Protocol
from uuid import uuid4

from dex_examples.products.ai_agent.models import (
    AgentConfig,
    AgentEvent,
    AgentMessage,
    ModelReply,
    ProviderContextItem,
    ToolCall,
    ToolDefinition,
)

TextWriter = Callable[[str], None]
ActivityWriter = Callable[[AgentEvent], None]


class ModelClient(Protocol):
    async def complete(
        self,
        config: AgentConfig,
        messages: Sequence[AgentMessage],
        tools: Sequence[ToolDefinition],
        write_assistant_text: TextWriter,
        write_reasoning_summary: TextWriter,
        write_activity: ActivityWriter,
        forced_tool_name: str | None = None,
        flow_id: str | None = None,
    ) -> ModelReply: ...

    async def summarize(
        self,
        config: AgentConfig,
        previous_summary: str,
        messages: Sequence[AgentMessage],
        flow_id: str | None = None,
    ) -> str: ...

    def count_tokens(self, model: str, messages: Sequence[AgentMessage]) -> int: ...


class AgentCredentialStore:
    def __init__(self) -> None:
        self._api_keys: dict[str, str] = {}

    def set_api_key(self, flow_id: str, api_key: str | None) -> None:
        if api_key:
            if not api_key.isascii() or not api_key.isprintable():
                raise ValueError(
                    "apiKey must contain only printable ASCII characters; "
                    "paste only the raw key value"
                )
            self._api_keys[flow_id] = api_key
        else:
            self._api_keys.pop(flow_id, None)

    def get_api_key(self, flow_id: str | None) -> str | None:
        if flow_id is None:
            return None
        return self._api_keys.get(flow_id)


class LiteLLMModelClient:
    def __init__(self, credentials: AgentCredentialStore | None = None) -> None:
        self._credentials = credentials or AgentCredentialStore()

    async def complete(
        self,
        config: AgentConfig,
        messages: Sequence[AgentMessage],
        tools: Sequence[ToolDefinition],
        write_assistant_text: TextWriter,
        write_reasoning_summary: TextWriter,
        write_activity: ActivityWriter,
        forced_tool_name: str | None = None,
        flow_id: str | None = None,
    ) -> ModelReply:
        if config.model == "mock/dex":
            return await _mock_completion(
                messages,
                tools,
                write_assistant_text,
                write_activity,
                forced_tool_name,
            )

        import litellm

        if config.model.startswith("openai/"):
            return await _complete_openai_response(
                litellm,
                config,
                messages,
                tools,
                write_assistant_text,
                write_reasoning_summary,
                write_activity,
                forced_tool_name,
                self._credentials.get_api_key(flow_id),
            )

        request: dict[str, Any] = {
            "model": config.model,
            "messages": _to_litellm_messages(config.system_prompt, messages),
            "stream": True,
        }
        api_key = self._credentials.get_api_key(flow_id)
        if api_key is not None:
            request["api_key"] = api_key
        if tools:
            request["tools"] = [_to_litellm_tool(tool) for tool in tools]
        if forced_tool_name is not None:
            if forced_tool_name not in {tool.name for tool in tools}:
                raise ValueError(f"forced tool {forced_tool_name!r} is not available")
            request["tool_choice"] = {
                "type": "function",
                "function": {"name": forced_tool_name},
            }
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
                write_assistant_text(content)
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
        _write_tool_call_activities(tool_calls, write_activity)
        return ModelReply("".join(content_parts), tool_calls)

    async def summarize(
        self,
        config: AgentConfig,
        previous_summary: str,
        messages: Sequence[AgentMessage],
        flow_id: str | None = None,
    ) -> str:
        if config.model == "mock/dex":
            return _local_summary(previous_summary, messages)

        import litellm

        transcript = json.dumps(
            [_message_as_json(message) for message in messages],
            ensure_ascii=False,
        )
        request: dict[str, Any] = {
            "model": config.compaction_model or config.model,
            "messages": [
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
        }
        api_key = self._credentials.get_api_key(flow_id)
        if api_key is not None:
            request["api_key"] = api_key
        response = await litellm.acompletion(**request)
        content = response.choices[0].message.content
        if not isinstance(content, str) or not content.strip():
            raise RuntimeError("the compaction model returned an empty summary")
        return content

    def count_tokens(self, model: str, messages: Sequence[AgentMessage]) -> int:
        if model == "mock/dex":
            return sum(max(1, len(message.content) // 4) for message in messages)
        import litellm

        provider_context_tokens = sum(
            max(
                1,
                len(item.item_json) // 4,
            )
            for message in messages
            for item in message.provider_context_items
        )
        try:
            message_tokens = int(
                litellm.token_counter(
                    model=model,
                    messages=_to_litellm_messages("", messages),
                )
            )
            return message_tokens + provider_context_tokens
        except Exception:
            return provider_context_tokens + sum(
                max(1, len(message.content) // 4) for message in messages
            )


async def _complete_openai_response(
    litellm: Any,
    config: AgentConfig,
    messages: Sequence[AgentMessage],
    tools: Sequence[ToolDefinition],
    write_assistant_text: TextWriter,
    write_reasoning_summary: TextWriter,
    write_activity: ActivityWriter,
    forced_tool_name: str | None,
    api_key: str | None,
) -> ModelReply:
    request: dict[str, Any] = {
        "model": config.model,
        "input": _to_responses_input(messages),
        "include": ["reasoning.encrypted_content"],
        "instructions": config.system_prompt,
        "reasoning": {"summary": "auto"},
        "store": False,
        "stream": True,
    }
    if api_key is not None:
        request["api_key"] = api_key
    if tools:
        request["tools"] = [_to_responses_tool(tool) for tool in tools]
    if forced_tool_name is not None:
        if forced_tool_name not in {tool.name for tool in tools}:
            raise ValueError(f"forced tool {forced_tool_name!r} is not available")
        request["tool_choice"] = {
            "type": "function",
            "name": forced_tool_name,
        }
    stream = await litellm.aresponses(**request)
    return await _consume_openai_response_stream(
        stream,
        write_assistant_text,
        write_reasoning_summary,
        write_activity,
    )


async def _consume_openai_response_stream(
    stream: Any,
    write_assistant_text: TextWriter,
    write_reasoning_summary: TextWriter,
    write_activity: ActivityWriter,
) -> ModelReply:
    content_parts: list[str] = []
    tool_calls: dict[int, ToolCall] = {}
    provider_context_items: list[ProviderContextItem] = []
    async for event in stream:
        event_type = _response_field(event, "type")
        if event_type in {
            "response.output_text.delta",
            "response.refusal.delta",
        }:
            delta = _response_field(event, "delta")
            if isinstance(delta, str) and delta:
                content_parts.append(delta)
                write_assistant_text(delta)
            continue
        if event_type == "response.reasoning_summary_text.delta":
            delta = _response_field(event, "delta")
            if isinstance(delta, str) and delta:
                write_reasoning_summary(delta)
            continue
        if event_type != "response.output_item.done":
            continue
        item = _response_field(event, "item")
        item_type = _response_field(item, "type")
        if item_type == "reasoning":
            provider_context_items.append(_reasoning_context_item(item))
            continue
        if item_type != "function_call":
            continue
        output_index = _response_field(event, "output_index")
        name = _response_field(item, "name")
        arguments = _response_field(item, "arguments")
        call_id = _response_field(item, "call_id")
        if not isinstance(output_index, int):
            raise RuntimeError(
                "OpenAI returned a function call without an output index"
            )
        if not isinstance(name, str) or not name:
            raise RuntimeError("OpenAI returned a function call without a name")
        if not isinstance(arguments, str):
            raise RuntimeError("OpenAI returned a function call without arguments")
        if not isinstance(call_id, str) or not call_id:
            raise RuntimeError("OpenAI returned a function call without a call ID")
        call = ToolCall(call_id, name, arguments)
        tool_calls[output_index] = call
        _write_tool_call_activity(call, write_activity)
    return ModelReply(
        "".join(content_parts),
        [call for _, call in sorted(tool_calls.items())],
        provider_context_items,
    )


def _response_field(value: object, name: str) -> object:
    if isinstance(value, dict):
        return value.get(name)
    return getattr(value, name, None)


def _reasoning_context_item(value: object) -> ProviderContextItem:
    if isinstance(value, dict):
        serialized = value
    else:
        model_dump = getattr(value, "model_dump", None)
        if not callable(model_dump):
            raise RuntimeError("OpenAI returned an invalid reasoning item")
        serialized = model_dump(mode="json", exclude_none=True)
    if not isinstance(serialized, dict):
        raise RuntimeError("OpenAI returned an invalid reasoning item")
    required_fields = ("id", "summary", "type")
    if any(field not in serialized for field in required_fields):
        raise RuntimeError("OpenAI returned an incomplete reasoning item")
    allowed_fields = (
        "id",
        "summary",
        "type",
        "content",
        "encrypted_content",
        "status",
    )
    item = {
        field: serialized[field]
        for field in allowed_fields
        if field in serialized and serialized[field] is not None
    }
    return ProviderContextItem(
        "openai",
        json.dumps(item, ensure_ascii=False, separators=(",", ":")),
    )


def _write_tool_call_activities(
    tool_calls: Sequence[ToolCall],
    write_activity: ActivityWriter,
) -> None:
    for call in tool_calls:
        _write_tool_call_activity(call, write_activity)


def _write_tool_call_activity(
    call: ToolCall,
    write_activity: ActivityWriter,
) -> None:
    write_activity(
        AgentEvent(
            "model_tool_call",
            f"Model requested {call.name}.",
            call.id,
            call.name,
        )
    )


async def _mock_completion(
    messages: Sequence[AgentMessage],
    tools: Sequence[ToolDefinition],
    write_assistant_text: TextWriter,
    write_activity: ActivityWriter,
    forced_tool_name: str | None,
) -> ModelReply:
    if forced_tool_name is not None:
        if forced_tool_name not in {tool.name for tool in tools}:
            raise ValueError(f"forced tool {forced_tool_name!r} is not available")
        if forced_tool_name != "write_todos":
            raise ValueError(f"mock/dex cannot force tool {forced_tool_name!r}")
        request = _last_user_content(messages) or "the requested objective"
        todos = (
            []
            if request.lower() == "/plan-clear"
            else [
                {
                    "content": f"Complete the objective: {request}",
                    "status": "pending",
                },
                {
                    "content": "Verify and report the result",
                    "status": "pending",
                },
            ]
        )
        reply = ModelReply(
            "I will prepare a plan for review.",
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="write_todos",
                    arguments_json=json.dumps({"todos": todos}),
                )
            ],
        )
        _write_tool_call_activities(reply.tool_calls, write_activity)
        return reply

    request = _last_user_content(messages)
    available_tool_names = {tool.name for tool in tools}
    if request.lower() == "/plan-clear" and "write_todos" in available_tool_names:
        reply = ModelReply(
            "I will clear the current plan.",
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="write_todos",
                    arguments_json='{"todos":[]}',
                )
            ],
        )
        _write_tool_call_activities(reply.tool_calls, write_activity)
        return reply
    if (
        request.lower().startswith("/choose ")
        and "request_user_input" in available_tool_names
    ):
        parts = [part.strip() for part in request.removeprefix("/choose ").split("|")]
        if len(parts) < 3 or any(not part for part in parts):
            raise ValueError(
                "local /choose syntax is "
                "/choose <prompt> | <choice> | <choice>"
            )
        reply = ModelReply(
            "Please choose an option so I can continue.",
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="request_user_input",
                    arguments_json=json.dumps(
                        {"prompt": parts[0], "choices": parts[1:]}
                    ),
                )
            ],
        )
        _write_tool_call_activities(reply.tool_calls, write_activity)
        return reply
    if request.lower().startswith("/ask-many ") and {
        "request_user_input",
        "durable_wait",
    }.issubset(available_tool_names):
        prompt = request.removeprefix("/ask-many ").strip()
        reply = ModelReply(
            "I need more information before I continue.",
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="request_user_input",
                    arguments_json=json.dumps({"prompt": prompt}),
                ),
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="durable_wait",
                    arguments_json=(
                        '{"duration_seconds":60,"reason":"superseded test"}'
                    ),
                ),
            ],
        )
        _write_tool_call_activities(reply.tool_calls, write_activity)
        return reply
    if request.lower().startswith("/ask ") and "request_user_input" in available_tool_names:
        content = "I need more information before I continue."
        await _stream_mock_content(content, write_assistant_text)
        reply = ModelReply(
            content,
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="request_user_input",
                    arguments_json=json.dumps(
                        {"prompt": request.removeprefix("/ask ").strip()}
                    ),
                )
            ],
        )
        _write_tool_call_activities(reply.tool_calls, write_activity)
        return reply

    active_plan = _active_plan(messages)
    if active_plan is not None and any(
        task.get("status") != "completed" for task in active_plan
    ):
        if _last_user_content(messages).lower().startswith("/plan-stop "):
            content = "I stopped before completing every plan task."
            await _stream_mock_content(content, write_assistant_text)
            return ModelReply(content)
        reply = ModelReply(
            "I will execute the approved plan.",
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="write_todos",
                    arguments_json=json.dumps(
                        {"todos": _next_mock_plan_tasks(active_plan)}
                    ),
                )
            ],
        )
        _write_tool_call_activities(reply.tool_calls, write_activity)
        return reply

    last_message = _last_conversation_message(messages)
    if last_message is None:
        content = "How can I help?"
    elif last_message.role == "tool" and last_message.tool_name == "write_todos":
        content = (
            "I completed the approved plan."
            if _plan_status(messages) == "completed"
            else "The plan is ready for review."
        )
    elif last_message.role == "tool":
        content = f"The tool finished with this result: {last_message.content}"
    else:
        request = _last_user_content(messages)
        if request.lower().startswith("/wait "):
            parts = request.split(maxsplit=2)
            duration = int(parts[1])
            reason = parts[2] if len(parts) > 2 else "Requested wait"
            reply = ModelReply(
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
            _write_tool_call_activities(reply.tool_calls, write_activity)
            return reply
        if request.lower().startswith("/tool "):
            parts = request.split(maxsplit=2)
            if len(parts) != 3:
                raise ValueError("local /tool syntax is /tool <name> <json-object>")
            arguments = json.loads(parts[2])
            if not isinstance(arguments, dict):
                raise ValueError("local /tool arguments must be a JSON object")
            reply = ModelReply(
                f"I will call {parts[1]}.",
                [
                    ToolCall(
                        id=f"call-{uuid4().hex}",
                        name=parts[1],
                        arguments_json=json.dumps(arguments),
                    )
                ],
            )
            _write_tool_call_activities(reply.tool_calls, write_activity)
            return reply
        content = f"Local demo response: {request}"
    await _stream_mock_content(content, write_assistant_text)
    return ModelReply(content)


async def _stream_mock_content(
    content: str,
    write_progress: TextWriter,
) -> None:
    midpoint = len(content) // 2
    write_progress(content[:midpoint])
    await asyncio.sleep(0.2)
    write_progress(content[midpoint:])
    await asyncio.sleep(0.2)


def _last_user_content(messages: Sequence[AgentMessage]) -> str:
    for message in reversed(messages):
        if message.role == "user":
            return message.content.strip()
    return ""


def _last_conversation_message(
    messages: Sequence[AgentMessage],
) -> AgentMessage | None:
    return next(
        (message for message in reversed(messages) if message.role != "system"),
        None,
    )


def _active_plan(messages: Sequence[AgentMessage]) -> list[dict[str, Any]] | None:
    if not _is_plan_execution(messages):
        return None
    plan = _durable_plan(messages)
    if plan is None or plan.get("status") != "active":
        return None
    tasks = plan.get("tasks")
    if not isinstance(tasks, list):
        return None
    return [task for task in tasks if isinstance(task, dict)]


def _is_plan_execution(messages: Sequence[AgentMessage]) -> bool:
    return bool(
        messages
        and messages[-1].role == "system"
        and "The user approved this plan. Execute it" in messages[-1].content
    )


def _plan_status(messages: Sequence[AgentMessage]) -> str | None:
    plan = _durable_plan(messages)
    status = plan.get("status") if plan is not None else None
    return status if isinstance(status, str) else None


def _durable_plan(messages: Sequence[AgentMessage]) -> dict[str, Any] | None:
    prefix = "Current durable plan: "
    for message in messages:
        if message.role != "system" or not message.content.startswith(prefix):
            continue
        try:
            plan = json.loads(message.content.removeprefix(prefix).split("\n", 1)[0])
        except json.JSONDecodeError:
            return None
        if not isinstance(plan, dict):
            return None
        return plan
    return None


def _next_mock_plan_tasks(tasks: list[dict[str, Any]]) -> list[dict[str, str]]:
    current_index = next(
        (
            index
            for index, task in enumerate(tasks)
            if task.get("status") == "in_progress"
        ),
        None,
    )
    if current_index is None:
        next_index = next(
            index
            for index, task in enumerate(tasks)
            if task.get("status") == "pending"
        )
        return [
            {
                "content": str(task.get("content", "")),
                "status": "in_progress" if index == next_index else str(task["status"]),
            }
            for index, task in enumerate(tasks)
        ]
    next_pending = next(
        (
            index
            for index, task in enumerate(tasks)
            if index > current_index and task.get("status") == "pending"
        ),
        None,
    )
    return [
        {
            "content": str(task.get("content", "")),
            "status": (
                "completed"
                if index == current_index
                else "in_progress"
                if index == next_pending
                else str(task["status"])
            ),
        }
        for index, task in enumerate(tasks)
    ]


def _to_litellm_messages(
    system_prompt: str,
    messages: Sequence[AgentMessage],
) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    if system_prompt:
        result.append({"role": "system", "content": system_prompt})
    for message in _without_orphan_tool_outputs(messages):
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


def _to_responses_input(
    messages: Sequence[AgentMessage],
) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    for message in _without_orphan_tool_outputs(messages):
        if message.role == "tool":
            if not message.tool_call_id:
                raise ValueError("tool messages require a tool call ID")
            result.append(
                {
                    "type": "function_call_output",
                    "call_id": message.tool_call_id,
                    "output": message.content,
                }
            )
            continue
        for context_item in message.provider_context_items:
            if context_item.provider == "openai":
                result.append(json.loads(context_item.item_json))
        if message.content:
            result.append(
                {
                    "type": "message",
                    "role": message.role,
                    "content": message.content,
                }
            )
        result.extend(
            {
                "type": "function_call",
                "call_id": call.id,
                "name": call.name,
                "arguments": call.arguments_json,
            }
            for call in message.tool_calls
        )
    return result


def _without_orphan_tool_outputs(
    messages: Sequence[AgentMessage],
) -> list[AgentMessage]:
    known_call_ids: set[str] = set()
    result: list[AgentMessage] = []
    for message in messages:
        if message.role == "tool":
            if message.tool_call_id not in known_call_ids:
                continue
            known_call_ids.remove(message.tool_call_id)
        else:
            known_call_ids.update(call.id for call in message.tool_calls)
        result.append(message)
    return result


def _to_responses_tool(tool: ToolDefinition) -> dict[str, Any]:
    return {
        "type": "function",
        "name": tool.name,
        "description": tool.description,
        "parameters": tool.input_schema,
        "strict": False,
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
