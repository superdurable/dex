# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A built-in loop agent with a scripted model and a screenshot tool, for the image tests.

The model reads the current turn's user text:

* ``describe`` answers ``saw <n> images``, counting the images whose bytes
  the request carries.
* ``screenshot`` calls ``take_screenshot``, a tool source tool that uploads a
  PNG and returns its reference, then answers ``screenshot taken``.
* ``screenshot-hold <gate>`` does the same, but the second model call waits at
  that ``FileGate`` after it logs its request.

Every model call appends one JSON line to ``model-requests.log``: each input
item's kind and text, with each image as its MIME type and the SHA-256 and
size of the bytes the request carries. ``BuiltinImageAgent`` has a
``LocalDirectoryMediaStore`` under the state directory, and
``BuiltinNoStoreImageAgent`` has none.
"""

from __future__ import annotations

import hashlib
import json
import os
import struct
import zlib
from pathlib import Path
from typing import Any, ClassVar

from dex_agents import (
    AgentSessionFlow,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    LocalDirectoryMediaStore,
    MediaStore,
    ToolEffect,
    ToolExecutionResult,
    ToolTaskPoll,
    ToolTaskPollOutcome,
)
from dex_agents.builtin_loop import (
    AgentDefinition,
    ModelRequest,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    SourceToolDefinition,
    ToolResultItem,
    UserTextItem,
    builtin_agent_session_flow,
)
from dex_agents.testing import FileGate, InvocationLog

IMAGE_AGENT_FLOW_TYPE = "BuiltinImageAgent"
NO_STORE_IMAGE_AGENT_FLOW_TYPE = "BuiltinNoStoreImageAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "BUILTIN_IMAGE_AGENT_STATE_DIR"
MODEL_REQUESTS_LOG_NAME = "model-requests.log"
MEDIA_DIRECTORY_NAME = "media"
SCREENSHOT_TOOL_NAME = "take_screenshot"


def screenshot_png() -> bytes:
    """Return the 2x2 PNG that ``take_screenshot`` uploads, the same bytes on every call."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        checksum = struct.pack(">I", zlib.crc32(kind + data))
        return struct.pack(">I", len(data)) + kind + data + checksum

    header = struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0)
    rows = b"\x00\x00\xff\x00\xff\x00\x00" + b"\x00\xff\xff\xff\x00\x00\x00"
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(rows, 9))
        + chunk(b"IEND", b"")
    )


def describe_model_request(request: ModelRequest) -> dict[str, Any]:
    """Return what the model saw: items in order, each image by the bytes it carries."""
    items: list[dict[str, Any]] = []
    for item in request.input_items:
        if isinstance(item, UserTextItem | ToolResultItem):
            items.append(
                {
                    "kind": "user" if isinstance(item, UserTextItem) else "tool_result",
                    "text": item.text if isinstance(item, UserTextItem) else item.output,
                    "images": [
                        {
                            "mime_type": image.mime_type,
                            "sha256": hashlib.sha256(request.image_bytes[image.sha256]).hexdigest(),
                            "size": len(request.image_bytes[image.sha256]),
                        }
                        for image in item.images
                    ],
                }
            )
        else:
            items.append({"kind": "model_output", "text": item.text})
    return {"instructions": request.instructions, "items": items}


class _ScreenshotTool:
    name = SCREENSHOT_TOOL_NAME
    description = "Take a screenshot of the screen."
    input_schema: ClassVar[dict[str, Any]] = {"type": "object", "properties": {}}


class ScreenshotToolSource:
    """A tool source whose one tool uploads ``screenshot_png`` and returns its reference."""

    source_name = "screenshots"

    def __init__(self, store: MediaStore) -> None:
        self._store = store

    async def list_tools(self) -> list[SourceToolDefinition]:
        return [_ScreenshotTool()]

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, ToolEffect.READ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        reference = await self._store.put_image(screenshot_png())
        return ToolExecutionResult(output="Took a screenshot.", images=[reference])

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        raise LookupError("take_screenshot starts no tool task")


class ImageModelScript:
    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir
        self._requests = InvocationLog(state_dir / MODEL_REQUESTS_LOG_NAME)

    async def respond(self, request: ModelRequest) -> ScriptedModelReply:
        self._requests.record(json.dumps(describe_model_request(request), sort_keys=True))
        command = ScriptedModelClient.user_texts(request)[-1]
        if command == "describe":
            count = sum(
                len(item.images)
                for item in request.input_items
                if isinstance(item, UserTextItem | ToolResultItem)
            )
            return ScriptedModelReply(text=f"saw {count} images")
        if command == "screenshot" or command.startswith("screenshot-hold "):
            if not ScriptedModelClient.tool_results(request):
                return ScriptedModelReply(
                    tool_calls=[ScriptedToolCall(tool_name=SCREENSHOT_TOOL_NAME)]
                )
            if command.startswith("screenshot-hold "):
                gate = FileGate(self._state_dir, command.removeprefix("screenshot-hold "))
                gate.mark_reached()
                await gate.wait_for_release()
            return ScriptedModelReply(text="screenshot taken")
        raise ValueError(f"unknown image command {command!r}")


def build_builtin_image_agent_flows(state_dir: Path) -> list[AgentSessionFlow]:
    """Build the agent with a media store and the same agent without one."""
    store = LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME)
    definition = AgentDefinition(
        name="image-agent",
        instructions="You look at images.",
        model=ScriptedModelClient(ImageModelScript(state_dir).respond),
        tool_sources=[ScreenshotToolSource(store)],
    )
    return [
        builtin_agent_session_flow(definition, flow_type=IMAGE_AGENT_FLOW_TYPE, media_store=store),
        builtin_agent_session_flow(definition, flow_type=NO_STORE_IMAGE_AGENT_FLOW_TYPE),
    ]


def build_builtin_image_agent_flows_from_environment() -> list[AgentSessionFlow]:
    return build_builtin_image_agent_flows(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))
