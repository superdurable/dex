# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real ADK agent with a media store, driven by a scripted model that records its requests.

The scripted model reads one command from the latest user text:

* ``describe`` answers ``saw <n> images``, counting the inline images of the
  request.
* ``screenshot`` calls ``take_screenshot``, an ADK tool that returns a PNG
  ``types.Part``. Once the result arrives, it answers
  ``take_screenshot: <result>``.
* ``screenshot-hold <gate>`` calls ``take_screenshot`` in two model calls in a
  row, and the third model call waits at that ``FileGate`` after recording its
  request.

Every model call appends the JSON of its request contents to
``model-requests.jsonl`` before it answers. The media store is a
``LocalDirectoryMediaStore`` under the state directory, shared by the test
process and every Worker.
"""

from __future__ import annotations

import json
import os
import struct
import zlib
from collections.abc import AsyncGenerator, Sequence
from pathlib import Path
from typing import Any

from google.adk.agents import LlmAgent
from google.adk.models.base_llm import BaseLlm
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse
from google.genai import types

from dex_agents import AgentSessionFlow, LocalDirectoryMediaStore, ToolEffect, ToolPolicy
from dex_agents.testing import FileGate
from dex_agents_google_adk import adk_agent_session_flow
from tests.scripted_adk_agent import STATE_DIR_ENVIRONMENT_VARIABLE

IMAGE_ADK_AGENT_FLOW_TYPE = "ImageAdkAgent"
MODEL_REQUESTS_LOG_NAME = "model-requests.jsonl"
MEDIA_DIRECTORY_NAME = "media"
SCREENSHOT_RESULT = "took a screenshot"


def screenshot_png() -> bytes:
    """Return the 2x2 PNG that ``take_screenshot`` returns, the same bytes on every call."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        checksum = struct.pack(">I", zlib.crc32(kind + data))
        return struct.pack(">I", len(data)) + kind + data + checksum

    header = struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0)
    rows = b"\x00\xff\x00\x00\x00\xff\x00" + b"\x00\x00\x00\xff\xff\xff\xff"
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(rows, 9))
        + chunk(b"IEND", b"")
    )


def read_model_requests(state_dir: Path) -> list[list[types.Content]]:
    """Return the contents of every recorded model request, oldest first."""
    path = state_dir / MODEL_REQUESTS_LOG_NAME
    if not path.exists():
        return []
    return [
        [types.Content.model_validate(content) for content in json.loads(line)]
        for line in path.read_text().splitlines()
    ]


class ScriptedImageModel(BaseLlm):
    """Record each request, then answer from the command in its latest user text."""

    model: str = "scripted-image-model"

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        _record_request(llm_request.contents)
        command_index, command = _latest_user_text(llm_request.contents)
        responses = [
            part.function_response
            for content in llm_request.contents[command_index + 1 :]
            for part in content.parts or []
            if part.function_response is not None
        ]
        if command == "describe":
            yield _text_response(f"saw {_inline_image_count(llm_request.contents)} images")
        elif command == "screenshot" or command.startswith("screenshot-hold "):
            screenshot_count = 2 if command.startswith("screenshot-hold ") else 1
            if len(responses) < screenshot_count:
                yield _function_call_response("take_screenshot")
                return
            if command.startswith("screenshot-hold "):
                gate = FileGate(_state_dir(), command.removeprefix("screenshot-hold "))
                gate.mark_reached()
                await gate.wait_for_release()
            result = (responses[-1].response or {}).get("result")
            yield _text_response(f"take_screenshot: {result}")
        else:
            raise ValueError(f"unknown image command {command!r}")


def take_screenshot() -> dict[str, Any]:
    """Take a screenshot of the screen."""
    return {
        "result": SCREENSHOT_RESULT,
        "screenshot": types.Part.from_bytes(data=screenshot_png(), mime_type="image/png"),
    }


def build_image_adk_flow(state_dir: Path) -> AgentSessionFlow:
    agent = LlmAgent(
        name="viewer",
        model=ScriptedImageModel(),
        instruction="Follow the scripted commands.",
        tools=[take_screenshot],
    )
    return adk_agent_session_flow(
        agent,
        flow_type=IMAGE_ADK_AGENT_FLOW_TYPE,
        tool_policies={"take_screenshot": ToolPolicy(ToolEffect.READ)},
        media_store=LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME),
    )


def build_image_adk_flow_from_environment() -> AgentSessionFlow:
    return build_image_adk_flow(_state_dir())


def _record_request(contents: Sequence[types.Content]) -> None:
    line = json.dumps([content.model_dump(mode="json", exclude_none=True) for content in contents])
    with (_state_dir() / MODEL_REQUESTS_LOG_NAME).open("a") as log:
        log.write(line + "\n")


def _latest_user_text(contents: Sequence[types.Content]) -> tuple[int, str]:
    user_texts = [
        (index, part.text)
        for index, content in enumerate(contents)
        if content.role == "user"
        for part in content.parts or []
        if part.text
    ]
    if not user_texts:
        raise RuntimeError("the request has no user message")
    return user_texts[-1]


def _inline_image_count(contents: Sequence[types.Content]) -> int:
    count = 0
    for content in contents:
        for part in content.parts or []:
            if part.inline_data is not None:
                count += 1
            if part.function_response is not None:
                count += sum(
                    1
                    for response_part in part.function_response.parts or []
                    if response_part.inline_data is not None
                )
    return count


def _function_call_response(tool_name: str) -> LlmResponse:
    return LlmResponse(
        content=types.Content(
            role="model",
            parts=[types.Part(function_call=types.FunctionCall(name=tool_name, args={}))],
        ),
        finish_reason=types.FinishReason.STOP,
    )


def _text_response(text: str) -> LlmResponse:
    return LlmResponse(
        content=types.Content(role="model", parts=[types.Part(text=text)]),
        finish_reason=types.FinishReason.STOP,
    )


def _state_dir() -> Path:
    return Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
