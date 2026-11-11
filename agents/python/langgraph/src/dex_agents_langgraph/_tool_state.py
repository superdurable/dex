# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Graph state that travels into a tool Step and the state writes a tool Step returns.

A tool that reads graph state, such as one with a ``ToolRuntime`` parameter,
receives the state of its deferral through the call's ``tool_source_reference``,
which only tool sources use otherwise. The state writes of a tool, from a
returned ``Command`` or from LangGraph's task send, follow its output text,
because a tool Step stores nothing else that the next ``RunModel`` reads. Both
are encoded with the runner's checkpoint serializer.
"""

from __future__ import annotations

import base64
import json
import re
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from langchain_core.messages import ToolMessage
from langgraph._internal._typing import MISSING
from langgraph.channels.base import BaseChannel
from langgraph.checkpoint.serde.base import SerializerProtocol
from langgraph.types import Command

#: One write to a graph state channel: the channel name and the value.
StateWrite = tuple[str, Any]

# The key of the tool_source_reference JSON object that carries a graph tool's state.
_TOOL_STATE_REFERENCE_KEY = "dex_agents_langgraph_tool_state"
_TOOL_STATE_FORMAT = "v1"

_STATE_WRITES_TAG = "dex-agents-langgraph-state-writes"
_STATE_WRITES_BLOCK = re.compile(
    rf'\n\n<{_STATE_WRITES_TAG} format="v1" tool-call-id=(?P<tool_call_id>"(?:[^"\\]|\\.)*") '
    rf'type="(?P<type>[^"]*)">(?P<data>[A-Za-z0-9+/=]*)</{_STATE_WRITES_TAG}>$'
)
_ANY_STATE_WRITES_BLOCK = re.compile(
    rf'\n\n<{_STATE_WRITES_TAG} format="v1" tool-call-id="(?:[^"\\]|\\.)*" type="[^"]*">'
    rf"[A-Za-z0-9+/=]*</{_STATE_WRITES_TAG}>"
)


@dataclass(frozen=True, kw_only=True)
class EncodedStateWrites:
    """Recorded state writes, still serialized: the serializer's type and base64 bytes."""

    type: str
    data: str


@dataclass(frozen=True, kw_only=True)
class ToolCommandResult:
    """What a tool's ``Command`` holds: its tool message and its other state writes."""

    tool_message: ToolMessage
    state_writes: list[StateWrite]


def encode_tool_state_reference(state: Mapping[str, Any], serializer: SerializerProtocol) -> str:
    """Return the ``tool_source_reference`` that carries ``state`` into a tool Step."""
    typed = serializer.dumps_typed(dict(state))
    return json.dumps(
        {
            _TOOL_STATE_REFERENCE_KEY: {
                "format": _TOOL_STATE_FORMAT,
                "type": typed[0],
                "data": base64.b64encode(typed[1]).decode("ascii"),
            }
        },
        separators=(",", ":"),
    )


def decode_tool_state_reference(
    tool_source_reference: str | None, serializer: SerializerProtocol
) -> dict[str, Any] | None:
    """Return the state a ``tool_source_reference`` carries, or ``None`` when it carries none."""
    if tool_source_reference is None:
        return None
    try:
        document = json.loads(tool_source_reference)
    except ValueError:
        return None
    carried = document.get(_TOOL_STATE_REFERENCE_KEY) if isinstance(document, dict) else None
    if not isinstance(carried, dict) or carried.get("format") != _TOOL_STATE_FORMAT:
        return None
    state = serializer.loads_typed((str(carried["type"]), base64.b64decode(carried["data"])))
    if not isinstance(state, dict):
        raise ValueError("the tool call carries graph state that is not a mapping")
    return state


def build_graph_state_reader(
    channels: Mapping[str, Any],
    state: Mapping[str, Any],
    state_writes: Sequence[StateWrite],
) -> Callable[..., Any]:
    """Return a ``CONFIG_KEY_READ`` function over ``state``, as a LangGraph task reads its state.

    With ``fresh``, a channel's value includes the writes sent so far through its reducer.
    """

    def read_channel(channel_name: str, is_fresh: bool) -> Any:
        pending = [value for name, value in state_writes if name == channel_name]
        spec = channels.get(channel_name)
        if not is_fresh or not pending or not isinstance(spec, BaseChannel):
            return state.get(channel_name)
        channel = spec.from_checkpoint(state.get(channel_name, MISSING))
        channel.update(pending)
        return channel.get() if channel.is_available() else None

    def read(select: str | Sequence[str], fresh: bool = False) -> Any:
        if isinstance(select, str):
            return read_channel(select, fresh)
        return {channel_name: read_channel(channel_name, fresh) for channel_name in select}

    return read


def split_tool_command(
    command: Command[Any], tool_call_id: str, messages_key: str
) -> ToolCommandResult:
    """Split a tool's ``Command`` into its tool message and its other state writes.

    Raises:
        TypeError: If the command routes the graph, updates state with something
            other than a mapping, or has no tool message for the call.
    """
    if command.goto or command.graph is not None or command.resume is not None:
        raise TypeError(
            "a tool of an agent session may return a Command with update= only; goto, "
            "graph, and resume cannot run outside the graph"
        )
    if not isinstance(command.update, Mapping):
        raise TypeError("a tool's Command must update state with a mapping")
    update = dict(command.update)
    messages = update.pop(messages_key, [])
    message_list: list[Any] = list(messages) if isinstance(messages, list | tuple) else [messages]
    for index, message in enumerate(message_list):
        if isinstance(message, ToolMessage) and message.tool_call_id == tool_call_id:
            other_messages = message_list[:index] + message_list[index + 1 :]
            state_writes: list[StateWrite] = list(update.items())
            if other_messages:
                # The tool message follows them, as the graph returns it after the task's writes.
                state_writes.append((messages_key, other_messages))
            return ToolCommandResult(tool_message=message, state_writes=state_writes)
    raise TypeError(
        f"the tool's Command has no ToolMessage for tool call {tool_call_id} under "
        f"{messages_key!r}"
    )


def append_state_writes(
    output: str,
    state_writes: Sequence[StateWrite],
    tool_call_id: str,
    serializer: SerializerProtocol,
) -> str:
    """Return ``output`` followed by the block that records the tool's state writes."""
    typed = serializer.dumps_typed([[channel, value] for channel, value in state_writes])
    data = base64.b64encode(typed[1]).decode("ascii")
    return (
        f'{output}\n\n<{_STATE_WRITES_TAG} format="v1" tool-call-id={json.dumps(tool_call_id)} '
        f'type="{typed[0]}">{data}</{_STATE_WRITES_TAG}>'
    )


def split_recorded_output(output: str, tool_call_id: str) -> tuple[str, EncodedStateWrites | None]:
    """Return the output text and the state writes block that ends it for this call, if any."""
    match = _STATE_WRITES_BLOCK.search(output)
    if match is None or json.loads(match["tool_call_id"]) != tool_call_id:
        return output, None
    return output[: match.start()], EncodedStateWrites(type=match["type"], data=match["data"])


def remove_state_writes_blocks(text: str) -> str:
    """Return ``text`` without any state writes block, as in a note about tool calls."""
    return _ANY_STATE_WRITES_BLOCK.sub("", text)


def decode_state_writes(
    encoded: EncodedStateWrites, serializer: SerializerProtocol
) -> list[StateWrite]:
    """Return the recorded state writes in the order the tool made them."""
    recorded = serializer.loads_typed((encoded.type, base64.b64decode(encoded.data)))
    return [(str(channel), value) for channel, value in recorded]
