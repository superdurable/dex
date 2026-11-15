# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The items of a turn that ends before its final answer, read from the serialized ``RunState``."""

from __future__ import annotations

import importlib
import json
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Any

from agents import Agent, ItemHelpers
from openai.types.responses import ResponseFunctionToolCall

from dex_agents import AgentToolCall, AgentTurnContext

# The RunState schema versions whose items this reader mirrors; the tests pin the SDK's current one.
_SUPPORTED_SCHEMA_VERSIONS = frozenset({"1.21"})
# The run item types this reader mirrors, with the raw item types each may hold.
_RAW_ITEM_TYPES_BY_RUN_ITEM_TYPE = {
    "message_output_item": frozenset({"message"}),
    "tool_call_item": frozenset({"function_call", "web_search_call", "file_search_call"}),
    "tool_call_output_item": frozenset({"function_call_output"}),
    "reasoning_item": frozenset({"reasoning"}),
    "handoff_call_item": frozenset({"function_call"}),
    "handoff_output_item": frozenset({"function_call_output"}),
    "compaction_item": frozenset({"compaction"}),
    "tool_approval_item": frozenset({"function_call"}),
}

#: Returns the output the resumed run would give the model for a pending call's record.
PendingToolOutput = Callable[[ResponseFunctionToolCall, AgentToolCall | None], Any]


def qualified_tool_name(function_call: Mapping[str, Any]) -> str:
    """Return the name a record holds for a function call: ``<namespace>.<name>`` or the name."""
    name = str(function_call.get("name") or "")
    namespace = function_call.get("namespace")
    return f"{namespace}.{name}" if namespace and namespace != name else name


def read_unfinished_turn_items(
    turn: AgentTurnContext,
    starting_agent: Agent[Any],
    pending_tool_output: PendingToolOutput,
) -> list[dict[str, Any]]:
    """Return the turn's input items from the ``RunState`` and answer every pending function call.

    It restores the session items with the SDK's own deserializer, as
    ``RunState.from_json`` does, and converts them as ``to_input_list()``
    does, without dropping pending calls. It accepts only run items it can
    mirror exactly. Each pending call gets the ``function_call_output`` that
    the SDK makes from ``pending_tool_output``.

    Raises:
        ValueError: If the run state has an unknown schema or item, its calls
            and results are not in order, or a record names another tool or
            has no call. The session then stores its note instead.
    """
    if turn.framework_run_state is None:
        if turn.tool_calls:
            raise ValueError(f"turn {turn.turn_id} has tool call records but no run state")
        return []
    document = json.loads(turn.framework_run_state)
    if not isinstance(document, dict) or not isinstance(document.get("$schemaVersion"), str):
        raise ValueError("the framework run state is not a serialized OpenAI Agents SDK RunState")
    schema_version = document["$schemaVersion"]
    if schema_version not in _SUPPORTED_SCHEMA_VERSIONS:
        raise ValueError(f"RunState schema {schema_version} is not one this runner reads")
    if document.get("nested_history_owned_session_item_refs"):
        # to_input_list leaves out items that nested handoff history owns; that is not mirrored.
        raise ValueError("the RunState holds nested handoff history")
    session_items = document.get("session_items")
    if not isinstance(session_items, list):
        raise ValueError(f"RunState schema {schema_version} has no session items")
    for index, session_item in enumerate(session_items):
        _require_mirrored_run_item(index, session_item)
    sdk = _load_sdk_run_item_functions()
    run_items = sdk.deserialize_items(
        session_items,
        sdk.build_agent_map(starting_agent),
        agent_identity_map=sdk.build_agent_identity_map(starting_agent),
    )
    if len(run_items) != len(session_items):
        # The deserializer skips an item whose agent is not in the graph.
        raise ValueError("the RunState holds items of agents outside the agent graph")
    reasoning_item_id_policy = document.get("reasoning_item_id_policy")
    items: list[dict[str, Any]] = []
    for run_item in run_items:
        input_item = sdk.run_item_to_input_item(run_item, reasoning_item_id_policy)
        if input_item is not None:
            items.append(dict(input_item))
    function_calls, pending_calls = _pair_calls_with_results(turn, items)
    for tool_call_id, record in turn.tool_calls.items():
        function_call = function_calls.get(tool_call_id)
        if function_call is None:
            raise ValueError(
                f"tool call {tool_call_id} of turn {turn.turn_id} is not in the run state"
            )
        if record.tool_name != qualified_tool_name(function_call):
            raise ValueError(
                f"tool call {tool_call_id} of turn {turn.turn_id} is recorded for tool "
                f"{record.tool_name}"
            )
    stored_call_ids = {
        message.tool_call_id for message in turn.messages if message.turn_id == turn.turn_id
    }
    if stored := sorted(function_calls.keys() & stored_call_ids):
        raise ValueError(f"tool calls {stored} of turn {turn.turn_id} are already stored")
    pending_outputs: list[dict[str, Any]] = []
    for item in pending_calls:
        pending_call = ResponseFunctionToolCall.model_validate(item)
        output_item = ItemHelpers.tool_call_output_item(
            pending_call,
            pending_tool_output(pending_call, turn.recorded_tool_call(pending_call.call_id)),
        )
        pending_outputs.append(dict(output_item))
    return [*items, *pending_outputs]


@dataclass(frozen=True)
class _SdkRunItemFunctions:
    deserialize_items: Callable[..., list[Any]]
    build_agent_map: Callable[[Agent[Any]], dict[str, Agent[Any]]]
    build_agent_identity_map: Callable[[Agent[Any]], dict[str, Agent[Any]]]
    run_item_to_input_item: Callable[[Any, Any], Any]


def _load_sdk_run_item_functions() -> _SdkRunItemFunctions:
    """Import the SDK's private RunState item functions only when a turn ends unfinished.

    Raises:
        ValueError: If this SDK release lacks them; the adapter still imports.
    """
    try:
        run_state = importlib.import_module("agents.run_state")
        agent_identity = importlib.import_module("agents._run_state_agent_identity")
        run_items = importlib.import_module("agents.run_internal.items")
        return _SdkRunItemFunctions(
            deserialize_items=run_state._deserialize_items,
            build_agent_map=agent_identity._build_agent_map,
            build_agent_identity_map=agent_identity._build_agent_identity_map,
            run_item_to_input_item=run_items.run_item_to_input_item,
        )
    except (ImportError, AttributeError) as error:
        raise ValueError(
            "this openai-agents release lacks the RunState item functions the runner reads"
        ) from error


def _require_mirrored_run_item(index: int, run_item: Any) -> None:
    if not isinstance(run_item, Mapping) or not isinstance(run_item.get("type"), str):
        raise ValueError(f"session item {index} of the RunState has no type")
    item_type = run_item["type"]
    raw_item = run_item.get("raw_item")
    if not isinstance(raw_item, Mapping):
        raise ValueError(f"session item {index} of the RunState has no raw_item")
    raw_item_types = _RAW_ITEM_TYPES_BY_RUN_ITEM_TYPE.get(item_type)
    if raw_item_types is None or raw_item.get("type") not in raw_item_types:
        raise ValueError(
            f"session item {index} of the RunState is a {item_type} of {raw_item.get('type')}"
        )
    if "caller" in raw_item:
        # A programmatic tool call belongs to its program, whose pairing rules are not mirrored.
        raise ValueError(f"session item {index} of the RunState belongs to a program")


def _pair_calls_with_results(
    turn: AgentTurnContext, items: list[dict[str, Any]]
) -> tuple[dict[str, dict[str, Any]], list[dict[str, Any]]]:
    """Check that each call's result follows its model response and comes once, in item order.

    Returns:
        Every function call by call ID, and the calls of the last model
        response without a result, in order.
    """
    function_calls: dict[str, dict[str, Any]] = {}
    open_calls: dict[str, dict[str, Any]] = {}
    has_result_since_last_call = False
    for item in items:
        item_type = item.get("type")
        if item_type == "function_call":
            call_id = _call_id(item)
            if call_id in function_calls:
                raise ValueError(f"the RunState of turn {turn.turn_id} repeats tool call {call_id}")
            if open_calls and has_result_since_last_call:
                raise ValueError(
                    f"tool call {call_id} of turn {turn.turn_id} starts before earlier calls "
                    "have results"
                )
            function_calls[call_id] = item
            open_calls[call_id] = item
            has_result_since_last_call = False
        elif item_type == "function_call_output":
            call_id = _call_id(item)
            if open_calls.pop(call_id, None) is None:
                raise ValueError(
                    f"the result of tool call {call_id} of turn {turn.turn_id} does not follow "
                    "one open call"
                )
            has_result_since_last_call = True
        elif open_calls:
            raise ValueError(
                f"a {item_type} item of turn {turn.turn_id} comes before earlier calls have results"
            )
    return function_calls, list(open_calls.values())


def _call_id(item: Mapping[str, Any]) -> str:
    call_id = item.get("call_id")
    if not isinstance(call_id, str) or not call_id:
        raise ValueError(f"a {item.get('type')} item of the RunState has no call_id")
    return call_id
