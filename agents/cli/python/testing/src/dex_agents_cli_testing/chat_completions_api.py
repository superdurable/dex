# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted local stand-in for the OpenAI Chat Completions API.

``ChatCompletionsApiStub`` answers ``POST /v1/chat/completions``, streaming
or not. Point a client at it with ``OPENAI_BASE_URL=<base_url>/v1``.
"""

from __future__ import annotations

import json
import time
import uuid
from collections.abc import Iterator, Mapping
from typing import Any

from dex_agents_cli_testing.model_api import ModelApiRequest, ModelApiResponse, ModelApiStub
from dex_agents_cli_testing.scripted_model import (
    ReplySource,
    ScriptedConversation,
    ScriptedReply,
    ScriptedToolResult,
    scripted_reply,
)

_USAGE = {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}


class ChatCompletionsApiStub(ModelApiStub):
    """Serve the scripted model as the Chat Completions API."""

    def __init__(self, reply_source: ReplySource = scripted_reply) -> None:
        """Create the stub; ``reply_source`` picks each reply."""
        super().__init__()
        self._reply_source = reply_source

    def is_model_request(self, request: ModelApiRequest) -> bool:
        """Report whether a request is ``POST .../chat/completions``."""
        path = request.path.split("?", 1)[0]
        return request.method == "POST" and path.endswith("/chat/completions")

    def respond(self, request: ModelApiRequest) -> ModelApiResponse:
        """Answer a Chat Completions request with the scripted reply."""
        if not self.is_model_request(request):
            return ModelApiResponse(404, body={"error": {"message": "not found"}})
        completion = _completion_object(
            request.body, self._reply_source(read_chat_conversation(request.body))
        )
        if request.body.get("stream"):
            return ModelApiResponse(events=_stream_chunks(completion), stream_end="[DONE]")
        return ModelApiResponse(body=completion)


def read_chat_conversation(body: Mapping[str, Any]) -> ScriptedConversation:
    """Read the scripted model's view of a Chat Completions request body."""
    messages = [message for message in body.get("messages") or [] if isinstance(message, Mapping)]
    prompts: list[str] = []
    instructions: list[str] = []
    last_prompt_index = -1
    for index, message in enumerate(messages):
        role = message.get("role")
        if role in ("system", "developer"):
            instructions.append(_content_text(message.get("content")))
        elif role == "user":
            prompts.append(_content_text(message.get("content")))
            last_prompt_index = index
    call_names = {
        str(call.get("id")): str((call.get("function") or {}).get("name"))
        for message in messages
        for call in message.get("tool_calls") or []
        if isinstance(call, Mapping)
    }
    later_messages = messages[last_prompt_index + 1 :]
    results = [
        ScriptedToolResult(
            call_names.get(str(message.get("tool_call_id")), "unknown"),
            _content_text(message.get("content")),
        )
        for message in later_messages
        if message.get("role") == "tool"
    ]
    tool_names = [
        str((tool.get("function") or {}).get("name"))
        for tool in body.get("tools") or []
        if isinstance(tool, Mapping)
    ]
    return ScriptedConversation(
        model=str(body.get("model") or ""),
        instructions="\n".join(text for text in instructions if text),
        prompts=prompts,
        tool_results=results,
        tool_names=tool_names,
        reply_count=sum(1 for message in later_messages if message.get("role") == "assistant"),
    )


def _completion_object(body: Mapping[str, Any], reply: ScriptedReply) -> dict[str, Any]:
    message: dict[str, Any] = {"role": "assistant", "content": reply.text}
    if reply.tool_calls:
        message["tool_calls"] = [
            {
                "id": call.call_id,
                "type": "function",
                "function": {"name": call.tool_name, "arguments": json.dumps(call.arguments)},
            }
            for call in reply.tool_calls
        ]
    return {
        "id": f"chatcmpl-{uuid.uuid4().hex[:20]}",
        "object": "chat.completion",
        "created": int(time.time()),
        "model": str(body.get("model") or ""),
        "choices": [
            {
                "index": 0,
                "message": message,
                "finish_reason": "tool_calls" if reply.tool_calls else "stop",
            }
        ],
        "usage": _USAGE,
    }


def _stream_chunks(completion: Mapping[str, Any]) -> Iterator[tuple[None, Mapping[str, Any]]]:
    choice = completion["choices"][0]
    message = choice["message"]
    base = {key: completion[key] for key in ("id", "created", "model")}

    def chunk(
        delta: Mapping[str, Any], finish_reason: str | None
    ) -> tuple[None, Mapping[str, Any]]:
        return None, {
            **base,
            "object": "chat.completion.chunk",
            "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
        }

    yield chunk({"role": "assistant", "content": ""}, None)
    if message.get("content"):
        yield chunk({"content": message["content"]}, None)
    for index, call in enumerate(message.get("tool_calls") or []):
        yield chunk({"tool_calls": [{"index": index, **call}]}, None)
    yield chunk({}, choice["finish_reason"])
    yield None, {**base, "object": "chat.completion.chunk", "choices": [], "usage": _USAGE}


def _content_text(content: object) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(
            str(part.get("text", ""))
            for part in content
            if isinstance(part, Mapping) and part.get("type") in ("text", "input_text")
        )
    return "" if content is None else json.dumps(content)
