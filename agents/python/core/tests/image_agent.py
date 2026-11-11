# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted runner that takes images, for the image session tests.

The model reads one command from the user message:

* ``describe`` loads every image of the replayed framework items and answers
  ``saw <n> images``.
* ``screenshot`` asks for ``take_screenshot``, which uploads a PNG and returns
  it as a reference. The second model call loads the images and answers.
* ``screenshot-gate <gate>`` does the same, but the tool's first attempt waits
  at that ``FileGate`` after the upload.
* ``screenshot-hold <gate>`` waits at that gate in the second model call, so a
  test can read the tool call record.
* ``bad-screenshot`` asks for ``return_invalid_image``, a single-attempt write
  whose result carries an invalid reference.
* ``bad-answer-images`` answers with an item whose ``images`` are invalid.
* ``bad-incremental-images`` asks for ``take_screenshot`` and stores an item
  with invalid ``images`` through ``ToolCallsRequested.new_messages``.
* ``screenshot-then-fail`` asks for ``take_screenshot``, then fails the second
  model call; ``build_unfinished_turn_messages`` returns the tool result with
  invalid ``images``.
* ``stall <gate>`` waits at that gate, then answers.

Every model call records ``<turn_id>:<model call>:<sha256>:<size>,...`` in
``image-loads.log`` for the bytes it loaded, and every upload records
``upload <sha256> attempt <n>`` in ``uploads.log``. The store is a
``LocalDirectoryMediaStore`` under the state directory, or an ``S3MediaStore``
when ``IMAGE_AGENT_S3_ENDPOINT`` and ``IMAGE_AGENT_S3_BUCKET`` are set.
"""

from __future__ import annotations

import json
import os
import struct
import zlib
from collections.abc import Sequence
from dataclasses import asdict, replace
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentImageReference,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolCall,
    DeferredToolExecution,
    FrameworkMessage,
    LocalDirectoryMediaStore,
    MediaStore,
    MessageRole,
    ToolCallsRequested,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    describe_tool_call_outcome,
    load_images,
)
from dex_agents.testing import FileGate, InvocationLog

IMAGE_AGENT_FLOW_TYPE = "ImageAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "IMAGE_AGENT_STATE_DIR"
S3_ENDPOINT_ENVIRONMENT_VARIABLE = "IMAGE_AGENT_S3_ENDPOINT"
S3_BUCKET_ENVIRONMENT_VARIABLE = "IMAGE_AGENT_S3_BUCKET"
S3_PREFIX = "agent-images"
IMAGE_LOADS_LOG_NAME = "image-loads.log"
UPLOADS_LOG_NAME = "uploads.log"
MEDIA_DIRECTORY_NAME = "media"
MODEL_CALL_USAGE = AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)
S3LOCAL_ACCESS_KEY_ID = "s3local"
S3LOCAL_SECRET_ACCESS_KEY = "s3local"
TOOL_POLICIES = {
    "take_screenshot": ToolPolicy(ToolEffect.READ),
    "return_invalid_image": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


def screenshot_png() -> bytes:
    """Return the 2x2 PNG that ``take_screenshot`` uploads, the same bytes on every call."""

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


def build_media_store(state_dir: Path) -> MediaStore:
    endpoint = os.environ.get(S3_ENDPOINT_ENVIRONMENT_VARIABLE)
    if endpoint is None:
        return LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME)
    # Imported here, so the default store needs no boto3.
    from dex_agents.s3 import S3MediaStore

    return S3MediaStore(
        os.environ[S3_BUCKET_ENVIRONMENT_VARIABLE],
        S3_PREFIX,
        endpoint_url=endpoint,
        region_name="us-east-1",
        force_path_style=True,
        access_key_id=S3LOCAL_ACCESS_KEY_ID,
        secret_access_key=S3LOCAL_SECRET_ACCESS_KEY,
    )


def invalid_image(reference: AgentImageReference) -> AgentImageReference:
    """Return the reference with an unsupported type, which validation refuses."""
    return AgentImageReference(**{**asdict(reference), "mime_type": "image/bmp"})


class ImageRunner:
    def __init__(self, state_dir: Path, store: MediaStore) -> None:
        self._state_dir = state_dir
        self._store = store
        self._image_loads = InvocationLog(state_dir / IMAGE_LOADS_LOG_NAME)
        self._uploads = InvocationLog(state_dir / UPLOADS_LOG_NAME)

    def build_user_message(self, text: str) -> FrameworkMessage:
        return self.build_user_message_with_images(text, ())

    def build_user_message_with_images(
        self, text: str, images: Sequence[AgentImageReference]
    ) -> FrameworkMessage:
        item: dict[str, Any] = {"type": "user", "text": text}
        if images:
            item["images"] = [asdict(image) for image in images]
        return FrameworkMessage(role=MessageRole.USER, text=text, framework_item=json.dumps(item))

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        if turn.framework_run_state is not None:
            return await self._continue_after_tool_call(turn, json.loads(turn.framework_run_state))
        await self._load_turn_images(turn, model_call=1, extra=())
        command = _current_user_text(turn)
        if command == "describe":
            return _answer(f"saw {len(_replayed_images(turn))} images")
        if command.startswith("stall "):
            gate = FileGate(self._state_dir, command.removeprefix("stall "))
            gate.mark_reached()
            await gate.wait_for_release()
            return _answer("stalled")
        if command == "bad-screenshot":
            return _request_tool(turn, command, "return_invalid_image", {})
        if command == "bad-answer-images":
            reference = await self._store.put_image(screenshot_png())
            answer = _answer("here is a bad image")
            message = replace(answer.new_messages[0], images=[invalid_image(reference)])
            return replace(answer, new_messages=[message])
        if command == "bad-incremental-images":
            reference = await self._store.put_image(screenshot_png())
            requested = _request_tool(turn, command, "take_screenshot", {})
            item = FrameworkMessage(
                role=MessageRole.ASSISTANT,
                text="calling",
                framework_item=json.dumps({"type": "assistant", "text": "calling"}),
                images=[invalid_image(reference)],
            )
            return replace(requested, new_messages=[item])
        if command in ("screenshot", "screenshot-then-fail") or command.startswith(
            ("screenshot-gate ", "screenshot-hold ")
        ):
            arguments = (
                {"gate": command.split(" ", 1)[1]} if command.startswith("screenshot-gate ") else {}
            )
            return _request_tool(turn, command, "take_screenshot", arguments)
        raise ValueError(f"unknown image command {command!r}")

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        record = call.tool_call
        arguments = json.loads(record.arguments)
        if record.tool_name == "return_invalid_image":
            reference = await self._store.put_image(screenshot_png())
            return ToolExecutionResult(
                output="returned a bad image",
                images=[AgentImageReference(**{**asdict(reference), "mime_type": "image/bmp"})],
            )
        if record.tool_name != "take_screenshot":
            raise ToolExecutionError(f"unknown tool {record.tool_name}")
        reference = await self._store.put_image(screenshot_png())
        self._uploads.record(f"upload {reference.sha256} attempt {call.attempt}")
        if "gate" in arguments and call.attempt == 1:
            gate = FileGate(self._state_dir, str(arguments["gate"]))
            gate.mark_reached()
            await gate.wait_for_release()
        return ToolExecutionResult(output="took a screenshot", images=[reference])

    async def _continue_after_tool_call(
        self, turn: AgentTurnContext, state: dict[str, Any]
    ) -> AgentTurnOutcome:
        command = str(state["command"])
        tool_call_id = str(state["tool_call_id"])
        record = turn.recorded_tool_call(tool_call_id)
        if record is None:
            raise RuntimeError(f"no record for tool call {tool_call_id}")
        await self._load_turn_images(turn, model_call=2, extra=record.images)
        if command == "screenshot-then-fail":
            raise RuntimeError("scripted failure after the screenshot")
        if command.startswith("screenshot-hold "):
            gate = FileGate(self._state_dir, command.removeprefix("screenshot-hold "))
            gate.mark_reached()
            await gate.wait_for_release()
        output = describe_tool_call_outcome(record)
        tool_result: dict[str, Any] = {
            "type": "tool_result",
            "tool_call_id": tool_call_id,
            "output": output,
        }
        if record.images:
            tool_result["images"] = [asdict(image) for image in record.images]
        return TurnCompleted(
            new_messages=[
                FrameworkMessage(
                    role=MessageRole.ASSISTANT,
                    tool_call_id=tool_call_id,
                    tool_name=record.tool_name,
                    tool_arguments=record.arguments,
                    framework_item=json.dumps({"type": "tool_call", "tool_call_id": tool_call_id}),
                ),
                FrameworkMessage(
                    role=MessageRole.TOOL,
                    tool_call_id=tool_call_id,
                    tool_name=record.tool_name,
                    tool_output=output,
                    framework_item=json.dumps(tool_result),
                    images=record.images,
                ),
                FrameworkMessage(
                    role=MessageRole.ASSISTANT,
                    text=f"{record.tool_name}: {output}",
                    framework_item=json.dumps({"type": "assistant", "text": output}),
                ),
            ],
            usage=MODEL_CALL_USAGE,
        )

    def build_unfinished_turn_messages(self, turn: AgentTurnContext) -> list[FrameworkMessage]:
        if _current_user_text(turn) != "screenshot-then-fail":
            return []
        record = turn.recorded_tool_call(f"{turn.turn_id}-call-0")
        if record is None or not record.images:
            return []
        return [
            FrameworkMessage(
                role=MessageRole.TOOL,
                tool_call_id=record.tool_call_id,
                tool_name=record.tool_name,
                tool_output=describe_tool_call_outcome(record),
                framework_item=json.dumps({"type": "tool_result"}),
                images=[invalid_image(image) for image in record.images],
            )
        ]

    async def _load_turn_images(
        self, turn: AgentTurnContext, model_call: int, extra: Sequence[AgentImageReference]
    ) -> None:
        references = [*_replayed_images(turn), *extra]
        images = await load_images(self._store, references)
        loaded = ",".join(f"{sha256}:{len(data)}" for sha256, data in images.items())
        self._image_loads.record(f"{turn.turn_id}:{model_call}:{loaded}")


def build_image_agent_flow(state_dir: Path) -> AgentSessionFlow:
    return AgentSessionFlow(
        ImageRunner(state_dir, build_media_store(state_dir)),
        flow_type=IMAGE_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(tool_policies=TOOL_POLICIES),
    )


def build_image_agent_flow_from_environment() -> AgentSessionFlow:
    return build_image_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def _replayed_images(turn: AgentTurnContext) -> list[AgentImageReference]:
    """Return the images in the stored framework items, the replay truth, in order."""
    return [
        AgentImageReference(**image)
        for message in turn.messages
        for image in json.loads(message.framework_item).get("images", [])
    ]


def _current_user_text(turn: AgentTurnContext) -> str:
    for message in reversed(turn.messages):
        if message.role is MessageRole.USER and message.turn_id == turn.turn_id:
            text: str = json.loads(message.framework_item)["text"]
            return text
    raise RuntimeError(f"turn {turn.turn_id} has no user message")


def _request_tool(
    turn: AgentTurnContext, command: str, tool_name: str, arguments: dict[str, Any]
) -> ToolCallsRequested:
    tool_call_id = f"{turn.turn_id}-call-0"
    return ToolCallsRequested(
        framework_run_state=json.dumps({"command": command, "tool_call_id": tool_call_id}),
        tool_calls=[
            DeferredToolCall(
                tool_call_id=tool_call_id,
                tool_name=tool_name,
                arguments=json.dumps(arguments),
                policy=turn.resolve_tool_policy(tool_name),
            )
        ],
        usage=MODEL_CALL_USAGE,
    )


def _answer(text: str) -> TurnCompleted:
    return TurnCompleted(
        new_messages=[
            FrameworkMessage(
                role=MessageRole.ASSISTANT,
                text=text,
                framework_item=json.dumps({"type": "assistant", "text": text}),
            )
        ],
        usage=MODEL_CALL_USAGE,
    )
