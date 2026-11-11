# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A chat model that answers from the conversation alone, so every Worker replies the same.

The model reads commands from the last user message:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with every earlier user message, proving history replay.
* ``describe`` answers ``saw <n> images`` for the base64 image blocks of every user message.
* ``call <tool> <json>`` asks for one tool call; join several with `` && ``.
  Separate model calls with `` then ``: each one's tool calls follow the last one's results.
  After the last one it answers ``<tool>: <result>`` for every call, joined by ``; ``.
  A ``fail`` step raises instead. A ``stall <gate>`` step streams ``waiting;``, waits at
  the gate, and then streams the rest of the final answer.

Every model call records ``model:<n>`` in the InvocationLog, ``n`` counting the turn's calls.
With ``model_input_log_path``, it also records its user and tool messages there as one JSON
line, with the MIME type, SHA-256, and base64 of each image block's bytes.
Each reply's ``response_metadata`` names the response model, provider, response ID, and
finish reason, as LangChain integrations do.
"""

from __future__ import annotations

import base64
import hashlib
import json
import uuid
from collections.abc import AsyncIterator, Callable, Iterator, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from dex_agents.testing import FileGate, InvocationLog
from langchain_core.callbacks import AsyncCallbackManagerForLLMRun, CallbackManagerForLLMRun
from langchain_core.language_models import BaseChatModel, LanguageModelInput
from langchain_core.messages import (
    AIMessage,
    AIMessageChunk,
    BaseMessage,
    HumanMessage,
    ToolCall,
    ToolMessage,
)
from langchain_core.messages.ai import UsageMetadata
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult
from langchain_core.runnables import Runnable
from langchain_core.tools import BaseTool

MODEL_CALL_INVOCATION_PREFIX = "model:"
MODEL_CALL_USAGE = UsageMetadata(input_tokens=10, output_tokens=5, total_tokens=15)
SCRIPTED_REQUEST_MODEL = "scripted-chat-model"
SCRIPTED_RESPONSE_MODEL = "scripted-chat-model-2026-11"
SCRIPTED_MODEL_PROVIDER = "scripted"
STALL_TEXT = "waiting;"


@dataclass(frozen=True)
class ScriptedReply:
    message: AIMessage
    stall_gate: str | None = None


class ScriptedChatModel(BaseChatModel):
    """Reply deterministically from the messages, and log every call to an InvocationLog."""

    invocation_log_path: str
    fresh_tool_call_ids: bool = False
    model_input_log_path: str | None = None
    # BaseChatModel._get_ls_params reports it as ls_model_name.
    model: str = SCRIPTED_REQUEST_MODEL

    @property
    def _llm_type(self) -> str:
        return "scripted"

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
        reply = self._reply(messages)
        if reply.stall_gate is not None:
            raise ValueError("a stall step needs the async streaming path")
        return ChatResult(generations=[ChatGeneration(message=reply.message)])

    def _stream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        reply = self._reply(messages)
        if reply.stall_gate is not None:
            raise ValueError("a stall step needs the async streaming path")
        yield from _reply_chunks(reply.message)

    async def _astream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[ChatGenerationChunk]:
        reply = self._reply(messages)
        chunks = list(_reply_chunks(reply.message))
        if reply.stall_gate is not None:
            yield chunks.pop(0)
            gate = FileGate(Path(self.invocation_log_path).parent, reply.stall_gate)
            gate.mark_reached()
            await gate.wait_for_release()
        for chunk in chunks:
            yield chunk

    def _reply(self, messages: list[BaseMessage]) -> ScriptedReply:
        user_index = max(
            index for index, message in enumerate(messages) if isinstance(message, HumanMessage)
        )
        turn_messages = messages[user_index + 1 :]
        replies = [message for message in turn_messages if isinstance(message, AIMessage)]
        InvocationLog(Path(self.invocation_log_path)).record(
            f"{MODEL_CALL_INVOCATION_PREFIX}{len(replies) + 1}"
        )
        if self.model_input_log_path is not None:
            InvocationLog(Path(self.model_input_log_path)).record(
                json.dumps(describe_model_input(messages))
            )
        reply = self._plan_reply(messages, user_index, turn_messages, replies)
        response_metadata = {
            "model_name": SCRIPTED_RESPONSE_MODEL,
            "model_provider": SCRIPTED_MODEL_PROVIDER,
            "id": f"scripted-response-{user_index}-{len(replies)}",
            "finish_reason": "tool_calls" if reply.message.tool_calls else "stop",
        }
        return ScriptedReply(
            reply.message.model_copy(update={"response_metadata": response_metadata}),
            reply.stall_gate,
        )

    def _plan_reply(
        self,
        messages: list[BaseMessage],
        user_index: int,
        turn_messages: Sequence[BaseMessage],
        replies: Sequence[AIMessage],
    ) -> ScriptedReply:
        command = messages[user_index].text
        if command.startswith("say "):
            return ScriptedReply(_answer(f"said: {command.removeprefix('say ')}"))
        if command == "recall":
            earlier = [
                message.text
                for message in messages[:user_index]
                if isinstance(message, HumanMessage)
            ]
            return ScriptedReply(_answer("history: " + " | ".join(earlier)))
        if command == "describe":
            image_count = sum(
                isinstance(block, dict) and block.get("type") == "image"
                for message in messages
                if isinstance(message, HumanMessage) and isinstance(message.content, list)
                for block in message.content
            )
            return ScriptedReply(_answer(f"saw {image_count} images"))
        if command.startswith("call "):
            return self._reply_to_tool_command(command, user_index, turn_messages, replies)
        raise ValueError(f"unknown scripted command {command!r}")

    def _reply_to_tool_command(
        self,
        command: str,
        user_index: int,
        turn_messages: Sequence[BaseMessage],
        replies: Sequence[AIMessage],
    ) -> ScriptedReply:
        steps = command.split(" then ")
        step = len(replies)
        if step < len(steps) and steps[step] == "fail":
            raise RuntimeError("scripted model failure")
        if step < len(steps) and steps[step].startswith("stall "):
            summary = _summarize_tool_results(turn_messages, replies).message.text
            return ScriptedReply(
                _answer(f"{STALL_TEXT} {summary}"), stall_gate=steps[step].removeprefix("stall ")
            )
        if step >= len(steps):
            return _summarize_tool_results(turn_messages, replies)
        tool_calls: list[ToolCall] = []
        for index, request in enumerate(steps[step].split(" && ")):
            _, tool_name, arguments = request.split(" ", 2)
            tool_call_id = f"call-{user_index}-{step}-{index}"
            if self.fresh_tool_call_ids:
                tool_call_id = f"{tool_call_id}-{uuid.uuid4().hex[:8]}"
            tool_calls.append(
                ToolCall(
                    name=tool_name, args=json.loads(arguments), id=tool_call_id, type="tool_call"
                )
            )
        return ScriptedReply(
            AIMessage(
                content=f"step {step + 1}", tool_calls=tool_calls, usage_metadata=MODEL_CALL_USAGE
            )
        )


def describe_model_input(messages: Sequence[BaseMessage]) -> list[dict[str, Any]]:
    """Describe the user and tool messages a model call received, image bytes included."""
    return [
        {
            "kind": "human" if isinstance(message, HumanMessage) else "tool",
            "content": _describe_content(message.content),
        }
        for message in messages
        if isinstance(message, HumanMessage | ToolMessage)
    ]


def _describe_content(content: str | list[str | dict[str, Any]]) -> list[Any]:
    described: list[Any] = []
    for block in [content] if isinstance(content, str) else content:
        if isinstance(block, str):
            described.append(block)
        elif block.get("type") == "text":
            described.append(block["text"])
        elif block.get("type") == "image" and "base64" in block:
            data = base64.b64decode(block["base64"])
            described.append(
                {
                    "image": block["mime_type"],
                    "sha256": hashlib.sha256(data).hexdigest(),
                    "base64": block["base64"],
                }
            )
        else:
            described.append(block)
    return described


def _summarize_tool_results(
    turn_messages: Sequence[BaseMessage],
    replies: Sequence[AIMessage],
) -> ScriptedReply:
    results = {
        message.tool_call_id: message.text
        for message in turn_messages
        if isinstance(message, ToolMessage)
    }
    summaries = [
        f"{tool_call['name']}: {results[tool_call['id'] or '']}"
        for reply in replies
        for tool_call in reply.tool_calls
    ]
    return ScriptedReply(_answer("; ".join(summaries)))


def _answer(text: str) -> AIMessage:
    return AIMessage(content=text, usage_metadata=MODEL_CALL_USAGE)


def _reply_chunks(reply: AIMessage) -> Iterator[ChatGenerationChunk]:
    words = reply.text.split(" ")
    for index, word in enumerate(words):
        yield ChatGenerationChunk(
            message=AIMessageChunk(content=word if index == 0 else f" {word}")
        )
    yield ChatGenerationChunk(
        message=AIMessageChunk(
            content="",
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
            usage_metadata=reply.usage_metadata,
            response_metadata=reply.response_metadata,
            chunk_position="last",
        )
    )
