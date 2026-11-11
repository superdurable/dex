# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images reach the model as SDK image parts, while session state holds only references."""

from __future__ import annotations

import base64
import json
from collections.abc import AsyncIterator
from pathlib import Path

import pytest_asyncio

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    AgentImageReference,
    InMemoryMediaStore,
    MessageRole,
)
from dex_agents.testing import AgentTestStack, InvocationLog
from tests.conftest import PACKAGE_DIR, SupportAgentHarness
from tests.image_agent import (
    INPUT_FILTER_LOG_NAME,
    MODEL_REQUEST_LOG_NAME,
    SCREENSHOT_JPEG,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    USER_PNG,
    build_image_agent_flow,
    build_media_store,
)

IMAGE_WORKER_MODULE = "tests.image_worker"
USER_PNG_URL = f"data:image/png;base64,{base64.b64encode(USER_PNG).decode()}"
SCREENSHOT_JPEG_URL = f"data:image/jpeg;base64,{base64.b64encode(SCREENSHOT_JPEG).decode()}"


class ImageAgentHarness(SupportAgentHarness):
    async def upload_user_image(self) -> AgentImageReference:
        return await build_media_store(self.state_dir).put_image(USER_PNG)

    def request_lines(self, call_name: str) -> list[str]:
        prefix = f"{call_name} "
        log = InvocationLog(self.state_dir / MODEL_REQUEST_LOG_NAME)
        return [line.removeprefix(prefix) for line in log.names() if line.startswith(prefix)]

    def request_images(self, call_name: str) -> list[str]:
        return [
            line.removeprefix("image ")
            for line in self.request_lines(call_name)
            if line.startswith("image ")
        ]

    def request_digests(self, call_name: str) -> list[str]:
        return [
            line.removeprefix("input ")
            for line in self.request_lines(call_name)
            if line.startswith("input ")
        ]

    def input_filter_records(self) -> list[str]:
        return InvocationLog(self.state_dir / INPUT_FILTER_LOG_NAME).names()


@pytest_asyncio.fixture
async def image_harness(tmp_path: Path) -> AsyncIterator[ImageAgentHarness]:
    state_dir = tmp_path / "image-state"
    state_dir.mkdir()
    flow = build_image_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        IMAGE_WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield ImageAgentHarness(stack, flow, stack.sessions(flow), state_dir)


async def test_user_and_tool_images_reach_the_model_as_bytes_and_are_stored_as_references(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    png = await image_harness.upload_user_image()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)

    assert await image_harness.sessions.send_user_message(
        session_id,
        'call take_screenshot {} then call lookup {"query": "q", "gate": "lookup-gate"}',
        images=[png],
    )
    gate = image_harness.gate("lookup-gate")
    await gate.wait_until_reached()
    attributes = await image_harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    run_state = attributes[FRAMEWORK_RUN_STATE]
    gate.release()
    first = await image_harness.wait_for_turns(session_id, 1)
    assert await image_harness.sessions.send_user_message(session_id, "say again")
    second = await image_harness.wait_for_turns(session_id, 2)

    assert first.state is not None and first.state.last_turn_error is None
    assert first.messages[-1].text == "take_screenshot: took a screenshot; lookup: found q"
    # Uploading the same bytes again returns the reference the tool stored.
    screenshot = await build_media_store(image_harness.state_dir).put_image(SCREENSHOT_JPEG)
    assert image_harness.request_images("model:1:1") == [f"user {USER_PNG_URL}"]
    tool_image = f"call-1-0-0 {SCREENSHOT_JPEG_URL}"
    assert image_harness.request_images("model:1:2") == [f"user {USER_PNG_URL}", tool_image]
    assert image_harness.request_images("model:1:3") == [f"user {USER_PNG_URL}", tool_image]
    # The second turn rebuilds its input from the stored items alone.
    assert image_harness.request_images("model:2:1") == [f"user {USER_PNG_URL}", tool_image]
    assert second.messages[-1].text == "said: again"
    # The application's own filter runs first and sees only placeholders.
    assert image_harness.input_filter_records() == [
        "images 1 bytes False",
        "images 2 bytes False",
        "images 2 bytes False",
        "images 2 bytes False",
    ]

    user_message = first.messages[0]
    assert user_message.images == [png]
    assert json.loads(user_message.framework_item)["content"] == [
        {"type": "input_text", "text": user_message.text},
        {"type": "input_image", "image_url": png.uri, "detail": "auto"},
    ]
    tool_message = next(
        message
        for message in first.messages
        if message.role is MessageRole.TOOL and message.tool_name == "take_screenshot"
    )
    assert tool_message.tool_output == "took a screenshot"
    assert tool_message.images == [screenshot]
    assert json.loads(tool_message.framework_item)["output"] == [
        {"type": "input_text", "text": "took a screenshot"},
        {"type": "input_image", "image_url": screenshot.uri, "detail": "auto"},
    ]
    assert png.uri in run_state and screenshot.uri in run_state
    stored = json.dumps([message.framework_item for message in second.messages]) + run_state
    for image in (USER_PNG, SCREENSHOT_JPEG):
        assert base64.b64encode(image).decode() not in stored


async def test_a_worker_killed_in_a_resumed_model_call_rebuilds_the_same_model_input(
    image_harness: ImageAgentHarness,
) -> None:
    worker = await image_harness.stack.start_worker()
    png = await image_harness.upload_user_image()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)

    assert await image_harness.sessions.send_user_message(
        session_id, "call take_screenshot {} then stall model-gate", images=[png]
    )
    gate = image_harness.gate("model-gate")
    await gate.wait_until_reached()
    worker.kill()
    gate.release()
    await image_harness.stack.start_worker()
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "stalled at model-gate: take_screenshot: took a screenshot"
    assert image_harness.model_calls() == ["model:1:1", "model:1:2", "model:1:2"]
    first_attempt, second_attempt = image_harness.request_digests("model:1:2")
    assert first_attempt == second_attempt
    images = image_harness.request_images("model:1:2")
    tool_image = f"call-1-0-0 {SCREENSHOT_JPEG_URL}"
    assert images == [f"user {USER_PNG_URL}", tool_image, f"user {USER_PNG_URL}", tool_image]
    assert image_harness.invocations.count("take_screenshot") == 1


async def test_a_runner_without_a_media_store_refuses_images(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    reference = await InMemoryMediaStore().put_image(USER_PNG)

    assert not await harness.sessions.send_user_message(session_id, "say hi", images=[reference])
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert [message.text for message in snapshot.messages] == ["say hi", "said: hi"]
    assert [message.images for message in snapshot.messages] == [[], []]
