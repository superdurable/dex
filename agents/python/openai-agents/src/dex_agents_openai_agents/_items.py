# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Conversion between OpenAI Agents SDK input items and stored agent messages."""

from __future__ import annotations

import json
from collections.abc import Callable, Mapping, Sequence
from typing import Any

from pydantic import BaseModel

from dex_agents import AgentMessage, AgentToolCall, FrameworkMessage, MessageRole
from dex_agents_openai_agents._images import has_input_image_parts

_TEXT_PART_TYPES = ("input_text", "output_text", "text")


def build_user_input_item(text: str) -> dict[str, Any]:
    return {"role": "user", "content": text}


def read_input_items(messages: Sequence[AgentMessage]) -> list[Any]:
    return [json.loads(message.framework_item) for message in messages]


def project_input_items(
    history: Sequence[AgentMessage],
    new_items: Sequence[Mapping[str, Any]],
    recorded_tool_call: Callable[[str], AgentToolCall | None],
) -> list[FrameworkMessage]:
    tool_names_by_call_id = {
        message.tool_call_id: message.tool_name
        for message in history
        if message.tool_call_id is not None and message.tool_name is not None
    }
    projected: list[FrameworkMessage] = []
    for item in new_items:
        message = _project_input_item(item, tool_names_by_call_id, recorded_tool_call)
        if message.tool_call_id is not None and message.tool_name is not None:
            tool_names_by_call_id.setdefault(message.tool_call_id, message.tool_name)
        projected.append(message)
    return projected


def format_tool_output(output: Any) -> str:
    if isinstance(output, str):
        return output
    if isinstance(output, BaseModel):
        text = getattr(output, "text", None)
        if getattr(output, "type", None) == "text" and isinstance(text, str):
            return text
        return output.model_dump_json()
    try:
        return json.dumps(output, ensure_ascii=False, default=_dump_model)
    except (TypeError, ValueError):
        return str(output)


def _project_input_item(
    item: Mapping[str, Any],
    tool_names_by_call_id: Mapping[str, str],
    recorded_tool_call: Callable[[str], AgentToolCall | None],
) -> FrameworkMessage:
    framework_item = json.dumps(item, ensure_ascii=False)
    item_type = item.get("type")
    role = item.get("role")
    call_id = item.get("call_id") if isinstance(item.get("call_id"), str) else None
    if item_type in (None, "message") and role == "user":
        return FrameworkMessage(
            role=MessageRole.USER, framework_item=framework_item, text=_message_text(item)
        )
    if item_type in (None, "message"):
        return FrameworkMessage(
            role=MessageRole.ASSISTANT, framework_item=framework_item, text=_message_text(item)
        )
    if item_type == "function_call":
        name = str(item.get("name") or "")
        namespace = item.get("namespace")
        qualified_name = f"{namespace}.{name}" if namespace and namespace != name else name
        return FrameworkMessage(
            role=MessageRole.ASSISTANT,
            framework_item=framework_item,
            tool_call_id=call_id,
            tool_name=qualified_name,
            tool_arguments=str(item.get("arguments") or ""),
        )
    if isinstance(item_type, str) and item_type.endswith("_output"):
        output = item.get("output")
        if has_input_image_parts(output):
            return _project_image_tool_output(
                item, framework_item, tool_names_by_call_id, recorded_tool_call
            )
        return FrameworkMessage(
            role=MessageRole.TOOL,
            framework_item=framework_item,
            tool_call_id=call_id,
            tool_name=tool_names_by_call_id.get(call_id) if call_id is not None else None,
            tool_output=format_tool_output(output),
        )
    return FrameworkMessage(
        role=MessageRole.ASSISTANT, framework_item=framework_item, tool_call_id=call_id
    )


def _project_image_tool_output(
    item: Mapping[str, Any],
    framework_item: str,
    tool_names_by_call_id: Mapping[str, str],
    recorded_tool_call: Callable[[str], AgentToolCall | None],
) -> FrameworkMessage:
    call_id = str(item.get("call_id") or "")
    record = recorded_tool_call(call_id)
    if record is None:
        raise RuntimeError(f"tool call {call_id} returned images but has no record in this turn")
    texts = [
        part["text"]
        for part in item["output"]
        if isinstance(part, Mapping)
        and part.get("type") == "input_text"
        and isinstance(part.get("text"), str)
    ]
    return FrameworkMessage(
        role=MessageRole.TOOL,
        framework_item=framework_item,
        tool_call_id=call_id,
        tool_name=tool_names_by_call_id.get(call_id),
        tool_output="".join(texts),
        images=record.images,
    )


def _message_text(item: Mapping[str, Any]) -> str | None:
    content = item.get("content")
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return None
    texts: list[str] = []
    for part in content:
        if not isinstance(part, Mapping):
            continue
        if part.get("type") in _TEXT_PART_TYPES and isinstance(part.get("text"), str):
            texts.append(part["text"])
        elif part.get("type") == "refusal" and isinstance(part.get("refusal"), str):
            texts.append(part["refusal"])
    return "".join(texts) if texts else None


def _dump_model(value: Any) -> Any:
    if isinstance(value, BaseModel):
        return value.model_dump(mode="json")
    raise TypeError(f"{type(value).__name__} is not JSON serializable")
