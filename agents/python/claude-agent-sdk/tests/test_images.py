# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images reach the bundled Claude Code CLI's model requests; the session stores references."""

from __future__ import annotations

import json
from typing import Any

from dex_agents import FRAMEWORK_RUN_STATE, MessageRole
from tests.claude_test_agent import (
    MEDIA_STORE_ENVIRONMENT_VARIABLE,
    TEST_IMAGES,
    build_media_store,
    conformance_image_bytes,
)
from tests.conftest import ClaudeAgentHarness, RealClaudeCliHarness

MEDIA_STORE_ENVIRONMENT = {MEDIA_STORE_ENVIRONMENT_VARIABLE: "1"}
USER_IMAGE_BASE64 = TEST_IMAGES["png"]["base64"]
SCREENSHOT_BASE64 = TEST_IMAGES["gif"]["base64"]
DRAWING_BASE64 = TEST_IMAGES["jpeg"]["base64"]


async def test_user_and_tool_result_images_reach_the_model_and_replay_after_restarts(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    media = build_media_store(harness.state_dir)
    user_image = await media.put_image(conformance_image_bytes("png"))
    worker = await harness.stack.start_worker(MEDIA_STORE_ENVIRONMENT)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(
        session_id, "call screenshot {}", images=[user_image]
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_name == "screenshot"
    # The suspended turn keeps its transcript, user image included, in the run state.
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    run_state = values[FRAMEWORK_RUN_STATE]
    assert user_image.uri in run_state
    assert USER_IMAGE_BASE64 not in run_state

    worker.kill()
    worker = await harness.stack.start_worker(MEDIA_STORE_ENVIRONMENT)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    first = await harness.wait_for_turns(session_id, 1)
    assert first.messages[-1].text == "screenshot: took a screenshot"
    worker.kill()
    await harness.stack.start_worker(MEDIA_STORE_ENVIRONMENT)
    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == "history: call screenshot {}"

    first_request, resumed_request, recall_request = _model_requests(harness)
    assert _image_blocks(first_request["messages"][0]["content"]) == [
        {
            "type": "image",
            "source": {"type": "base64", "media_type": "image/png", "data": USER_IMAGE_BASE64},
        }
    ]
    tool_result = next(
        block
        for block in resumed_request["messages"][-1]["content"]
        if block.get("type") == "tool_result"
    )
    assert tool_result["tool_use_id"] == interrupt.tool_call_id
    assert _image_blocks(tool_result["content"]) == [
        {
            "type": "image",
            "source": {"type": "base64", "media_type": "image/gif", "data": SCREENSHOT_BASE64},
        }
    ]
    # Each restarted Worker rebuilt the earlier conversation from references, byte for byte.
    first_conversation = _conversation(first_request)
    resumed_conversation = _conversation(resumed_request)
    recall_conversation = _conversation(recall_request)
    assert resumed_conversation[: len(first_conversation)] == first_conversation
    assert recall_conversation[: len(resumed_conversation)] == resumed_conversation

    screenshot_image = await media.put_image(conformance_image_bytes("gif"))
    assert second.messages[0].images == [user_image]
    tool_messages = [message for message in second.messages if message.role is MessageRole.TOOL]
    assert [message.tool_name for message in tool_messages] == ["screenshot"]
    assert [message.images for message in tool_messages] == [[screenshot_image]]
    stored = json.dumps([message.framework_item for message in second.messages])
    assert USER_IMAGE_BASE64 not in stored
    assert SCREENSHOT_BASE64 not in stored
    assert screenshot_image.uri in stored
    assert harness.invocations.count("screenshot") == 1


async def test_a_deferred_custom_tool_image_is_stored_as_a_reference(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    await harness.stack.start_worker(MEDIA_STORE_ENVIRONMENT)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call draw {"subject": "a cat"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "draw: drew a cat"
    tool_messages = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_name for message in tool_messages] == ["draw"]
    assert [[image.sha256 for image in message.images] for message in tool_messages] == [
        [TEST_IMAGES["jpeg"]["sha256"]]
    ]
    resumed_request = _model_requests(harness)[-1]
    tool_result = next(
        block
        for message in resumed_request["messages"]
        if isinstance(message["content"], list)
        for block in message["content"]
        if block.get("type") == "tool_result"
    )
    assert _image_blocks(tool_result["content"]) == [
        {
            "type": "image",
            "source": {"type": "base64", "media_type": "image/jpeg", "data": DRAWING_BASE64},
        }
    ]
    stored = json.dumps([message.framework_item for message in snapshot.messages])
    assert DRAWING_BASE64 not in stored
    assert harness.invocations.count("draw") == 1


async def test_a_runner_without_a_media_store_refuses_images(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    image = await build_media_store(harness.state_dir).put_image(conformance_image_bytes("png"))
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)

    assert not await harness.sessions.send_user_message(session_id, "say hi", images=[image])
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "said: hi"
    assert [message.images for message in snapshot.messages] == [[], []]


def _model_requests(harness: RealClaudeCliHarness) -> list[dict[str, Any]]:
    return [
        request.body for request in harness.messages_api.requests if request.body.get("messages")
    ]


def _image_blocks(content: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [block for block in content if block.get("type") == "image"]


def _conversation(request: dict[str, Any]) -> list[Any]:
    """Return the request's messages without the CLI's system turns and cache breakpoints."""
    return [
        _without_cache_control(message)
        for message in request["messages"]
        if message.get("role") != "system"
    ]


def _without_cache_control(value: Any) -> Any:
    if isinstance(value, list):
        return [_without_cache_control(item) for item in value]
    if isinstance(value, dict):
        return {
            key: _without_cache_control(item)
            for key, item in value.items()
            if key != "cache_control"
        }
    return value
