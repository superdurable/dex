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

"""The local demo model as a Pydantic AI FunctionModel; see demo_model_script."""

from __future__ import annotations

from collections.abc import AsyncIterator

from pydantic_ai import (
    ModelMessage,
    ModelRequest,
    ModelResponse,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.models.function import AgentInfo, DeltaToolCall, DeltaToolCalls, FunctionModel

from agent_harness_examples.demo_model_script import DemoToolRequest, plan_demo_reply


def build_pydantic_ai_demo_model() -> FunctionModel:
    return FunctionModel(_respond, stream_function=_stream_response)


def _respond(messages: list[ModelMessage], info: AgentInfo) -> ModelResponse:
    reply = _plan_reply(messages)
    if isinstance(reply, ToolCallPart):
        return ModelResponse(parts=[reply])
    return ModelResponse(parts=[TextPart(reply)])


async def _stream_response(
    messages: list[ModelMessage], info: AgentInfo
) -> AsyncIterator[str | DeltaToolCalls]:
    reply = _plan_reply(messages)
    if isinstance(reply, ToolCallPart):
        yield {
            0: DeltaToolCall(reply.tool_name, reply.args_as_json_str(), tool_call_id=reply.tool_call_id)
        }
        return
    yield reply


def _plan_reply(messages: list[ModelMessage]) -> ToolCallPart | str:
    """Returns the reply from the turn's prompt and tool results alone, so retries repeat it."""
    prompt_indexes = [
        index
        for index, message in enumerate(messages)
        if isinstance(message, ModelRequest)
        and any(isinstance(part, UserPromptPart) for part in message.parts)
    ]
    last_prompt_index = prompt_indexes[-1]
    prompt = next(
        part
        for part in messages[last_prompt_index].parts
        if isinstance(part, UserPromptPart)
    )
    outputs = [
        part.model_response_str()
        for message in messages[last_prompt_index:]
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, ToolReturnPart)
    ]
    user_text = prompt.content if isinstance(prompt.content, str) else ""
    reply = plan_demo_reply(user_text, outputs)
    if isinstance(reply, DemoToolRequest):
        call_id = f"call-{len(prompt_indexes)}-{reply.tool_name}"
        return ToolCallPart(reply.tool_name, reply.arguments, call_id)
    return reply
