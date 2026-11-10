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

"""The local demo model as an OpenAI Agents SDK Model; see demo_model_script."""

from __future__ import annotations

from collections.abc import AsyncIterator, Mapping
from typing import Any

from agents import Handoff, Model, ModelResponse, ModelSettings, ModelTracing
from agents.agent_output import AgentOutputSchemaBase
from agents.items import TResponseInputItem, TResponseStreamEvent
from agents.testing import ModelStep, ScriptedModel, assistant_message, function_call
from agents.tool import Tool
from openai.types.responses.response_prompt_param import ResponsePromptParam

from agent_harness_examples.demo_model_script import DemoToolRequest, plan_demo_reply


class OpenAIAgentsDemoModel(Model):
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
        return await _scripted_model(input).get_response(
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
        events = _scripted_model(input).stream_response(
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


def _scripted_model(input: str | list[TResponseInputItem]) -> ScriptedModel:
    """Returns a one-step model whose reply depends only on the input, so retries repeat it."""
    items = [{"role": "user", "content": input}] if isinstance(input, str) else input
    mappings = [_as_mapping(item) for item in items]
    user_indexes = [index for index, item in enumerate(mappings) if _is_user_message(item)]
    last_user_index = user_indexes[-1]
    turn_items = mappings[last_user_index + 1 :]
    outputs = [
        str(item.get("output"))
        for item in turn_items
        if item.get("type") == "function_call_output"
    ]
    reply = plan_demo_reply(_message_text(mappings[last_user_index]), outputs)
    if isinstance(reply, DemoToolRequest):
        call_id = f"call-{len(user_indexes)}-{reply.tool_name}"
        output = function_call(reply.tool_name, reply.arguments, call_id=call_id)
    else:
        output = assistant_message(reply, item_id=f"message-{len(user_indexes)}")
    return ScriptedModel([ModelStep(output=[output])])


def _as_mapping(item: Any) -> Mapping[str, Any]:
    if isinstance(item, Mapping):
        return item
    dumped: Mapping[str, Any] = item.model_dump(exclude_unset=True)
    return dumped


def _is_user_message(item: Mapping[str, Any]) -> bool:
    return item.get("role") == "user" and item.get("type") in (None, "message")


def _message_text(item: Mapping[str, Any]) -> str:
    content = item.get("content")
    if isinstance(content, str):
        return content
    return "".join(str(part.get("text", "")) for part in content or [] if isinstance(part, Mapping))
