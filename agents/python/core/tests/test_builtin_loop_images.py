# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images through a built-in loop session: references in state, bytes in each model request."""

from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import AsyncIterator, Awaitable, Callable, Sequence
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest_asyncio

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentEventType,
    AgentImageReference,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSnapshot,
    FrameworkMessage,
    InMemoryMediaStore,
    LocalDirectoryMediaStore,
    MessageRole,
    ToolCallStatus,
    build_image_reference,
)
from dex_agents.builtin_loop import (
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ScriptedModelClient,
    ScriptedModelReply,
)
from dex_agents.testing import (
    AgentTestStack,
    FileGate,
    InvocationLog,
    wait_for_agent_snapshot,
    wait_until,
)
from tests.builtin_loop_image_agent import (
    FUNCTION_TOOL_STORES_LOG_NAME,
    MEDIA_DIRECTORY_NAME,
    MODEL_REQUESTS_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    STORE_READS_FAIL_FILE_NAME,
    STORE_READS_LOG_NAME,
    build_builtin_image_agent_flows,
    plain_result_json,
    screenshot_png,
)
from tests.conftest import CORE_DIR
from tests.fake_mcp_server import SCREENSHOT_PNG_BASE64

WORKER_MODULE = "tests.builtin_loop_image_worker"
IMAGES: dict[str, Any] = {
    case["name"]: case
    for case in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
}
PNG = base64.b64decode(IMAGES["png"]["base64"])
JPEG = base64.b64decode(IMAGES["jpeg"]["base64"])


@dataclass
class BuiltinImageHarness:
    stack: AgentTestStack
    sessions: AgentSessionClient
    no_store_sessions: AgentSessionClient
    flow: AgentSessionFlow
    no_store_flow: AgentSessionFlow
    state_dir: Path

    @property
    def media(self) -> LocalDirectoryMediaStore:
        return LocalDirectoryMediaStore(self.state_dir / MEDIA_DIRECTORY_NAME)

    def model_requests(self) -> list[dict[str, Any]]:
        names = InvocationLog(self.state_dir / MODEL_REQUESTS_LOG_NAME).names()
        return [json.loads(name) for name in names]

    def gate(self, name: str) -> FileGate:
        return FileGate(self.state_dir, name)

    async def wait_for_turns(self, session_id: str, turn_count: int) -> AgentSnapshot:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            else None,
            description=f"{turn_count} finished turn(s)",
        )


@pytest_asyncio.fixture
async def image_harness(tmp_path: Path) -> AsyncIterator[BuiltinImageHarness]:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flows: list[AgentSessionFlow] = build_builtin_image_agent_flows(state_dir)
    async with AgentTestStack(
        tmp_path,
        flows,
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield BuiltinImageHarness(
            stack, stack.sessions(flows[0]), stack.sessions(flows[1]), flows[0], flows[1], state_dir
        )


def image_seen(data: bytes, mime_type: str) -> dict[str, Any]:
    return {"mime_type": mime_type, "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}


def assert_no_image_bytes(snapshot: AgentSnapshot, *images: bytes) -> None:
    stored = json.dumps(
        [message.framework_item for message in snapshot.messages]
        + [record.output for record in snapshot.tool_calls]
    )
    for data in images:
        assert base64.b64encode(data).decode() not in stored


async def test_user_and_tool_images_reach_the_model_as_bytes_and_are_stored_as_references(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    png = await image_harness.media.put_image(PNG)
    jpeg = await image_harness.media.put_image(JPEG)
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id)

    assert await image_harness.sessions.send_user_message(
        session_id, "describe", images=[png, jpeg]
    )
    await image_harness.wait_for_turns(session_id, 1)
    assert await image_harness.sessions.send_user_message(session_id, "screenshot")
    snapshot = await image_harness.wait_for_turns(session_id, 2)

    screenshot = screenshot_png()
    requests = image_harness.model_requests()
    user_seen = {
        "kind": "user",
        "text": "describe",
        "images": [image_seen(PNG, "image/png"), image_seen(JPEG, "image/jpeg")],
    }
    assert requests[0]["items"] == [user_seen]
    # The second turn replays the first turn's images, then shows the tool's image with its result.
    assert requests[-1]["items"][0] == user_seen
    assert requests[-1]["items"][-1] == {
        "kind": "tool_result",
        "text": "Took a screenshot.",
        "images": [image_seen(screenshot, "image/png")],
    }
    assert snapshot.messages[-1].text == "screenshot taken"

    user_message = snapshot.messages[0]
    assert user_message.images == [png, jpeg]
    assert [image["sha256"] for image in json.loads(user_message.framework_item)["images"]] == [
        png.sha256,
        jpeg.sha256,
    ]
    tool_message = next(
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    )
    screenshot_sha256 = hashlib.sha256(screenshot).hexdigest()
    assert [image.sha256 for image in tool_message.images] == [screenshot_sha256]
    assert [image["sha256"] for image in json.loads(tool_message.framework_item)["images"]] == [
        screenshot_sha256
    ]
    assert_no_image_bytes(snapshot, PNG, JPEG, screenshot)


async def test_a_worker_killed_in_the_model_call_after_a_tool_image_rebuilds_the_same_input(
    image_harness: BuiltinImageHarness,
) -> None:
    worker = await image_harness.stack.start_worker()
    png = await image_harness.media.put_image(PNG)
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id)
    assert await image_harness.sessions.send_user_message(
        session_id, "screenshot-hold model-gate", images=[png]
    )
    gate = image_harness.gate("model-gate")
    await gate.wait_until_reached()
    before = image_harness.model_requests()

    worker.kill()
    await image_harness.stack.start_worker()
    await gate.wait_until_reached(count=2)
    gate.release()
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    after = image_harness.model_requests()
    # The new Worker read the same bytes from the store and built the identical request.
    assert len(before) == 2
    assert after[2] == before[1]
    assert after[2]["items"][0]["images"] == [image_seen(PNG, "image/png")]
    assert after[2]["items"][-1]["images"] == [image_seen(screenshot_png(), "image/png")]
    assert snapshot.messages[-1].text == "screenshot taken"
    assert_no_image_bytes(snapshot, PNG, screenshot_png())


async def test_a_runner_without_a_media_store_refuses_images_and_hides_tool_images(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    png = await image_harness.media.put_image(PNG)
    sessions = image_harness.no_store_sessions
    session_id = f"session-{uuid4().hex}"
    await sessions.start_session(session_id)

    assert not await sessions.send_user_message(session_id, "describe", images=[png])
    assert await sessions.send_user_message(session_id, "screenshot")
    snapshot = await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if snapshot.state is not None
        and snapshot.state.turn_count >= 1
        and snapshot.state.active_turn_id is None
        else None,
        description="1 finished turn",
    )

    assert [message.text for message in snapshot.messages if message.role is MessageRole.USER] == [
        "screenshot"
    ]
    # The tool's reference is stored for display, but the model saw only the text.
    tool_message = next(
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    )
    assert len(tool_message.images) == 1
    assert image_harness.model_requests()[-1]["items"][-1] == {
        "kind": "tool_result",
        "text": "Took a screenshot.",
        "images": [],
    }


async def test_an_mcp_image_reaches_the_model_input_as_bytes(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id, "mcp-screenshot")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    mcp_png = base64.b64decode(SCREENSHOT_PNG_BASE64)
    assert image_harness.model_requests()[-1]["items"][-1] == {
        "kind": "tool_result",
        "text": "captured the page\n[image 1]",
        "images": [image_seen(mcp_png, "image/png")],
    }
    assert snapshot.messages[-1].text == "mcp screenshot taken"
    tool_message = next(
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    )
    assert [image.sha256 for image in tool_message.images] == [hashlib.sha256(mcp_png).hexdigest()]
    assert_no_image_bytes(snapshot, mcp_png)


async def test_a_failed_store_read_fails_the_model_call_and_the_retry_succeeds(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    png = await image_harness.media.put_image(PNG)
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id)
    reads_fail = image_harness.state_dir / STORE_READS_FAIL_FILE_NAME
    reads_fail.touch()
    reads = InvocationLog(image_harness.state_dir / STORE_READS_LOG_NAME)

    assert await image_harness.sessions.send_user_message(session_id, "describe", images=[png])
    await wait_until_true(lambda: reads.count(f"read-failed {png.sha256}") >= 1)
    reads_fail.unlink()
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    # The failed read made no model call; the retried attempt showed the image.
    assert image_harness.model_requests() == [
        {
            "instructions": "You look at images.",
            "items": [
                {"kind": "user", "text": "describe", "images": [image_seen(PNG, "image/png")]}
            ],
        }
    ]
    assert snapshot.messages[-1].text == "saw 1 images"
    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    history = await image_harness.stack.read_step_history(session_id)
    assert max(entry.final_attempt for entry in history if entry.step_type == "RunModel") >= 2


async def test_a_continuation_after_tools_keeps_the_tool_image_and_adds_none(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id, "screenshot-continue")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    # The continuation message follows the tool result, whose image the next model call still shows.
    assert image_harness.model_requests()[-1]["items"][-2:] == [
        {
            "kind": "tool_result",
            "text": "Took a screenshot.",
            "images": [image_seen(screenshot_png(), "image/png")],
        },
        {"kind": "user", "text": "Describe the screenshot.", "images": []},
    ]
    assert snapshot.messages[-1].text == "described"
    continuation = snapshot.messages[-2]
    assert (continuation.role, continuation.text, continuation.images) == (
        MessageRole.USER,
        "Describe the screenshot.",
        [],
    )
    assert "images" not in json.loads(continuation.framework_item)


async def test_a_function_tool_uploads_through_the_context_store_and_the_model_sees_the_image(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id, "function-screenshot")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    screenshot = screenshot_png()
    screenshot_sha256 = hashlib.sha256(screenshot).hexdigest()
    assert InvocationLog(image_harness.state_dir / FUNCTION_TOOL_STORES_LOG_NAME).names() == [
        "store present attempt 1"
    ]
    # The model request after the call carries the bytes the tool uploaded.
    assert image_harness.model_requests()[-1]["items"][-1] == {
        "kind": "tool_result",
        "text": "Took a function screenshot.",
        "images": [image_seen(screenshot, "image/png")],
    }
    assert snapshot.messages[-1].text == "screenshot taken"
    tool_message = next(
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    )
    assert [image.sha256 for image in tool_message.images] == [screenshot_sha256]
    assert await image_harness.media.get_image(tool_message.images[0]) == screenshot
    assert [image["sha256"] for image in json.loads(tool_message.framework_item)["images"]] == [
        screenshot_sha256
    ]
    assert_no_image_bytes(snapshot, screenshot)


async def test_a_worker_killed_after_a_function_tool_image_rebuilds_the_same_input(
    image_harness: BuiltinImageHarness,
) -> None:
    worker = await image_harness.stack.start_worker()
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id)
    assert await image_harness.sessions.send_user_message(
        session_id, "function-screenshot-hold model-gate"
    )
    gate = image_harness.gate("model-gate")
    await gate.wait_until_reached()
    before = image_harness.model_requests()

    worker.kill()
    await image_harness.stack.start_worker()
    await gate.wait_until_reached(count=2)
    gate.release()
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    after = image_harness.model_requests()
    # The tool ran once; the new Worker replayed its stored reference into the identical request.
    assert len(before) == 2
    assert after[2] == before[1]
    assert after[2]["items"][-1]["images"] == [image_seen(screenshot_png(), "image/png")]
    assert InvocationLog(image_harness.state_dir / FUNCTION_TOOL_STORES_LOG_NAME).names() == [
        "store present attempt 1"
    ]
    assert snapshot.messages[-1].text == "screenshot taken"


async def test_a_function_tool_without_a_media_store_gets_none_and_its_images_fail_the_call_once(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    sessions = image_harness.no_store_sessions
    session_id = f"session-{uuid4().hex}"
    await sessions.start_session(session_id, "function-screenshot")
    snapshot = await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if snapshot.state is not None
        and snapshot.state.turn_count >= 1
        and snapshot.state.active_turn_id is None
        else None,
        description="1 finished turn",
    )

    # No retry could show the images, so the read fails on its only attempt.
    assert InvocationLog(image_harness.state_dir / FUNCTION_TOOL_STORES_LOG_NAME).names() == [
        "store absent attempt 1"
    ]
    events = await image_harness.stack.client.list_stream_messages(
        session_id, image_harness.no_store_flow.agent_events, 500
    )
    assert [
        json.loads(message.value.payload)["status"]
        for message in events.messages
        if message.value.type == AgentEventType.TOOL_CALL_FINISHED
    ] == [ToolCallStatus.FAILED]
    [tool_result] = [
        item
        for item in image_harness.model_requests()[-1]["items"]
        if item["kind"] == "tool_result"
    ]
    assert tool_result["images"] == []
    assert (
        "function tool take_function_screenshot returned images, but the runner has no media store"
        in tool_result["text"]
    )
    tool_message = next(
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    )
    assert tool_message.images == []


async def test_a_dict_with_a_function_tool_result_fields_stays_json_with_or_without_a_store(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    for label, sessions, flow in (
        ("store", image_harness.sessions, image_harness.flow),
        ("no-store", image_harness.no_store_sessions, image_harness.no_store_flow),
    ):
        for with_images in (False, True):
            session_id = f"session-plain-json-{label}-{with_images}-{uuid4().hex}"
            command = "function-plain-json-images" if with_images else "function-plain-json"
            await sessions.start_session(session_id, command)
            snapshot = await wait_for_agent_snapshot(
                sessions,
                session_id,
                lambda snapshot: snapshot
                if snapshot.state is not None
                and snapshot.state.turn_count >= 1
                and snapshot.state.active_turn_id is None
                else None,
                description="1 finished turn",
            )

            tool_message = next(
                message for message in snapshot.messages if message.role is MessageRole.TOOL
            )
            assert json.loads(tool_message.tool_output or "null") == plain_result_json(with_images)
            assert tool_message.images == []
            events = await image_harness.stack.client.list_stream_messages(
                session_id, flow.agent_events, 500
            )
            assert [
                json.loads(message.value.payload)["status"]
                for message in events.messages
                if message.value.type == AgentEventType.TOOL_CALL_FINISHED
            ] == [ToolCallStatus.SUCCEEDED]
            assert snapshot.messages[-1].text == "plain json returned"


async def test_a_function_tool_invalid_image_fails_like_a_tool_source_image(
    image_harness: BuiltinImageHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = f"session-{uuid4().hex}"
    await image_harness.sessions.start_session(session_id, "function-invalid-image")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    # As for a tool source, the session refuses the reference and the single attempt is unknown.
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_messages] == [OUTCOME_UNKNOWN_TOOL_RESULT]
    assert [message.images for message in tool_messages] == [[]]


async def wait_until_true(predicate: Callable[[], bool]) -> None:
    await wait_until("the condition", _as_async(predicate), timeout=timedelta(seconds=60))


def _as_async(predicate: Callable[[], bool]) -> Callable[[], Awaitable[bool]]:
    async def check() -> bool:
        return predicate()

    return check


def test_a_subclass_image_builder_stays_in_use_with_a_media_store() -> None:
    class CustomImageRunner(BuiltinLoopAgentRunner):
        def build_user_message_with_images(
            self, text: str, images: Sequence[AgentImageReference]
        ) -> FrameworkMessage:
            return FrameworkMessage(role=MessageRole.USER, text="custom", framework_item="{}")

    definition = AgentDefinition(
        name="subclass",
        instructions="Help.",
        model=ScriptedModelClient(lambda request: ScriptedModelReply(text="ok")),
    )
    store = InMemoryMediaStore("subclass")
    reference = build_image_reference(PNG, store.store_uri)

    custom = CustomImageRunner(definition, media_store=store)
    builtin = BuiltinLoopAgentRunner(definition, media_store=store)

    assert custom.build_user_message_with_images("hi", [reference]).text == "custom"
    stored = json.loads(builtin.build_user_message_with_images("hi", [reference]).framework_item)
    assert [image["sha256"] for image in stored["images"]] == [reference.sha256]
    assert not hasattr(BuiltinLoopAgentRunner(definition), "build_user_message_with_images")
