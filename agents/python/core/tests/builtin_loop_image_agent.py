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
* ``mcp-screenshot`` calls ``local__take_screenshot`` of an ``McpToolSource``
  with the media store, whose fake stdio server returns an image block, then
  answers ``mcp screenshot taken``.
* ``screenshot-continue`` calls ``take_screenshot`` with the text ``continue
  after tools``, so ``decide_turn_continuation`` continues the turn with the
  user text ``Describe the screenshot.``, which the model answers ``described``.
* ``function-screenshot`` and ``function-screenshot-hold <gate>`` work like
  ``screenshot`` and ``screenshot-hold``, but call ``take_function_screenshot``,
  a function tool that uploads the PNG through ``ToolCallContext.media_store``
  and returns a ``FunctionToolResult``. Without a store it returns a reference
  it built itself. It logs ``store present`` or ``store absent`` with its
  attempt in ``function-tool-stores.log``.
* ``function-invalid-image`` calls ``return_invalid_function_image``, a
  single-attempt write function tool that returns a reference with an
  unsupported MIME type.

Every model call appends one JSON line to ``model-requests.log``: each input
item's kind and text, with each image as its MIME type and the SHA-256 and
size of the bytes the request carries. ``BuiltinImageAgent`` has a
``LocalDirectoryMediaStore`` under the state directory, and
``BuiltinNoStoreImageAgent`` has none. While the file ``store-reads-fail``
exists in the state directory, every image read fails with
``MediaStoreError`` and is logged as ``read-failed <sha256>`` in
``store-reads.log``.
"""

from __future__ import annotations

import hashlib
import json
import os
import struct
import sys
import zlib
from dataclasses import asdict
from pathlib import Path
from typing import Any, ClassVar

from dex_agents import (
    AgentImageReference,
    AgentSessionFlow,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    LocalDirectoryMediaStore,
    MediaStore,
    MediaStoreError,
    ToolEffect,
    ToolExecutionResult,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    build_image_reference,
)
from dex_agents.builtin_loop import (
    AgentDefinition,
    ContinueTurn,
    FunctionTool,
    FunctionToolResult,
    ModelRequest,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    SourceToolDefinition,
    ToolCallContext,
    ToolResultItem,
    TurnContinuation,
    TurnContinuationContext,
    UserTextItem,
    builtin_agent_session_flow,
    function_tool,
)
from dex_agents.mcp import McpStdioServerConfig, McpToolSource
from dex_agents.testing import FileGate, InvocationLog
from tests.fake_mcp_server import STATE_DIR_ENVIRONMENT_VARIABLE as MCP_STATE_DIR_VARIABLE

IMAGE_AGENT_FLOW_TYPE = "BuiltinImageAgent"
NO_STORE_IMAGE_AGENT_FLOW_TYPE = "BuiltinNoStoreImageAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "BUILTIN_IMAGE_AGENT_STATE_DIR"
MODEL_REQUESTS_LOG_NAME = "model-requests.log"
MEDIA_DIRECTORY_NAME = "media"
SCREENSHOT_TOOL_NAME = "take_screenshot"
FUNCTION_SCREENSHOT_TOOL_NAME = "take_function_screenshot"
INVALID_FUNCTION_IMAGE_TOOL_NAME = "return_invalid_function_image"
FUNCTION_TOOL_STORES_LOG_NAME = "function-tool-stores.log"
NO_STORE_URI = "file:///no-media-store"
MCP_SCREENSHOT_TOOL_NAME = "local__take_screenshot"
STORE_READS_FAIL_FILE_NAME = "store-reads-fail"
STORE_READS_LOG_NAME = "store-reads.log"
CORE_DIR = Path(__file__).resolve().parents[1]
CONTINUE_AFTER_TOOLS_TEXT = "continue after tools"
CONTINUATION_MESSAGE = "Describe the screenshot."


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


class SwitchableReadMediaStore:
    """A media store whose reads fail while the test's flag file exists."""

    def __init__(self, store: MediaStore, state_dir: Path) -> None:
        self._store = store
        self._state_dir = state_dir
        self._reads = InvocationLog(state_dir / STORE_READS_LOG_NAME)

    async def put_image(self, data: bytes) -> AgentImageReference:
        return await self._store.put_image(data)

    async def get_image(self, reference: AgentImageReference) -> bytes:
        if (self._state_dir / STORE_READS_FAIL_FILE_NAME).exists():
            self._reads.record(f"read-failed {reference.sha256}")
            raise MediaStoreError(f"the test made reads of {reference.uri} fail")
        return await self._store.get_image(reference)


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
        tool_name = SCREENSHOT_TOOL_NAME
        if command.startswith("function-screenshot"):
            tool_name = FUNCTION_SCREENSHOT_TOOL_NAME
            command = command.removeprefix("function-")
        if command == "screenshot" or command.startswith("screenshot-hold "):
            if not ScriptedModelClient.tool_results(request):
                return ScriptedModelReply(tool_calls=[ScriptedToolCall(tool_name=tool_name)])
            if command.startswith("screenshot-hold "):
                gate = FileGate(self._state_dir, command.removeprefix("screenshot-hold "))
                gate.mark_reached()
                await gate.wait_for_release()
            return ScriptedModelReply(text="screenshot taken")
        if command == "function-invalid-image":
            if not ScriptedModelClient.tool_results(request):
                return ScriptedModelReply(
                    tool_calls=[ScriptedToolCall(tool_name=INVALID_FUNCTION_IMAGE_TOOL_NAME)]
                )
            return ScriptedModelReply(text="invalid image returned")
        if command == "screenshot-continue":
            return ScriptedModelReply(
                text=CONTINUE_AFTER_TOOLS_TEXT,
                tool_calls=[ScriptedToolCall(tool_name=SCREENSHOT_TOOL_NAME)],
            )
        if command == CONTINUATION_MESSAGE:
            return ScriptedModelReply(text="described")
        if command == "mcp-screenshot":
            if not ScriptedModelClient.tool_results(request):
                return ScriptedModelReply(
                    tool_calls=[ScriptedToolCall(tool_name=MCP_SCREENSHOT_TOOL_NAME)]
                )
            return ScriptedModelReply(text="mcp screenshot taken")
        raise ValueError(f"unknown image command {command!r}")


def build_function_image_tools(state_dir: Path) -> list[FunctionTool]:
    """Build the function tools that return images through ``FunctionToolResult``."""
    stores = InvocationLog(state_dir / FUNCTION_TOOL_STORES_LOG_NAME)

    @function_tool(effect=ToolEffect.READ, name=FUNCTION_SCREENSHOT_TOOL_NAME)
    async def take_function_screenshot(context: ToolCallContext) -> FunctionToolResult:
        """Take a screenshot of the screen."""
        if context.media_store is None:
            stores.record(f"store absent attempt {context.attempt}")
            reference = build_image_reference(screenshot_png(), NO_STORE_URI)
        else:
            stores.record(f"store present attempt {context.attempt}")
            reference = await context.media_store.put_image(screenshot_png())
        return FunctionToolResult(output="Took a function screenshot.", images=[reference])

    @function_tool(
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=False,
        name=INVALID_FUNCTION_IMAGE_TOOL_NAME,
    )
    async def return_invalid_function_image() -> FunctionToolResult:
        """Return an image reference with an unsupported MIME type."""
        reference = build_image_reference(screenshot_png(), NO_STORE_URI)
        invalid = AgentImageReference(**{**asdict(reference), "mime_type": "image/bmp"})
        return FunctionToolResult(output="returned a bad image", images=[invalid])

    return [take_function_screenshot, return_invalid_function_image]


def continue_after_tools(context: TurnContinuationContext) -> TurnContinuation:
    """Continue with a user message after the tools of a response that says so."""
    asks_to_continue = any(item.text == CONTINUE_AFTER_TOOLS_TEXT for item in context.model_output)
    if context.tool_results and asks_to_continue:
        return ContinueTurn(message=CONTINUATION_MESSAGE)
    return "default"


def build_builtin_image_agent_flows(state_dir: Path) -> list[AgentSessionFlow]:
    """Build the agent with a media store and the same agent without one."""
    store = SwitchableReadMediaStore(
        LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME), state_dir
    )
    mcp_tools = McpToolSource(
        [
            McpStdioServerConfig(
                name="local",
                command=sys.executable,
                args=["-m", "tests.fake_mcp_server", "stdio"],
                env={MCP_STATE_DIR_VARIABLE: str(state_dir)},
                cwd=str(CORE_DIR),
            )
        ],
        media_store=store,
    )
    definition = AgentDefinition(
        name="image-agent",
        instructions="You look at images.",
        model=ScriptedModelClient(ImageModelScript(state_dir).respond),
        tools=build_function_image_tools(state_dir),
        tool_sources=[ScreenshotToolSource(store), mcp_tools],
        decide_turn_continuation=continue_after_tools,
    )
    return [
        builtin_agent_session_flow(definition, flow_type=IMAGE_AGENT_FLOW_TYPE, media_store=store),
        builtin_agent_session_flow(definition, flow_type=NO_STORE_IMAGE_AGENT_FLOW_TYPE),
    ]


def build_builtin_image_agent_flows_from_environment() -> list[AgentSessionFlow]:
    return build_builtin_image_agent_flows(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))
