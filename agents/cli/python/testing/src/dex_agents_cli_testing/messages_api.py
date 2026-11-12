# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted local stand-in for the Anthropic Messages API.

``MessagesApiStub`` answers ``POST /v1/messages``, streaming or not, and
``POST /v1/messages/count_tokens``. It serves the Claude Code CLI with
``ANTHROPIC_BASE_URL=<base_url>`` and pi-ai with the same URL. Text blocks
that start with ``<``, such as Claude Code's system reminders, and the hidden
message a resumed Claude Code CLI sends after a deferred call are not prompts.
"""

from __future__ import annotations

import json
import uuid
from collections.abc import Iterator, Mapping, Sequence
from typing import Any

from dex_agents_cli_testing.model_api import ModelApiRequest, ModelApiResponse, ModelApiStub
from dex_agents_cli_testing.scripted_model import (
    ReplySource,
    ScriptedConversation,
    ScriptedReply,
    ScriptedToolResult,
    scripted_reply,
)

# The hidden message a resumed Claude Code CLI sends after a deferred call; never a command.
CONTINUE_PROMPT = "Continue from where you left off."
_USAGE = {
    "input_tokens": 10,
    "output_tokens": 5,
    "cache_read_input_tokens": 0,
    "cache_creation_input_tokens": 0,
}


class MessagesApiStub(ModelApiStub):
    """Serve the scripted model as the Anthropic Messages API."""

    def __init__(self, reply_source: ReplySource = scripted_reply) -> None:
        """Create the stub; ``reply_source`` picks each reply."""
        super().__init__()
        self._reply_source = reply_source

    def is_model_request(self, request: ModelApiRequest) -> bool:
        """Report whether a request is ``POST .../messages``."""
        return request.method == "POST" and request.path.split("?", 1)[0].endswith("/messages")

    def respond(self, request: ModelApiRequest) -> ModelApiResponse:
        """Answer a Messages API request with the scripted reply."""
        path = request.path.split("?", 1)[0]
        if request.method == "POST" and path.endswith("/messages/count_tokens"):
            return ModelApiResponse(body={"input_tokens": 10})
        if not self.is_model_request(request):
            return ModelApiResponse(
                404, body={"type": "error", "error": {"type": "not_found_error", "message": path}}
            )
        message = _message_object(
            request.body, self._reply_source(read_messages_conversation(request.body))
        )
        if request.body.get("stream"):
            return ModelApiResponse(events=_stream_events(message))
        return ModelApiResponse(body=message)


def read_messages_conversation(body: Mapping[str, Any]) -> ScriptedConversation:
    """Read the scripted model's view of a Messages API request body."""
    messages = [message for message in body.get("messages") or [] if isinstance(message, Mapping)]
    prompts: list[str] = []
    last_prompt_index = -1
    for index, message in enumerate(messages):
        prompt = _prompt_text(message)
        if prompt is not None:
            prompts.append(prompt)
            last_prompt_index = index
    call_names = {
        str(block.get("id")): str(block.get("name"))
        for message in messages
        for block in _blocks(message)
        if block.get("type") == "tool_use"
    }
    later_messages = messages[last_prompt_index + 1 :]
    results = [
        ScriptedToolResult(
            call_names.get(str(block.get("tool_use_id")), "unknown"),
            _block_text(block.get("content")),
        )
        for message in later_messages
        for block in _blocks(message)
        if block.get("type") == "tool_result"
    ]
    return ScriptedConversation(
        model=str(body.get("model") or ""),
        instructions=_system_text(body.get("system")),
        prompts=prompts,
        tool_results=results,
        tool_names=[
            str(tool.get("name")) for tool in body.get("tools") or [] if isinstance(tool, Mapping)
        ],
        reply_count=sum(1 for message in later_messages if message.get("role") == "assistant"),
    )


def _message_object(body: Mapping[str, Any], reply: ScriptedReply) -> dict[str, Any]:
    content: list[dict[str, Any]] = []
    if reply.text is not None:
        content.append({"type": "text", "text": reply.text})
    for call in reply.tool_calls:
        content.append(
            {
                "type": "tool_use",
                "id": f"toolu_{uuid.uuid4().hex[:20]}",
                "name": call.tool_name,
                "input": call.arguments,
            }
        )
    return {
        "id": f"msg_{uuid.uuid4().hex[:20]}",
        "type": "message",
        "role": "assistant",
        "model": str(body.get("model") or ""),
        "content": content,
        "stop_reason": "tool_use" if reply.tool_calls else "end_turn",
        "stop_sequence": None,
        "usage": _USAGE,
    }


def _stream_events(message: Mapping[str, Any]) -> Iterator[tuple[str, Mapping[str, Any]]]:
    def event(event_type: str, **fields: Any) -> tuple[str, Mapping[str, Any]]:
        return event_type, {"type": event_type, **fields}

    yield event("message_start", message={**message, "content": [], "stop_reason": None})
    blocks: Sequence[Mapping[str, Any]] = message["content"]
    for index, block in enumerate(blocks):
        if block["type"] == "text":
            yield event(
                "content_block_start", index=index, content_block={"type": "text", "text": ""}
            )
            yield event(
                "content_block_delta",
                index=index,
                delta={"type": "text_delta", "text": block["text"]},
            )
        else:
            yield event("content_block_start", index=index, content_block={**block, "input": {}})
            yield event(
                "content_block_delta",
                index=index,
                delta={"type": "input_json_delta", "partial_json": json.dumps(block["input"])},
            )
        yield event("content_block_stop", index=index)
    yield event(
        "message_delta",
        delta={"stop_reason": message["stop_reason"], "stop_sequence": None},
        usage={"output_tokens": _USAGE["output_tokens"]},
    )
    yield event("message_stop")


def _prompt_text(message: Mapping[str, Any]) -> str | None:
    if message.get("role") != "user":
        return None
    content = message.get("content")
    if isinstance(content, str):
        text = content.strip()
        return text if text and not text.startswith("<") and text != CONTINUE_PROMPT else None
    blocks = _blocks(message)
    if any(block.get("type") == "tool_result" for block in blocks):
        return None
    commands = [
        text
        for block in blocks
        if block.get("type") == "text"
        and (text := str(block.get("text", "")).strip())
        and not text.startswith("<")
        and text != CONTINUE_PROMPT
    ]
    return commands[-1] if commands else None


def _blocks(message: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    content = message.get("content")
    if not isinstance(content, list):
        return []
    return [block for block in content if isinstance(block, Mapping)]


def _block_text(content: object) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(
            str(block.get("text", ""))
            for block in content
            if isinstance(block, Mapping) and block.get("type") == "text"
        )
    return json.dumps(content)


def _system_text(system: object) -> str:
    if isinstance(system, str):
        return system
    if isinstance(system, list):
        return "\n".join(
            str(block.get("text", "")) for block in system if isinstance(block, Mapping)
        )
    return ""
