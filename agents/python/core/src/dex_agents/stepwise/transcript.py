# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""How Stepwise stores model input items as session messages, and reads them back."""

from __future__ import annotations

import json
from collections.abc import Iterable, Sequence
from typing import Any

from dex_agents.runner import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    FrameworkMessage,
)
from dex_agents.stepwise.model import (
    ModelInputItem,
    ModelOutputItem,
    ModelToolCall,
    ToolResultItem,
    UserTextItem,
)
from dex_agents.values import AgentMessage, MessageRole

#: ``framework_item`` ``type`` of stored user text.
USER_TEXT_ITEM_TYPE = "user_text"
#: ``framework_item`` ``type`` of a stored model output item.
MODEL_OUTPUT_ITEM_TYPE = "model_output"
#: ``framework_item`` ``type`` of a stored tool result.
TOOL_RESULT_ITEM_TYPE = "tool_result"


def encode_stepwise_message(item: ModelInputItem) -> FrameworkMessage:
    """Return the session message that stores one model input item.

    The ``framework_item`` is a JSON object with a ``type`` of ``user_text``,
    ``model_output``, or ``tool_result``. A model output item keeps the
    provider's item as a JSON string, so its bytes survive storage.

    Args:
        item: User text, a model output item, or a tool result.

    Returns:
        The message, with display fields set.
    """
    if isinstance(item, UserTextItem):
        return FrameworkMessage(
            role=MessageRole.USER,
            text=item.text,
            framework_item=_compact_json({"type": USER_TEXT_ITEM_TYPE, "text": item.text}),
        )
    if isinstance(item, ToolResultItem):
        return FrameworkMessage(
            role=MessageRole.TOOL,
            tool_call_id=item.tool_call_id,
            tool_name=item.tool_name,
            tool_output=item.output,
            framework_item=_compact_json(
                {
                    "type": TOOL_RESULT_ITEM_TYPE,
                    "tool_call_id": item.tool_call_id,
                    "tool_name": item.tool_name,
                    "output": item.output,
                }
            ),
        )
    single_call = item.tool_calls[0] if len(item.tool_calls) == 1 else None
    return FrameworkMessage(
        role=MessageRole.ASSISTANT,
        text=item.text,
        tool_call_id=single_call.tool_call_id if single_call else None,
        tool_name=single_call.tool_name if single_call else None,
        tool_arguments=single_call.arguments if single_call else None,
        framework_item=_compact_json(
            {
                "type": MODEL_OUTPUT_ITEM_TYPE,
                "provider": item.provider,
                "api": item.api,
                "item": item.item,
                "text": item.text,
                "tool_calls": [
                    {
                        "tool_call_id": call.tool_call_id,
                        "tool_name": call.tool_name,
                        "arguments": call.arguments,
                    }
                    for call in item.tool_calls
                ],
            }
        ),
    )


def decode_stepwise_message(message: AgentMessage) -> ModelInputItem:
    """Return the model input item that one stored session message holds.

    Args:
        message: A message stored by a Stepwise session.

    Returns:
        The item, with a model output item's provider JSON unchanged.

    Raises:
        ValueError: If the message was not stored by Stepwise.
    """
    try:
        stored: dict[str, Any] = json.loads(message.framework_item)
        item_type = stored["type"]
        if item_type == USER_TEXT_ITEM_TYPE:
            return UserTextItem(text=stored["text"])
        if item_type == TOOL_RESULT_ITEM_TYPE:
            return ToolResultItem(
                tool_call_id=stored["tool_call_id"],
                tool_name=stored["tool_name"],
                output=stored["output"],
            )
        if item_type == MODEL_OUTPUT_ITEM_TYPE:
            return ModelOutputItem(
                provider=stored["provider"],
                api=stored["api"],
                item=stored["item"],
                text=stored.get("text"),
                tool_calls=tuple(
                    ModelToolCall(
                        tool_call_id=call["tool_call_id"],
                        tool_name=call["tool_name"],
                        arguments=call["arguments"],
                    )
                    for call in stored.get("tool_calls", [])
                ),
            )
    except (json.JSONDecodeError, KeyError, TypeError) as error:
        raise ValueError(f"message {message.sequence} is not a Stepwise item: {error}") from None
    raise ValueError(f"message {message.sequence} has unknown Stepwise item type {item_type!r}")


def pair_tool_calls_with_results(items: Iterable[ModelInputItem]) -> list[ModelInputItem]:
    """Return the items with exactly one result after every tool call.

    A tool call without a stored result, such as one whose turn failed before
    the result was stored, gets the outcome-unknown text as its result, placed
    before the next user text. A result whose call is not in the items is
    dropped. Providers reject either kind of orphan.

    Args:
        items: Items in conversation order.

    Returns:
        A new list that every provider accepts.
    """
    paired: list[ModelInputItem] = []
    open_calls: dict[str, ModelToolCall] = {}
    for item in items:
        if isinstance(item, ModelOutputItem):
            paired.append(item)
            open_calls.update((call.tool_call_id, call) for call in item.tool_calls)
        elif isinstance(item, ToolResultItem):
            if open_calls.pop(item.tool_call_id, None) is not None:
                paired.append(item)
        else:
            paired.extend(_outcome_unknown_results(open_calls.values()))
            open_calls.clear()
            paired.append(item)
    paired.extend(_outcome_unknown_results(open_calls.values()))
    return paired


def _outcome_unknown_results(calls: Iterable[ModelToolCall]) -> Sequence[ToolResultItem]:
    return [
        ToolResultItem(
            tool_call_id=call.tool_call_id,
            tool_name=call.tool_name,
            output=OUTCOME_UNKNOWN_TOOL_RESULT,
        )
        for call in calls
    ]


def _compact_json(value: dict[str, Any]) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))
