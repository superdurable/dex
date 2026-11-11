# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images reach the Pydantic AI model as bytes, while session state keeps only references."""

from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

import pytest_asyncio

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    AgentImageReference,
    AgentSnapshot,
    MessageRole,
    image_uri,
)
from dex_agents.testing import AgentTestStack, InvocationLog
from tests.conftest import PACKAGE_DIR, ScriptedAgentHarness
from tests.image_agent import (
    MODEL_INPUTS_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_image_agent_flow,
    build_media_store,
    chart_png,
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
    def model_inputs(self) -> list[list[dict[str, Any]]]:
        log = InvocationLog(self.state_dir / MODEL_INPUTS_LOG_NAME)
        return [json.loads(line) for line in log.names()]


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


def model_image(data: bytes, mime_type: str = "image/png") -> dict[str, str]:
    return {
        "image": mime_type,
        "sha256": hashlib.sha256(data).hexdigest(),
        "base64": base64.b64encode(data).decode(),
    }


def tool_image_reference(harness: ImageAgentHarness, data: bytes) -> AgentImageReference:
    sha256 = hashlib.sha256(data).hexdigest()
    return AgentImageReference(
        uri=image_uri(build_media_store(harness.state_dir).store_uri, sha256),
        mime_type="image/png",
        sha256=sha256,
        size_bytes=len(data),
    )


def assert_no_image_bytes_stored(snapshot: AgentSnapshot, *images: bytes) -> None:
    stored = json.dumps([message.framework_item for message in snapshot.messages])
    for data in images:
        assert base64.b64encode(data).decode() not in stored


async def test_user_images_reach_the_model_as_binary_content_and_replay_as_references(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    media_store = build_media_store(image_harness.state_dir)
    png = await media_store.put_image(PNG)
    jpeg = await media_store.put_image(JPEG)
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)

    assert await image_harness.sessions.send_user_message(
        session_id, "describe", images=[png, jpeg]
    )
    first = await image_harness.wait_for_turns(session_id, 1)
    assert await image_harness.sessions.send_user_message(session_id, "describe")
    second = await image_harness.wait_for_turns(session_id, 2)

    user_part = {
        "kind": "user",
        "content": ["describe", model_image(PNG), model_image(JPEG, "image/jpeg")],
    }
    assert image_harness.model_inputs() == [
        [user_part],
        [user_part, {"kind": "user", "content": ["describe"]}],
    ]
    assert first.messages[0].images == [png, jpeg]
    assert first.messages[0].text == "describe"
    assert first.messages[-1].text == "saw 2 images"
    assert second.messages[-1].text == "saw 2 images"
    assert png.uri in first.messages[0].framework_item
    assert_no_image_bytes_stored(second, PNG, JPEG)


async def test_a_tool_source_image_reaches_the_model_inside_the_tool_return(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id, "screenshot")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    screenshot = tool_image_reference(image_harness, screenshot_png())
    assert image_harness.model_inputs()[-1] == [
        {"kind": "user", "content": ["screenshot"]},
        {
            "kind": "tool-return",
            "tool_name": "take_screenshot",
            "content": ["took a screenshot", model_image(screenshot_png())],
        },
    ]
    assert snapshot.messages[-1].text == "take_screenshot: took a screenshot with 1 images"
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.images for message in tool_messages] == [[screenshot]]
    assert [message.tool_output for message in tool_messages] == ["took a screenshot"]
    assert_no_image_bytes_stored(snapshot, screenshot_png())


async def test_a_tool_that_returns_binary_content_records_the_image_as_a_reference(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id, "chart")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    chart = tool_image_reference(image_harness, chart_png())
    assert image_harness.model_inputs()[-1][-1] == {
        "kind": "tool-return",
        "tool_name": "render_chart",
        "content": ["rendered a chart", model_image(chart_png())],
    }
    assert snapshot.messages[-1].text == "render_chart: rendered a chart with 1 images"
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.images for message in tool_messages] == [[chart]]
    assert [message.tool_output for message in tool_messages] == ["rendered a chart"]
    assert_no_image_bytes_stored(snapshot, chart_png())


async def test_a_worker_killed_in_the_resumed_model_call_rebuilds_the_same_model_input(
    image_harness: ImageAgentHarness,
) -> None:
    worker = await image_harness.stack.start_worker()
    png = await build_media_store(image_harness.state_dir).put_image(PNG)
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)
    assert await image_harness.sessions.send_user_message(
        session_id, "screenshot-stall model-gate", images=[png]
    )
    gate = image_harness.gate("model-gate")
    await gate.wait_until_reached()
    run_state = (await image_harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE))[
        FRAMEWORK_RUN_STATE
    ]
    assert png.uri in run_state
    assert base64.b64encode(PNG).decode() not in run_state

    worker.kill()
    gate.release()
    await image_harness.stack.start_worker()
    first = await image_harness.wait_for_turns(session_id, 1)
    assert await image_harness.sessions.send_user_message(session_id, "describe")
    second = await image_harness.wait_for_turns(session_id, 2)

    turn_input = [
        {"kind": "user", "content": ["screenshot-stall model-gate", model_image(PNG)]},
        {
            "kind": "tool-return",
            "tool_name": "take_screenshot",
            "content": ["took a screenshot", model_image(screenshot_png())],
        },
    ]
    inputs = image_harness.model_inputs()
    assert len(inputs) == 4
    # The killed attempt and its retry on the new Worker sent the same input.
    assert inputs[1] == turn_input
    assert inputs[2] == turn_input
    assert inputs[3] == [*turn_input, {"kind": "user", "content": ["describe"]}]
    assert first.messages[-1].text == "take_screenshot: took a screenshot with 1 images"
    assert second.messages[-1].text == "saw 1 images"
    assert_no_image_bytes_stored(second, PNG, screenshot_png())


async def test_a_runner_without_a_media_store_refuses_images(
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
    assert snapshot.messages[-1].text == "said: hi"
    assert all(message.images == [] for message in snapshot.messages)
