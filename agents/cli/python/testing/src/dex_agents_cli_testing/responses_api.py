# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted local stand-in for the OpenAI Responses API.

``ResponsesApiStub`` answers ``POST /v1/responses``, streaming or not, with
the scripted model's reply. Point a client at it with
``OPENAI_BASE_URL=<base_url>/v1``. It keeps no conversation state, so every
request must carry its whole input, as the Dex harnesses send it.
"""

from __future__ import annotations

import json
import time
import uuid
from collections.abc import Iterator, Mapping, Sequence
from typing import Any

from dex_agents_cli_testing.model_api import ModelApiRequest, ModelApiResponse, ModelApiStub
from dex_agents_cli_testing.scripted_model import (
    ScriptedConversation,
    ScriptedReply,
    ScriptedToolResult,
    scripted_reply,
)

_USAGE = {
    "input_tokens": 10,
    "input_tokens_details": {"cached_tokens": 0},
    "output_tokens": 5,
    "output_tokens_details": {"reasoning_tokens": 0},
    "total_tokens": 15,
}


class ResponsesApiStub(ModelApiStub):
    """Serve the scripted model as the Responses API."""

    def is_model_request(self, request: ModelApiRequest) -> bool:
        """Report whether a request is ``POST .../responses``."""
        return request.method == "POST" and request.path.split("?", 1)[0].endswith("/responses")

    def respond(self, request: ModelApiRequest) -> ModelApiResponse:
        """Answer a Responses API request with the scripted reply."""
        if not self.is_model_request(request):
            return ModelApiResponse(404, body={"error": {"message": "not found"}})
        conversation = read_responses_conversation(request.body)
        response = _response_object(request.body, scripted_reply(conversation))
        if request.body.get("stream"):
            return ModelApiResponse(events=_stream_events(response))
        return ModelApiResponse(body=response)


def read_responses_conversation(body: Mapping[str, Any]) -> ScriptedConversation:
    """Read the scripted model's view of a Responses API request body."""
    raw_input = body.get("input")
    items: list[Mapping[str, Any]] = (
        [{"role": "user", "content": raw_input}]
        if isinstance(raw_input, str)
        else [item for item in raw_input or [] if isinstance(item, Mapping)]
    )
    instructions = [str(body.get("instructions") or "")]
    prompts: list[str] = []
    last_prompt_index = -1
    for index, item in enumerate(items):
        role = item.get("role")
        if role in ("system", "developer"):
            instructions.append(_content_text(item.get("content")))
        elif role == "user" and item.get("type") in (None, "message"):
            prompts.append(_content_text(item.get("content")))
            last_prompt_index = index
    call_names = {
        str(item.get("call_id")): str(item.get("name"))
        for item in items
        if item.get("type") == "function_call"
    }
    results = [
        ScriptedToolResult(
            call_names.get(str(item.get("call_id")), "unknown"), _output_text(item.get("output"))
        )
        for item in items[last_prompt_index + 1 :]
        if item.get("type") == "function_call_output"
    ]
    tool_names = [
        str(tool.get("name"))
        for tool in body.get("tools") or []
        if isinstance(tool, Mapping) and tool.get("type") == "function"
    ]
    return ScriptedConversation(
        model=str(body.get("model") or ""),
        instructions="\n".join(text for text in instructions if text),
        prompts=prompts,
        tool_results=results,
        tool_names=tool_names,
    )


def _response_object(body: Mapping[str, Any], reply: ScriptedReply) -> dict[str, Any]:
    output: list[dict[str, Any]] = []
    if reply.text is not None:
        output.append(
            {
                "type": "message",
                "id": f"msg_{uuid.uuid4().hex[:16]}",
                "status": "completed",
                "role": "assistant",
                "content": [
                    {"type": "output_text", "text": reply.text, "annotations": [], "logprobs": []}
                ],
            }
        )
    for call in reply.tool_calls:
        output.append(
            {
                "type": "function_call",
                "id": f"fc_{uuid.uuid4().hex[:16]}",
                "call_id": call.call_id,
                "name": call.tool_name,
                "arguments": json.dumps(call.arguments),
                "status": "completed",
            }
        )
    return {
        "id": f"resp_{uuid.uuid4().hex[:16]}",
        "object": "response",
        "created_at": int(time.time()),
        "status": "completed",
        "model": str(body.get("model") or ""),
        "output": output,
        "parallel_tool_calls": True,
        "tool_choice": body.get("tool_choice") or "auto",
        "tools": body.get("tools") or [],
        "usage": _USAGE,
        "error": None,
        "incomplete_details": None,
        "instructions": body.get("instructions"),
        "metadata": {},
        "temperature": 1.0,
        "top_p": 1.0,
        "text": {"format": {"type": "text"}},
        "truncation": "disabled",
    }


def _stream_events(response: Mapping[str, Any]) -> Iterator[tuple[str, Mapping[str, Any]]]:
    sequence = iter(range(1_000_000))
    pending = {**response, "status": "in_progress", "output": []}

    def event(event_type: str, **fields: Any) -> tuple[str, Mapping[str, Any]]:
        return event_type, {"type": event_type, "sequence_number": next(sequence), **fields}

    yield event("response.created", response=pending)
    yield event("response.in_progress", response=pending)
    output: Sequence[Mapping[str, Any]] = response["output"]
    for index, item in enumerate(output):
        if item["type"] == "message":
            text = item["content"][0]["text"]
            yield event(
                "response.output_item.added",
                output_index=index,
                item={**item, "status": "in_progress", "content": []},
            )
            empty_part = {"type": "output_text", "text": "", "annotations": [], "logprobs": []}
            yield event(
                "response.content_part.added",
                item_id=item["id"],
                output_index=index,
                content_index=0,
                part=empty_part,
            )
            yield event(
                "response.output_text.delta",
                item_id=item["id"],
                output_index=index,
                content_index=0,
                delta=text,
                logprobs=[],
            )
            yield event(
                "response.output_text.done",
                item_id=item["id"],
                output_index=index,
                content_index=0,
                text=text,
                logprobs=[],
            )
            yield event(
                "response.content_part.done",
                item_id=item["id"],
                output_index=index,
                content_index=0,
                part=item["content"][0],
            )
        else:
            yield event(
                "response.output_item.added",
                output_index=index,
                item={**item, "status": "in_progress", "arguments": ""},
            )
            yield event(
                "response.function_call_arguments.delta",
                item_id=item["id"],
                output_index=index,
                delta=item["arguments"],
            )
            yield event(
                "response.function_call_arguments.done",
                item_id=item["id"],
                output_index=index,
                arguments=item["arguments"],
                name=item["name"],
            )
        yield event("response.output_item.done", output_index=index, item=item)
    yield event("response.completed", response=response)


def _content_text(content: object) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(
            str(part.get("text", ""))
            for part in content
            if isinstance(part, Mapping)
            and part.get("type") in ("input_text", "output_text", "text")
        )
    return ""


def _output_text(output: object) -> str:
    if isinstance(output, str):
        return output
    if isinstance(output, list):
        return _content_text(output)
    return json.dumps(output)
