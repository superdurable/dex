# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images through a real session: stored as references, replayed, and uploaded once."""

from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import AsyncIterator
from dataclasses import asdict, replace
from pathlib import Path
from typing import Any

import pytest
import pytest_asyncio

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentImageReference,
    AgentUserMessage,
    ImageValidationError,
    MessageRole,
    image_uri,
)
from dex_agents.testing import AgentTestStack, InvocationLog, wait_for_agent_snapshot
from tests.conftest import CORE_DIR, ScriptedAgentHarness
from tests.directory_media_store import DirectoryMediaStore
from tests.image_agent import (
    IMAGE_LOADS_LOG_NAME,
    MEDIA_DIRECTORY_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    UPLOADS_LOG_NAME,
    build_image_agent_flow,
    screenshot_png,
)

IMAGE_WORKER_MODULE = "tests.image_worker"
IMAGES: dict[str, Any] = {
    case["name"]: case
    for case in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
}
PNG = base64.b64decode(IMAGES["png"]["base64"])
JPEG = base64.b64decode(IMAGES["jpeg"]["base64"])


class ImageAgentHarness(ScriptedAgentHarness):
    @property
    def media(self) -> DirectoryMediaStore:
        return DirectoryMediaStore(self.state_dir / MEDIA_DIRECTORY_NAME)

    @property
    def image_loads(self) -> InvocationLog:
        return InvocationLog(self.state_dir / IMAGE_LOADS_LOG_NAME)

    @property
    def uploads(self) -> InvocationLog:
        return InvocationLog(self.state_dir / UPLOADS_LOG_NAME)


@pytest_asyncio.fixture
async def image_harness(tmp_path: Path) -> AsyncIterator[ImageAgentHarness]:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_image_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        IMAGE_WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield ImageAgentHarness(stack, flow, stack.sessions(flow), state_dir)


def loaded(reference: AgentImageReference) -> str:
    return f"{reference.sha256}:{reference.size_bytes}"


async def test_a_user_message_with_images_is_stored_as_references_and_replays(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    png = await image_harness.media.put_image(PNG)
    jpeg = await image_harness.media.put_image(JPEG)
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)

    assert await image_harness.sessions.send_user_message(
        session_id, "describe", images=[png, jpeg]
    )
    first = await image_harness.wait_for_turns(session_id, 1)
    assert await image_harness.sessions.send_user_message(session_id, "describe")
    second = await image_harness.wait_for_turns(session_id, 2)

    user_message = first.messages[0]
    assert user_message.role is MessageRole.USER
    assert user_message.images == [png, jpeg]
    assert json.loads(user_message.framework_item)["images"] == [
        asdict(image) for image in (png, jpeg)
    ]
    assert first.messages[-1].text == "saw 2 images"
    assert second.messages[-1].text == "saw 2 images"
    assert second.messages[2].images == []
    # Every model call read the referenced bytes back, the second turn from replayed history.
    assert image_harness.image_loads.names() == [
        f"turn-1:1:{loaded(png)},{loaded(jpeg)}",
        f"turn-2:1:{loaded(png)},{loaded(jpeg)}",
    ]
    stored = json.dumps([message.framework_item for message in second.messages])
    assert base64.b64encode(PNG).decode() not in stored
    assert len(image_harness.media.image_files) == 2


async def test_a_queued_message_keeps_its_images_when_steered(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    png = await image_harness.media.put_image(PNG)
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id, "stall model-gate")
    gate = image_harness.gate("model-gate")
    await gate.wait_until_reached()
    assert await image_harness.sessions.send_user_message(session_id, "describe", images=[png])
    queued = await wait_for_agent_snapshot(
        image_harness.sessions,
        session_id,
        lambda snapshot: snapshot if snapshot.queued_messages else None,
        description="a queued message",
    )
    assert queued.queued_messages[0].message.images == [png]

    assert await image_harness.sessions.steer_user_message(
        session_id, message_id=queued.queued_messages[0].message_id
    )
    gate.release()
    snapshot = await image_harness.wait_for_turns(session_id, 2)

    steered = [message for message in snapshot.messages if message.text == "describe"]
    assert [message.images for message in steered] == [[png]]
    assert snapshot.messages[-1].text == "saw 1 images"


async def test_a_tool_image_is_stored_as_a_reference_on_the_record_and_the_message(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id, "screenshot-hold hold-gate")
    gate = image_harness.gate("hold-gate")
    await gate.wait_until_reached()
    expected_sha256 = hashlib.sha256(screenshot_png()).hexdigest()
    expected = AgentImageReference(
        uri=image_uri(image_harness.media.store_uri, expected_sha256),
        mime_type="image/png",
        sha256=expected_sha256,
        size_bytes=len(screenshot_png()),
    )

    held = await image_harness.sessions.get_agent_snapshot(session_id)
    assert [record.images for record in held.tool_calls] == [[expected]]
    gate.release()
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.images for message in tool_messages] == [[expected]]
    assert json.loads(tool_messages[0].framework_item)["images"][0]["sha256"] == expected_sha256
    assert image_harness.image_loads.names() == ["turn-1:1:", f"turn-1:2:{loaded(expected)}"]


async def test_a_worker_killed_after_the_upload_uploads_the_same_single_object_again(
    image_harness: ImageAgentHarness,
) -> None:
    worker = await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id, "screenshot-gate upload-gate")
    await image_harness.gate("upload-gate").wait_until_reached()

    worker.kill()
    await image_harness.stack.start_worker()
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    sha256 = hashlib.sha256(screenshot_png()).hexdigest()
    assert image_harness.uploads.names() == [
        f"upload {sha256} attempt 1",
        f"upload {sha256} attempt 2",
    ]
    assert [path.name for path in image_harness.media.image_files] == [sha256]
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [[image.sha256 for image in message.images] for message in tool_messages] == [[sha256]]
    assert tool_messages[0].tool_output == "took a screenshot"


async def test_a_tool_result_with_an_invalid_image_fails_the_attempt(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id, "bad-screenshot")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_messages] == [OUTCOME_UNKNOWN_TOOL_RESULT]
    assert [message.images for message in tool_messages] == [[]]


async def test_invalid_images_are_refused_before_and_by_the_session(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    png = await image_harness.media.put_image(PNG)
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)
    oversized = replace(png, size_bytes=3_750_001)

    with pytest.raises(ImageValidationError) as raised:
        await image_harness.sessions.send_user_message(session_id, "describe", images=[oversized])
    assert str(raised.value) == "images[0]: image reference size_bytes must be from 1 to 3750000"
    with pytest.raises(ImageValidationError, match="at most 20 images, got 21"):
        await image_harness.sessions.send_user_message(session_id, "describe", images=[png] * 21)
    client = image_harness.stack.client
    send = image_harness.flow.send_user_message
    for images in (
        [oversized],
        [replace(png, mime_type="image/svg+xml")],
        [replace(png, uri=image_uri("s3://elsewhere", "0" * 64))],
        [png] * 21,
    ):
        assert not await client.invoke_rpc(
            send, session_id, AgentUserMessage(text="describe", images=images)
        )
    snapshot = await image_harness.sessions.get_agent_snapshot(session_id)
    assert snapshot.queued_messages == []
    assert snapshot.messages == []


async def test_a_runner_without_image_support_refuses_images(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    reference = AgentImageReference(
        uri=image_uri("memory://default", IMAGES["png"]["sha256"]),
        mime_type="image/png",
        sha256=IMAGES["png"]["sha256"],
        size_bytes=IMAGES["png"]["size_bytes"],
    )

    assert not await harness.sessions.send_user_message(session_id, "say hi", images=[reference])
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert [message.images for message in snapshot.messages] == [[], []]
