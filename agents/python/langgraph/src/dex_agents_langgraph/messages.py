# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Conversion between LangChain messages and stored agent messages."""

from __future__ import annotations

import json
from collections.abc import Sequence
from typing import Any

from dex_agents import AgentMessage, FrameworkMessage, MessageRole
from langchain_core.messages import (
    AIMessage,
    BaseMessage,
    ChatMessage,
    HumanMessage,
    ToolMessage,
    message_to_dict,
    messages_from_dict,
)
from pydantic_core import to_jsonable_python


def langchain_message_to_framework_message(message: BaseMessage) -> FrameworkMessage:
    """Convert one LangChain message into the item an agent session stores.

    The runner calls it for every message a turn adds to the graph's message
    state. ``framework_item`` holds ``langchain_core.messages.message_to_dict``
    as JSON text, which ``framework_message_to_langchain_message`` reads back
    unchanged on the next turn. The other fields are a display projection:

    * ``HumanMessage`` and a ``ChatMessage`` with role ``"user"`` become
      ``MessageRole.USER`` with ``text``.
    * ``ToolMessage`` becomes ``MessageRole.TOOL`` with ``tool_call_id``,
      ``tool_name``, and ``tool_output``.
    * Every other message, including ``AIMessage`` and ``SystemMessage``,
      becomes ``MessageRole.ASSISTANT``. An ``AIMessage`` with exactly one tool
      call also sets ``tool_call_id``, ``tool_name``, and ``tool_arguments``.

    Values that JSON cannot represent directly, such as a ``datetime`` in
    ``response_metadata``, are stored in their JSON-compatible form, so they
    come back as strings or plain containers.

    Example:
        >>> stored = langchain_message_to_framework_message(AIMessage("Done", id="ai-1"))
        >>> stored.role, stored.text
        (<MessageRole.ASSISTANT: 'assistant'>, 'Done')

    Args:
        message: A message from the graph's message state.

    Returns:
        The message with its JSON ``framework_item`` and display fields.

    Raises:
        TypeError: If a value in the message has no JSON-compatible form.
    """
    framework_item = json.dumps(
        message_to_dict(message), ensure_ascii=False, default=to_jsonable_python
    )
    if isinstance(message, HumanMessage) or (
        isinstance(message, ChatMessage) and message.role == "user"
    ):
        return FrameworkMessage(
            role=MessageRole.USER, framework_item=framework_item, text=message.text
        )
    if isinstance(message, ToolMessage):
        return FrameworkMessage(
            role=MessageRole.TOOL,
            framework_item=framework_item,
            tool_call_id=message.tool_call_id,
            tool_name=message.name,
            tool_output=message.text,
        )
    text = message.text or None
    if isinstance(message, AIMessage) and len(message.tool_calls) == 1:
        tool_call = message.tool_calls[0]
        return FrameworkMessage(
            role=MessageRole.ASSISTANT,
            framework_item=framework_item,
            text=text,
            tool_call_id=tool_call["id"],
            tool_name=tool_call["name"],
            tool_arguments=json.dumps(
                tool_call["args"], ensure_ascii=False, default=to_jsonable_python
            ),
        )
    return FrameworkMessage(role=MessageRole.ASSISTANT, framework_item=framework_item, text=text)


def framework_message_to_langchain_message(message: AgentMessage) -> BaseMessage:
    """Rebuild the LangChain message that a stored agent message holds.

    The runner calls it for every stored message to rebuild the graph's input.
    A message stored without a LangChain message ID receives
    ``"dex-message-<sequence>"``, so later turns can tell stored messages from
    new ones.

    Example:
        >>> history = [framework_message_to_langchain_message(m) for m in turn.messages]

    Args:
        message: A message from ``AgentTurnContext.messages``.

    Returns:
        The LangChain message that ``langchain_message_to_framework_message`` stored.

    Raises:
        ValueError: If ``framework_item`` is not a stored LangChain message.
    """
    item: Any = json.loads(message.framework_item)
    if not isinstance(item, dict) or "type" not in item or "data" not in item:
        raise ValueError(
            f"agent message {message.sequence} does not hold a stored LangChain message"
        )
    restored = messages_from_dict([item])[0]
    if restored.id is None:
        restored.id = f"dex-message-{message.sequence}"
    return restored


def framework_messages_to_langchain_messages(
    messages: Sequence[AgentMessage],
) -> list[BaseMessage]:
    """Rebuild every stored message of a session, in sequence order.

    Args:
        messages: ``AgentTurnContext.messages``.

    Returns:
        LangChain messages in the same order.

    Raises:
        ValueError: If a ``framework_item`` is not a stored LangChain message.
    """
    return [framework_message_to_langchain_message(message) for message in messages]
