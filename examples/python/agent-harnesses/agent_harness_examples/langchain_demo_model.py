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

"""The local demo model as a LangChain chat model; see demo_model_script."""

from __future__ import annotations

import json
from collections.abc import Callable, Iterator, Sequence
from typing import Any

from langchain_core.callbacks import CallbackManagerForLLMRun
from langchain_core.language_models import BaseChatModel, LanguageModelInput
from langchain_core.messages import (
    AIMessage,
    AIMessageChunk,
    BaseMessage,
    HumanMessage,
    ToolCall,
    ToolMessage,
)
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult
from langchain_core.runnables import Runnable
from langchain_core.tools import BaseTool

from agent_harness_examples.demo_model_script import DemoToolRequest, plan_demo_reply


class LangChainDemoChatModel(BaseChatModel):
    @property
    def _llm_type(self) -> str:
        return "order-support-demo"

    def bind_tools(
        self,
        tools: Sequence[dict[str, Any] | type | Callable[..., Any] | BaseTool],
        *,
        tool_choice: str | None = None,
        **kwargs: Any,
    ) -> Runnable[LanguageModelInput, AIMessage]:
        return self

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        return ChatResult(generations=[ChatGeneration(message=_reply(messages))])

    def _stream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        reply = _reply(messages)
        yield ChatGenerationChunk(
            message=AIMessageChunk(
                content=reply.content,
                tool_call_chunks=[
                    {
                        "name": tool_call["name"],
                        "args": json.dumps(tool_call["args"]),
                        "id": tool_call["id"],
                        "index": index,
                        "type": "tool_call_chunk",
                    }
                    for index, tool_call in enumerate(reply.tool_calls)
                ],
                chunk_position="last",
            )
        )


def _reply(messages: list[BaseMessage]) -> AIMessage:
    """Returns the reply from the turn's prompt and tool results alone, so retries repeat it."""
    prompt_indexes = [
        index for index, message in enumerate(messages) if isinstance(message, HumanMessage)
    ]
    last_prompt_index = prompt_indexes[-1]
    outputs = [
        message.text
        for message in messages[last_prompt_index + 1 :]
        if isinstance(message, ToolMessage)
    ]
    reply = plan_demo_reply(messages[last_prompt_index].text, outputs)
    if isinstance(reply, DemoToolRequest):
        tool_call = ToolCall(
            name=reply.tool_name,
            args=dict(reply.arguments),
            id=f"call-{len(prompt_indexes)}-{reply.tool_name}",
            type="tool_call",
        )
        return AIMessage(content="", tool_calls=[tool_call])
    return AIMessage(content=reply)

