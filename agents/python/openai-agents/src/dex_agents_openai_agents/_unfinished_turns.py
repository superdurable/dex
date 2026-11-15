# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The items of a turn that ends before its final answer, read from the serialized ``RunState``."""

from __future__ import annotations

import json
from collections.abc import Callable, Mapping
from typing import Any

from agents import ItemHelpers
from openai.types.responses import ResponseFunctionToolCall

from dex_agents import AgentToolCall, AgentTurnContext

# Local tool calls that the SDK pairs with one output item; only function calls can be pending here.
_PAIRED_LOCAL_CALL_OUTPUT_TYPES = {
    "computer_call": "computer_call_output",
    "local_shell_call": "local_shell_call_output",
    "shell_call": "shell_call_output",
    "apply_patch_call": "apply_patch_call_output",
    "custom_tool_call": "custom_tool_call_output",
}
# Items a model response may hold after its first function call.
_TRAILING_RESPONSE_ITEM_TYPES = frozenset({"function_call", "function_call_output", "reasoning"})

PendingToolOutput = Callable[[ResponseFunctionToolCall, AgentToolCall | None], Any]


def read_unfinished_turn_items(
    turn: AgentTurnContext, pending_tool_output: PendingToolOutput
) -> list[dict[str, Any]]:
    """Return the turn's input items from the ``RunState`` and answer every pending function call.

    It mirrors ``RunResult.to_input_list()`` on the serialized session items,
    without dropping pending calls. Each pending call gets the
    ``function_call_output`` that the SDK makes from ``pending_tool_output``.

    Raises:
        ValueError: If the run state cannot be read, a call would be left
            without a result, or a record has no call in the run state. The
            session then stores its note instead.
    """
    if turn.framework_run_state is None:
        if turn.tool_calls:
            raise ValueError(f"turn {turn.turn_id} has tool call records but no run state")
        return []
    document = json.loads(turn.framework_run_state)
    if not isinstance(document, dict) or not isinstance(document.get("$schemaVersion"), str):
        raise ValueError("the framework run state is not a serialized OpenAI Agents SDK RunState")
    if document.get("nested_history_owned_session_item_refs"):
        # to_input_list leaves out items that nested handoff history owns; that is not mirrored.
        raise ValueError("the RunState holds nested handoff history")
    run_items = document.get("session_items", document.get("generated_items"))
    if not isinstance(run_items, list):
        raise ValueError(f"RunState schema {document['$schemaVersion']} has no session items")
    reasoning_item_id_policy = document.get("reasoning_item_id_policy")
    items = [
        item
        for index, run_item in enumerate(run_items)
        if (item := _to_input_item(index, run_item, reasoning_item_id_policy)) is not None
    ]
    function_call_ids = _unique_call_ids(items, "function_call")
    output_call_ids = _unique_call_ids(items, "function_call_output")
    _require_local_call_outputs(items)
    pending_indexes = [
        index
        for index, item in enumerate(items)
        if item.get("type") == "function_call" and item["call_id"] not in output_call_ids
    ]
    if pending_indexes and any(
        item.get("type") not in _TRAILING_RESPONSE_ITEM_TYPES
        for item in items[pending_indexes[0] :]
    ):
        raise ValueError(
            f"turn {turn.turn_id} has a pending function call before the last model response"
        )
    if missing := sorted(output_call_ids - function_call_ids):
        raise ValueError(f"turn {turn.turn_id} has results of tool calls {missing} without calls")
    if missing := sorted(set(turn.tool_calls) - function_call_ids):
        raise ValueError(f"tool calls {missing} of turn {turn.turn_id} are not in the run state")
    stored_call_ids = {
        message.tool_call_id for message in turn.messages if message.turn_id == turn.turn_id
    }
    if stored := sorted(function_call_ids & stored_call_ids):
        raise ValueError(f"tool calls {stored} of turn {turn.turn_id} are already stored")
    pending_outputs: list[dict[str, Any]] = []
    for index in pending_indexes:
        function_call = ResponseFunctionToolCall.model_validate(items[index])
        output_item = ItemHelpers.tool_call_output_item(
            function_call,
            pending_tool_output(function_call, turn.recorded_tool_call(function_call.call_id)),
        )
        pending_outputs.append(dict(output_item))
    return [*items, *pending_outputs]


def _to_input_item(
    index: int, run_item: Any, reasoning_item_id_policy: Any
) -> dict[str, Any] | None:
    """Mirror the SDK's ``run_item_to_input_item`` on one serialized run item."""
    if not isinstance(run_item, Mapping) or not isinstance(run_item.get("type"), str):
        raise ValueError(f"session item {index} of the RunState has no type")
    item_type = run_item["type"]
    if item_type == "tool_approval_item":
        return None
    if item_type == "input_item":
        raise ValueError("the RunState holds input admitted while resuming")
    raw_item = run_item.get("raw_item")
    if not isinstance(raw_item, Mapping):
        raise ValueError(f"session item {index} of the RunState has no raw_item")
    item = dict(raw_item)
    if item_type != "compaction_item":
        item.pop("created_by", None)
    if item.get("type") == "shell_call_output":
        for key in ("status", "shell_output", "provider_data"):
            item.pop(key, None)
        if isinstance(item.get("output"), list):
            item["output"] = [
                {key: value for key, value in chunk.items() if key != "created_by"}
                if isinstance(chunk, dict)
                else chunk
                for chunk in item["output"]
            ]
    if "status" in item and item["status"] is None:
        del item["status"]
    if item_type == "reasoning_item" and reasoning_item_id_policy == "omit":
        item.pop("id", None)
    return item


def _unique_call_ids(items: list[dict[str, Any]], item_type: str) -> set[str]:
    call_ids: set[str] = set()
    for item in items:
        if item.get("type") != item_type:
            continue
        call_id = item.get("call_id")
        if not isinstance(call_id, str) or not call_id:
            raise ValueError(f"a {item_type} item of the RunState has no call_id")
        if call_id in call_ids:
            raise ValueError(f"the RunState has more than one {item_type} with call ID {call_id}")
        call_ids.add(call_id)
    return call_ids


def _require_local_call_outputs(items: list[dict[str, Any]]) -> None:
    output_keys = {(item.get("type"), item.get("call_id")) for item in items}
    for item in items:
        output_type = _PAIRED_LOCAL_CALL_OUTPUT_TYPES.get(str(item.get("type")))
        # The provider runs a hosted shell call; it has no local output until it completes.
        is_pending_hosted_shell_call = item.get("type") == "shell_call" and item.get("status") in (
            None,
            "in_progress",
        )
        if (
            output_type is not None
            and not is_pending_hosted_shell_call
            and (output_type, item.get("call_id")) not in output_keys
        ):
            raise ValueError(
                f"{item.get('type')} {item.get('call_id')} has no output in the RunState"
            )
