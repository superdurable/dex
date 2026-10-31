# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Store Pydantic AI messages as session messages and replay them as model history."""

from __future__ import annotations

from collections.abc import Sequence

from pydantic_ai import (
    ModelMessage,
    ModelMessagesTypeAdapter,
    ModelRequest,
    ModelResponse,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.messages import UserContent

from dex_agents import AgentMessage, FrameworkMessage, MessageRole


def build_user_prompt_message(text: str) -> FrameworkMessage:
    request = ModelRequest(parts=[UserPromptPart(text)])
    return FrameworkMessage(
        role=MessageRole.USER,
        text=text,
        framework_item=serialize_model_message(request),
    )


def build_turn_messages(
    turn_messages: Sequence[ModelMessage],
    user_prompt: str | Sequence[UserContent],
) -> list[FrameworkMessage]:
    # The session already stored the user prompt; keep the run's request only when it adds parts.
    if turn_messages and _is_plain_user_prompt_request(turn_messages[0], user_prompt):
        turn_messages = turn_messages[1:]
    return [project_model_message(message) for message in turn_messages]


def project_model_message(message: ModelMessage) -> FrameworkMessage:
    framework_item = serialize_model_message(message)
    if isinstance(message, ModelResponse):
        texts = [part.content for part in message.parts if isinstance(part, TextPart)]
        tool_calls = [part for part in message.parts if isinstance(part, ToolCallPart)]
        tool_call = tool_calls[0] if len(tool_calls) == 1 else None
        return FrameworkMessage(
            role=MessageRole.ASSISTANT,
            framework_item=framework_item,
            text="".join(texts) if texts else None,
            tool_call_id=tool_call.tool_call_id if tool_call else None,
            tool_name=tool_call.tool_name if tool_call else None,
            tool_arguments=tool_call.args_as_json_str() if tool_call else None,
        )
    tool_returns = [part for part in message.parts if isinstance(part, ToolReturnPart)]
    tool_return = tool_returns[0] if len(tool_returns) == 1 else None
    return FrameworkMessage(
        role=MessageRole.TOOL if tool_returns else MessageRole.USER,
        framework_item=framework_item,
        tool_call_id=tool_return.tool_call_id if tool_return else None,
        tool_name=tool_return.tool_name if tool_return else None,
        tool_output=tool_return.model_response_str(wrap_if_error=False) if tool_return else None,
    )


def rebuild_earlier_turn_history(
    messages: Sequence[AgentMessage],
    current_turn_id: str,
) -> list[ModelMessage]:
    history: list[ModelMessage] = []
    for index, message in enumerate(messages):
        if message.turn_id == current_turn_id or _is_superseded_user_prompt(messages, index):
            continue
        history.extend(deserialize_framework_item(message.framework_item))
    return history


def read_turn_user_prompt(
    messages: Sequence[AgentMessage],
    turn_id: str,
) -> str | Sequence[UserContent]:
    opening = next((message for message in messages if message.turn_id == turn_id), None)
    if opening is None or opening.role is not MessageRole.USER:
        raise RuntimeError(f"turn {turn_id} has no stored user message")
    for model_message in deserialize_framework_item(opening.framework_item):
        for part in model_message.parts:
            if isinstance(part, UserPromptPart):
                return part.content
    raise RuntimeError(f"the user message of turn {turn_id} has no user prompt part")


def serialize_model_message(message: ModelMessage) -> str:
    return ModelMessagesTypeAdapter.dump_json([message]).decode()


def deserialize_framework_item(framework_item: str) -> list[ModelMessage]:
    return ModelMessagesTypeAdapter.validate_json(framework_item)


def _is_superseded_user_prompt(messages: Sequence[AgentMessage], index: int) -> bool:
    # The run's opening request, stored next and repeating the prompt, replaces it in replay.
    message = messages[index]
    if message.role is not MessageRole.USER or index + 1 >= len(messages):
        return False
    following = messages[index + 1]
    if following.turn_id != message.turn_id or following.role is not MessageRole.USER:
        return False
    user_prompts = _user_prompt_contents(message.framework_item)
    return bool(user_prompts) and user_prompts[0] in _user_prompt_contents(following.framework_item)


def _user_prompt_contents(framework_item: str) -> list[str | Sequence[UserContent]]:
    return [
        part.content
        for model_message in deserialize_framework_item(framework_item)
        if isinstance(model_message, ModelRequest)
        for part in model_message.parts
        if isinstance(part, UserPromptPart)
    ]


def _is_plain_user_prompt_request(
    message: ModelMessage,
    user_prompt: str | Sequence[UserContent],
) -> bool:
    if not isinstance(message, ModelRequest) or len(message.parts) != 1:
        return False
    part = message.parts[0]
    return isinstance(part, UserPromptPart) and part.content == user_prompt
