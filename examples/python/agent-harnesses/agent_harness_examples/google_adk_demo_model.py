# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""The local demo model as a Google ADK BaseLlm; see demo_model_script."""

from __future__ import annotations

import json
from collections.abc import AsyncGenerator
from typing import Any

from google.adk.models.base_llm import BaseLlm
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse
from google.genai import types

from agent_harness_examples.demo_model_script import DEMO_MODEL, DemoToolRequest, plan_demo_reply


class GoogleADKDemoModel(BaseLlm):
    model: str = DEMO_MODEL

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        yield _reply(llm_request.contents)


def _reply(contents: list[types.Content]) -> LlmResponse:
    """Returns the reply from the turn's prompt and tool results alone, so retries repeat it."""
    prompt_indexes = [
        index
        for index, content in enumerate(contents)
        if content.role == "user" and any(part.text for part in content.parts or [])
    ]
    last_prompt_index = prompt_indexes[-1]
    prompt_text = "".join(part.text or "" for part in contents[last_prompt_index].parts or [])
    outputs = [
        _describe_tool_response(part.function_response.response or {})
        for content in contents[last_prompt_index + 1 :]
        for part in content.parts or []
        if part.function_response is not None
    ]
    reply = plan_demo_reply(prompt_text, outputs)
    if isinstance(reply, DemoToolRequest):
        call = types.FunctionCall(name=reply.tool_name, args=dict(reply.arguments))
        return LlmResponse(content=types.Content(role="model", parts=[types.Part(function_call=call)]))
    return LlmResponse(content=types.Content(role="model", parts=[types.Part(text=reply)]))


def _describe_tool_response(response: dict[str, Any]) -> str:
    result = response.get("result")
    return result if isinstance(result, str) else json.dumps(response, sort_keys=True)
