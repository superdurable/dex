# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted local stand-in for the Gemini API.

``GeminiApiStub`` answers ``POST /v1beta/models/<model>:generateContent`` and
``:streamGenerateContent?alt=sse`` with the scripted model's reply. Point the
Google Gen AI SDK at it with ``GOOGLE_GEMINI_BASE_URL=<base_url>``.
"""

from __future__ import annotations

import json
from collections.abc import Mapping
from typing import Any

from dex_agents_cli_testing.model_api import ModelApiRequest, ModelApiResponse, ModelApiStub
from dex_agents_cli_testing.scripted_model import (
    ScriptedConversation,
    ScriptedReply,
    ScriptedToolResult,
    scripted_reply,
)

_USAGE = {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}


class GeminiApiStub(ModelApiStub):
    """Serve the scripted model as the Gemini API."""

    def is_model_request(self, request: ModelApiRequest) -> bool:
        """Report whether a request generates content."""
        path = request.path.split("?", 1)[0]
        return request.method == "POST" and path.endswith(
            (":generateContent", ":streamGenerateContent")
        )

    def respond(self, request: ModelApiRequest) -> ModelApiResponse:
        """Answer a generate content request with the scripted reply."""
        if not self.is_model_request(request):
            return ModelApiResponse(404, body={"error": {"code": 404, "message": "not found"}})
        path = request.path.split("?", 1)[0]
        model = path.rsplit("/", 1)[-1].split(":", 1)[0]
        response = _response_object(
            model, scripted_reply(read_gemini_conversation(model, request.body))
        )
        if path.endswith(":streamGenerateContent"):
            return ModelApiResponse(events=[(None, response)])
        return ModelApiResponse(body=response)


def read_gemini_conversation(model: str, body: Mapping[str, Any]) -> ScriptedConversation:
    """Read the scripted model's view of a generate content request body."""
    contents = [content for content in body.get("contents") or [] if isinstance(content, Mapping)]
    prompts: list[str] = []
    last_prompt_index = -1
    for index, content in enumerate(contents):
        parts = _parts(content)
        texts = [str(part["text"]) for part in parts if isinstance(part.get("text"), str)]
        has_response = any("functionResponse" in part for part in parts)
        if content.get("role", "user") == "user" and texts and not has_response:
            prompts.append("".join(texts))
            last_prompt_index = index
    results = [
        ScriptedToolResult(
            str(part["functionResponse"].get("name", "unknown")),
            _response_text(part["functionResponse"].get("response")),
        )
        for content in contents[last_prompt_index + 1 :]
        for part in _parts(content)
        if isinstance(part.get("functionResponse"), Mapping)
    ]
    system_instruction = body.get("systemInstruction")
    instructions = (
        "".join(str(part.get("text", "")) for part in _parts(system_instruction))
        if isinstance(system_instruction, Mapping)
        else ""
    )
    tool_names = [
        str(declaration.get("name"))
        for tool in body.get("tools") or []
        if isinstance(tool, Mapping)
        for declaration in tool.get("functionDeclarations") or []
        if isinstance(declaration, Mapping)
    ]
    return ScriptedConversation(
        model=model,
        instructions=instructions,
        prompts=prompts,
        tool_results=results,
        tool_names=tool_names,
    )


def _response_object(model: str, reply: ScriptedReply) -> dict[str, Any]:
    parts: list[dict[str, Any]] = []
    if reply.text is not None:
        parts.append({"text": reply.text})
    for call in reply.tool_calls:
        parts.append(
            {"functionCall": {"id": call.call_id, "name": call.tool_name, "args": call.arguments}}
        )
    return {
        "candidates": [
            {"content": {"role": "model", "parts": parts}, "finishReason": "STOP", "index": 0}
        ],
        "usageMetadata": _USAGE,
        "modelVersion": model,
    }


def _parts(content: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    return [part for part in content.get("parts") or [] if isinstance(part, Mapping)]


def _response_text(response: object) -> str:
    if isinstance(response, Mapping):
        for key in ("result", "output", "content"):
            value = response.get(key)
            if isinstance(value, str):
                return value
    return json.dumps(response)
