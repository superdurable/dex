# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Pydantic AI agent with a media store, whose ``FunctionModel`` records the images it receives.

The model reads the text of the latest user prompt:

* ``describe`` answers ``saw <n> images`` for every image in the user prompts.
* ``screenshot`` calls ``take_screenshot`` from a tool source, which uploads a
  PNG and returns it as a reference, then answers with the tool results.
* ``chart`` calls the agent's ``render_chart`` tool, which returns a PNG as
  ``BinaryContent``, then answers with the tool results.
* ``screenshot-stall <gate>`` is ``screenshot``, but the second model call
  waits at that ``FileGate`` after it records its input.

Every model call records its input in ``model-inputs.log`` as one JSON line:
for each request part, its kind, text, and the MIME type and SHA-256 of each
image's bytes. An image placeholder that reached the model is recorded as
``image-url``. The store is a ``LocalDirectoryMediaStore`` under the state
directory, shared by the test and the Worker.
"""

from __future__ import annotations

import hashlib
import json
import os
import struct
import zlib
from collections.abc import AsyncIterator, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from pydantic_ai import (
    Agent,
    BinaryContent,
    DeferredToolRequests,
    ImageUrl,
    ModelMessage,
    ModelRequest,
    ModelResponse,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.models.function import AgentInfo, DeltaToolCall, DeltaToolCalls, FunctionModel

from dex_agents import (
    AgentSessionFlow,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    LocalDirectoryMediaStore,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
)
from dex_agents.testing import FileGate, InvocationLog
from dex_agents_pydantic_ai import pydantic_ai_session_flow

IMAGE_AGENT_FLOW_TYPE = "ImagePydanticAIAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "IMAGE_AGENT_STATE_DIR"
MODEL_INPUTS_LOG_NAME = "model-inputs.log"
MEDIA_DIRECTORY_NAME = "media"
TOOL_POLICIES = {
    "take_screenshot": ToolPolicy(ToolEffect.READ),
    "render_chart": ToolPolicy(ToolEffect.READ),
}


def solid_png(red: int, green: int, blue: int) -> bytes:
    """Return a 2x2 PNG of one color, the same bytes on every call."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        checksum = struct.pack(">I", zlib.crc32(kind + data))
        return struct.pack(">I", len(data)) + kind + data + checksum

    header = struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0)
    row = b"\x00" + bytes((red, green, blue)) * 2
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(row * 2, 9))
        + chunk(b"IEND", b"")
    )


def screenshot_png() -> bytes:
    return solid_png(0, 128, 255)


def chart_png() -> bytes:
    return solid_png(255, 128, 0)


def build_media_store(state_dir: Path) -> LocalDirectoryMediaStore:
    return LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME)


@dataclass(frozen=True)
class ScreenshotToolDefinition:
    name: str
    description: str
    input_schema: Mapping[str, Any]


class ScreenshotToolSource:
    """Offer ``take_screenshot``, which uploads a PNG and returns its reference."""

    source_name = "screenshots"

    def __init__(self, media_store: LocalDirectoryMediaStore) -> None:
        self._media_store = media_store

    async def list_tools(self) -> Sequence[ScreenshotToolDefinition]:
        return [
            ScreenshotToolDefinition(
                "take_screenshot", "Take a screenshot.", {"type": "object", "properties": {}}
            )
        ]

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: Any,
    ) -> DeferredToolCall:
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, ToolEffect.READ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        reference = await self._media_store.put_image(screenshot_png())
        return ToolExecutionResult(output="took a screenshot", images=[reference])

    async def poll_tool_task(self, poll: Any) -> Any:
        raise LookupError("take_screenshot starts no tool tasks")


def build_image_agent(state_dir: Path) -> Agent[None, Any]:
    model_inputs = InvocationLog(state_dir / MODEL_INPUTS_LOG_NAME)

    def respond(messages: list[ModelMessage], info: AgentInfo) -> ModelResponse:
        model_inputs.record(json.dumps(describe_model_input(messages)))
        text, tool_calls, _ = plan_image_response(messages)
        return ModelResponse(parts=list(tool_calls) if tool_calls else [TextPart(text)])

    async def stream_response(
        messages: list[ModelMessage], info: AgentInfo
    ) -> AsyncIterator[str | DeltaToolCalls]:
        model_inputs.record(json.dumps(describe_model_input(messages)))
        text, tool_calls, stall_gate = plan_image_response(messages)
        if tool_calls:
            yield {
                index: DeltaToolCall(
                    call.tool_name, call.args_as_json_str(), tool_call_id=call.tool_call_id
                )
                for index, call in enumerate(tool_calls)
            }
            return
        if stall_gate is not None:
            gate = FileGate(state_dir, stall_gate)
            gate.mark_reached()
            await gate.wait_for_release()
        yield text

    agent: Agent[None, Any] = Agent(
        FunctionModel(respond, stream_function=stream_response),
        output_type=[str, DeferredToolRequests],
        name="images",
    )

    @agent.tool_plain
    def render_chart() -> list[Any]:
        """Render a chart."""
        return ["rendered a chart", BinaryContent(data=chart_png(), media_type="image/png")]

    return agent


def build_image_agent_flow(state_dir: Path) -> AgentSessionFlow:
    media_store = build_media_store(state_dir)
    return pydantic_ai_session_flow(
        build_image_agent(state_dir),
        flow_type=IMAGE_AGENT_FLOW_TYPE,
        tool_policies=TOOL_POLICIES,
        tool_sources=[ScreenshotToolSource(media_store)],
        media_store=media_store,
    )


def build_image_agent_flow_from_environment() -> AgentSessionFlow:
    return build_image_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def describe_model_input(messages: Sequence[ModelMessage]) -> list[dict[str, Any]]:
    parts: list[dict[str, Any]] = []
    for message in messages:
        if not isinstance(message, ModelRequest):
            continue
        for part in message.parts:
            if isinstance(part, UserPromptPart):
                parts.append({"kind": "user", "content": _describe_content(part.content)})
            elif isinstance(part, ToolReturnPart):
                parts.append(
                    {
                        "kind": "tool-return",
                        "tool_name": part.tool_name,
                        "content": _describe_content(part.content),
                    }
                )
    return parts


def plan_image_response(
    messages: Sequence[ModelMessage],
) -> tuple[str, tuple[ToolCallPart, ...], str | None]:
    prompt_index, command = _latest_prompt(messages)
    stage = sum(isinstance(message, ModelResponse) for message in messages[prompt_index + 1 :])
    if command == "describe":
        return f"saw {_count_user_images(messages)} images", (), None
    tool_name = {"screenshot": "take_screenshot", "chart": "render_chart"}.get(
        command.split(" ", 1)[0].removesuffix("-stall")
    )
    if tool_name is None:
        raise ValueError(f"unknown image command {command!r}")
    if stage == 0:
        return "", (ToolCallPart(tool_name, "{}", f"{tool_name}-{prompt_index}"),), None
    stall_gate = command.split(" ", 1)[1] if command.startswith("screenshot-stall ") else None
    return _summarize_tool_returns(messages[prompt_index + 1 :]), (), stall_gate


def _describe_content(content: Any) -> list[Any]:
    items = content if isinstance(content, list) else [content]
    described: list[Any] = []
    for item in items:
        if isinstance(item, BinaryContent):
            described.append(
                {
                    "image": item.media_type,
                    "sha256": hashlib.sha256(item.data).hexdigest(),
                    "base64": item.base64,
                }
            )
        elif isinstance(item, ImageUrl):
            described.append({"image-url": item.url})
        else:
            described.append(item if isinstance(item, str) else repr(item))
    return described


def _latest_prompt(messages: Sequence[ModelMessage]) -> tuple[int, str]:
    for index in range(len(messages) - 1, -1, -1):
        message = messages[index]
        if not isinstance(message, ModelRequest):
            continue
        for part in message.parts:
            if isinstance(part, UserPromptPart):
                content = part.content
                texts = (
                    [content]
                    if isinstance(content, str)
                    else [item for item in content if isinstance(item, str)]
                )
                return index, texts[0]
    raise ValueError("no user prompt")


def _count_user_images(messages: Sequence[ModelMessage]) -> int:
    return sum(
        isinstance(item, BinaryContent)
        for message in messages
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, UserPromptPart) and isinstance(part.content, list)
        for item in part.content
    )


def _summarize_tool_returns(messages: Sequence[ModelMessage]) -> str:
    return "; ".join(
        f"{part.tool_name}: {part.model_response_str()} with {len(part.files)} images"
        for message in messages
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, ToolReturnPart)
    )
