# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images reach the chat model as base64 blocks, while session state keeps only references."""

from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

import pytest
import pytest_asyncio
from dex_agents import (
    FRAMEWORK_RUN_STATE,
    AgentImageReference,
    AgentSnapshot,
    MessageRole,
    image_uri,
)
from dex_agents.testing import AgentTestStack, InvocationLog
from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer

from dex_agents_langgraph._checkpoints import decode_suspended_graph_run_state
from tests.conftest import PACKAGE_DIR, ScriptedAgentHarness
from tests.image_agents import (
    IMAGE_AGENT_FLOW_TYPES,
    IMAGE_CREATE_AGENT_FLOW_TYPE,
    MODEL_INPUTS_LOG_NAME,
    NOT_AN_IMAGE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_image_agent_flows,
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


@pytest.fixture
def image_flow_type() -> str:
    """Selects the agent under test; tests parametrize it with IMAGE_AGENT_FLOW_TYPES."""
    return IMAGE_CREATE_AGENT_FLOW_TYPE


@pytest_asyncio.fixture
async def image_harness(tmp_path: Path, image_flow_type: str) -> AsyncIterator[ImageAgentHarness]:
    state_dir = tmp_path / "image-state"
    state_dir.mkdir()
    flows = build_image_agent_flows(state_dir)
    flow = next(flow for flow in flows if flow.get_flow_type() == image_flow_type)
    async with AgentTestStack(
        tmp_path,
        flows,
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


def decode_run_state(run_state: str) -> str:
    """Return the checkpoints and pending writes of a stored run state as readable text."""
    serde = JsonPlusSerializer()
    decoded: list[Any] = []
    for stored in decode_suspended_graph_run_state(run_state).checkpoints:
        for encoded in [
            stored["checkpoint"],
            *(write["value"] for write in stored["pending_writes"]),
        ]:
            decoded.append(serde.loads_typed((encoded["type"], base64.b64decode(encoded["data"]))))
    return repr(decoded)


@pytest.mark.parametrize("image_flow_type", IMAGE_AGENT_FLOW_TYPES)
async def test_user_images_reach_the_model_as_base64_blocks_and_replay_as_references(
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

    user_message = {
        "kind": "human",
        "content": ["describe", model_image(PNG), model_image(JPEG, "image/jpeg")],
    }
    assert image_harness.model_inputs() == [
        [user_message],
        [user_message, {"kind": "human", "content": ["describe"]}],
    ]
    assert first.messages[0].images == [png, jpeg]
    assert first.messages[0].text == "describe"
    assert first.messages[-1].text == "saw 2 images"
    assert second.messages[-1].text == "saw 2 images"
    assert png.uri in first.messages[0].framework_item
    assert_no_image_bytes_stored(second, PNG, JPEG)


@pytest.mark.parametrize("image_flow_type", IMAGE_AGENT_FLOW_TYPES)
async def test_a_tool_source_image_reaches_the_model_inside_the_tool_message(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id, "call take_screenshot {}")
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    screenshot = tool_image_reference(image_harness, screenshot_png())
    assert image_harness.model_inputs()[-1] == [
        {"kind": "human", "content": ["call take_screenshot {}"]},
        {"kind": "tool", "content": ["took a screenshot", model_image(screenshot_png())]},
    ]
    assert snapshot.messages[-1].text == "take_screenshot: took a screenshot"
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.images for message in tool_messages] == [[screenshot]]
    assert [message.tool_output for message in tool_messages] == ["took a screenshot"]
    assert_no_image_bytes_stored(snapshot, screenshot_png())


@pytest.mark.parametrize("image_flow_type", IMAGE_AGENT_FLOW_TYPES)
@pytest.mark.parametrize("form", ["base64", "data_url", "jpg_mime"])
async def test_a_tool_that_returns_an_inline_image_block_records_a_reference(
    image_harness: ImageAgentHarness, form: str
) -> None:
    await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(
        session_id, f'call render_chart {{"form": "{form}"}}'
    )
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    chart = tool_image_reference(image_harness, chart_png())
    output = json.dumps([{"type": "text", "text": "rendered a chart"}])
    assert image_harness.model_inputs()[-1][-1] == {
        "kind": "tool",
        "content": [output, model_image(chart_png())],
    }
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.images for message in tool_messages] == [[chart]]
    assert [message.tool_output for message in tool_messages] == [output]
    assert_no_image_bytes_stored(snapshot, chart_png())


@pytest.mark.parametrize("image_flow_type", IMAGE_AGENT_FLOW_TYPES)
async def test_a_tool_image_the_store_refuses_fails_the_call_without_storing_its_bytes(
    image_harness: ImageAgentHarness,
) -> None:
    await image_harness.stack.start_worker()
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(
        session_id, 'call render_chart {"form": "not_an_image"}'
    )
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_messages] == [
        "The tool call failed: tool render_chart returned image 1, which is not a supported "
        "image: image is not PNG, JPEG, GIF, or WebP"
    ]
    assert [message.images for message in tool_messages] == [[]]
    assert_no_image_bytes_stored(snapshot, NOT_AN_IMAGE)


@pytest.mark.parametrize("image_flow_type", IMAGE_AGENT_FLOW_TYPES)
async def test_a_worker_killed_in_the_resumed_model_call_rebuilds_the_same_model_input(
    image_harness: ImageAgentHarness,
) -> None:
    worker = await image_harness.stack.start_worker()
    png = await build_media_store(image_harness.state_dir).put_image(PNG)
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)
    command = "call take_screenshot {} then stall model-gate"
    assert await image_harness.sessions.send_user_message(session_id, command, images=[png])
    gate = image_harness.gate("model-gate")
    await gate.wait_until_reached()
    run_state = (await image_harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE))[
        FRAMEWORK_RUN_STATE
    ]
    checkpoints = decode_run_state(run_state)
    assert png.uri in checkpoints
    assert base64.b64encode(PNG).decode() not in checkpoints

    worker.kill()
    gate.release()
    await image_harness.stack.start_worker()
    first = await image_harness.wait_for_turns(session_id, 1)
    assert await image_harness.sessions.send_user_message(session_id, "describe")
    second = await image_harness.wait_for_turns(session_id, 2)

    turn_input = [
        {"kind": "human", "content": [command, model_image(PNG)]},
        {"kind": "tool", "content": ["took a screenshot", model_image(screenshot_png())]},
    ]
    inputs = image_harness.model_inputs()
    assert len(inputs) == 4
    # The killed attempt and its retry on the new Worker sent the same input.
    assert inputs[1] == turn_input
    assert inputs[2] == turn_input
    assert inputs[3] == [*turn_input, {"kind": "human", "content": ["describe"]}]
    assert first.messages[-1].text == "waiting; take_screenshot: took a screenshot"
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
