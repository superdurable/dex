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

"""A keyless stand-in for the OpenAI model, used only by the tests.

It reads the turn's user message and the tool results the turn already has,
so a retried model call gets the same reply.
"""

from __future__ import annotations

import re
from collections.abc import AsyncIterator, Mapping
from typing import Any

from agents import Handoff, Model, ModelResponse, ModelSettings, ModelTracing
from agents.agent_output import AgentOutputSchemaBase
from agents.items import TResponseInputItem, TResponseStreamEvent
from agents.testing import ModelStep, ScriptedModel, assistant_message, function_call
from agents.tool import Tool
from openai.types.responses.response_prompt_param import ResponsePromptParam


class OrderScriptModel(Model):
    async def get_response(
        self,
        system_instructions: str | None,
        input: str | list[TResponseInputItem],
        model_settings: ModelSettings,
        tools: list[Tool],
        output_schema: AgentOutputSchemaBase | None,
        handoffs: list[Handoff],
        tracing: ModelTracing,
        *,
        previous_response_id: str | None,
        conversation_id: str | None,
        prompt: ResponsePromptParam | None,
    ) -> ModelResponse:
        return await _step(input).get_response(
            system_instructions,
            input,
            model_settings,
            tools,
            output_schema,
            handoffs,
            tracing,
            previous_response_id=previous_response_id,
            conversation_id=conversation_id,
            prompt=prompt,
        )

    async def stream_response(
        self,
        system_instructions: str | None,
        input: str | list[TResponseInputItem],
        model_settings: ModelSettings,
        tools: list[Tool],
        output_schema: AgentOutputSchemaBase | None,
        handoffs: list[Handoff],
        tracing: ModelTracing,
        *,
        previous_response_id: str | None,
        conversation_id: str | None,
        prompt: ResponsePromptParam | None,
    ) -> AsyncIterator[TResponseStreamEvent]:
        events = _step(input).stream_response(
            system_instructions,
            input,
            model_settings,
            tools,
            output_schema,
            handoffs,
            tracing,
            previous_response_id=previous_response_id,
            conversation_id=conversation_id,
            prompt=prompt,
        )
        async for event in events:
            yield event


def _step(input: str | list[TResponseInputItem]) -> ScriptedModel:
    items: list[Mapping[str, Any]] = (
        [{"role": "user", "content": input}] if isinstance(input, str) else [_as_mapping(i) for i in input]
    )
    users = [n for n, item in enumerate(items) if item.get("role") == "user"]
    text = _text(items[users[-1]])
    outputs = [str(i.get("output")) for i in items[users[-1] + 1 :] if i.get("type") == "function_call_output"]
    match = re.search(r"order (\d+)", text, re.IGNORECASE)
    if outputs or match is None:
        output = assistant_message("; ".join(outputs) or "How can I help?", item_id=f"m-{len(users)}")
    elif "cancel" in text.lower():
        output = function_call(
            "cancel_order", f'{{"order_id": "{match[1]}", "reason": "asked"}}', call_id=f"c-{len(users)}"
        )
    else:
        output = function_call("look_up_order", f'{{"order_id": "{match[1]}"}}', call_id=f"l-{len(users)}")
    return ScriptedModel([ModelStep(output=[output])])


def _as_mapping(item: Any) -> Mapping[str, Any]:
    if isinstance(item, Mapping):
        return item
    dumped: Mapping[str, Any] = item.model_dump(exclude_unset=True)
    return dumped


def _text(item: Mapping[str, Any]) -> str:
    content = item.get("content")
    if isinstance(content, str):
        return content
    return "".join(str(p.get("text", "")) for p in content or [] if isinstance(p, Mapping))
