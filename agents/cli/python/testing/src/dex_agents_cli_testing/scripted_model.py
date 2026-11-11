# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The scripted model behind every model API stub: it obeys the latest user message.

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers ``history: `` and the earlier prompts joined with `` | ``.
* ``model`` answers ``model: <the request's model>``.
* ``prompt`` answers ``prompt: <the request's instructions>``.
* ``call <tool> <json>`` calls one tool; join calls with `` && `` for one reply
  with several. A tool the request does not offer answers ``unbound <tool>``.
  Once the calls have results, it answers ``<tool>: <result>`` joined with ``; ``.
* anything else answers ``echo: <text>``.
"""

from __future__ import annotations

import json
import uuid
from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class ScriptedToolResult:
    """One tool result after the latest prompt.

    Attributes:
        tool_name: The tool the result answers.
        output: The result as text.
    """

    tool_name: str
    output: str


@dataclass(frozen=True)
class ScriptedConversation:
    """What a stub read from one model request.

    Attributes:
        model: The request's model.
        instructions: The system instructions, ``""`` without any.
        prompts: Every user prompt, oldest first; tool results are not prompts.
        tool_results: The tool results after the latest prompt.
        tool_names: The offered tool names.
    """

    model: str
    instructions: str
    prompts: Sequence[str]
    tool_results: Sequence[ScriptedToolResult]
    tool_names: Sequence[str]


@dataclass(frozen=True)
class ScriptedToolCall:
    """One tool call of a scripted reply.

    Attributes:
        call_id: A fresh tool call ID.
        tool_name: The offered tool's name.
        arguments: The call's arguments.
    """

    call_id: str
    tool_name: str
    arguments: dict[str, Any]


@dataclass(frozen=True)
class ScriptedReply:
    """The scripted model's reply: text, tool calls, or both.

    Attributes:
        text: The answer text, or ``None``.
        tool_calls: The tool calls, in order.
    """

    text: str | None = None
    tool_calls: Sequence[ScriptedToolCall] = field(default_factory=tuple)


def scripted_reply(conversation: ScriptedConversation) -> ScriptedReply:
    """Return the scripted model's reply to one request.

    Args:
        conversation: The request as the stub read it.

    Returns:
        The reply.
    """
    command = conversation.prompts[-1].strip() if conversation.prompts else ""
    if command.startswith("say "):
        return ScriptedReply(text=f"said: {command.removeprefix('say ')}")
    if command == "recall":
        return ScriptedReply(text="history: " + " | ".join(conversation.prompts[:-1]))
    if command == "model":
        return ScriptedReply(text=f"model: {conversation.model}")
    if command == "prompt":
        return ScriptedReply(text=f"prompt: {conversation.instructions}")
    if command.startswith("call "):
        return _tool_reply(command, conversation)
    return ScriptedReply(text=f"echo: {command}")


def _tool_reply(command: str, conversation: ScriptedConversation) -> ScriptedReply:
    if conversation.tool_results:
        return ScriptedReply(
            text="; ".join(
                f"{result.tool_name}: {result.output}" for result in conversation.tool_results
            )
        )
    calls: list[ScriptedToolCall] = []
    for call in command.split(" && "):
        _, _, rest = call.strip().partition(" ")
        tool_name, _, arguments = rest.partition(" ")
        resolved = _resolve_tool_name(tool_name, conversation.tool_names)
        if resolved is None:
            return ScriptedReply(text=f"unbound {tool_name}")
        calls.append(
            ScriptedToolCall(
                call_id=f"call_{uuid.uuid4().hex[:16]}",
                tool_name=resolved,
                arguments=json.loads(arguments.strip() or "{}"),
            )
        )
    return ScriptedReply(tool_calls=tuple(calls))


def _resolve_tool_name(tool_name: str, tool_names: Sequence[str]) -> str | None:
    for candidate in tool_names:
        if candidate == tool_name or candidate.endswith(f"__{tool_name}"):
            return candidate
    return None
