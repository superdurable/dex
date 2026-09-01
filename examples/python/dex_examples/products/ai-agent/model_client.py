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
        forced_tool_name: str | None = None,
        flow_id: str | None = None,
    ) -> ModelReply: ...

    async def summarize(
        self,
        config: AgentConfig,
        previous_summary: str,
        messages: Sequence[AgentMessage],
    ) -> str: ...

    def count_tokens(self, model: str, messages: Sequence[AgentMessage]) -> int: ...


class AgentCredentialStore:
    def __init__(self) -> None:
        self._api_keys: dict[str, str] = {}

    def set_api_key(self, flow_id: str, api_key: str | None) -> None:
        if api_key:
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
        write_progress: ProgressWriter,
        forced_tool_name: str | None = None,
        flow_id: str | None = None,
    ) -> ModelReply:
        if config.model == "mock/dex":
            return await _mock_completion(
                messages,
                tools,
                write_progress,
                forced_tool_name,
            )

        import litellm

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
                write_progress(content)
            reasoning = getattr(delta, "reasoning_content", None)
            if isinstance(reasoning, str) and reasoning:
                write_progress(reasoning)
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
    tools: Sequence[ToolDefinition],
    write_progress: ProgressWriter,
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
        return ModelReply(
            "I will prepare a plan for review.",
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="write_todos",
                    arguments_json=json.dumps({"todos": todos}),
                )
            ],
        )

    request = _last_user_content(messages)
    available_tool_names = {tool.name for tool in tools}
    if request.lower() == "/plan-clear" and "write_todos" in available_tool_names:
        return ModelReply(
            "I will clear the current plan.",
            [
                ToolCall(
                    id=f"call-{uuid4().hex}",
                    name="write_todos",
                    arguments_json='{"todos":[]}',
                )
            ],
        )
    if request.lower().startswith("/ask-many ") and {
        "request_user_input",
        "durable_wait",
    }.issubset(available_tool_names):
        prompt = request.removeprefix("/ask-many ").strip()
        return ModelReply(
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
    if request.lower().startswith("/ask ") and "request_user_input" in available_tool_names:
        content = "I need more information before I continue."
        await _stream_mock_content(content, write_progress)
        return ModelReply(
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

    active_plan = _active_plan(messages)
    if active_plan is not None and any(
        task.get("status") != "completed" for task in active_plan
    ):
        if _last_user_content(messages).lower().startswith("/plan-stop "):
            content = "I stopped before completing every plan task."
            await _stream_mock_content(content, write_progress)
            return ModelReply(content)
        return ModelReply(
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

    if not messages:
        content = "How can I help?"
    elif messages[-1].role == "tool" and messages[-1].tool_name == "write_todos":
        content = (
            "I completed the approved plan."
            if _plan_status(messages) == "completed"
            else "The plan is ready for review."
        )
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
    await _stream_mock_content(content, write_progress)
    return ModelReply(content)


async def _stream_mock_content(
    content: str,
    write_progress: ProgressWriter,
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
    return any(
        message.role == "system"
        and "The user approved this plan. Execute it" in message.content
        for message in messages
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
