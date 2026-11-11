# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images reach the ADK model as inline parts, while the session stores only references."""

from __future__ import annotations

import base64
import hashlib
import json
from pathlib import Path
from typing import Any

from google.genai import types

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    AgentImageReference,
    LocalDirectoryMediaStore,
    MessageRole,
    image_uri,
)
from tests.conftest import AdkAgentHarness
from tests.image_adk_agent import (
    MEDIA_DIRECTORY_NAME,
    SCREENSHOT_RESULT,
    read_model_requests,
    screenshot_png,
)

IMAGES: dict[str, Any] = {
    case["name"]: case
    for case in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
}
PNG = base64.b64decode(IMAGES["png"]["base64"])


def screenshot_reference(media: LocalDirectoryMediaStore) -> AgentImageReference:
    sha256 = hashlib.sha256(screenshot_png()).hexdigest()
    return AgentImageReference(
        uri=image_uri(media.store_uri, sha256),
        mime_type="image/png",
        sha256=sha256,
        size_bytes=len(screenshot_png()),
    )


def assert_holds_no_image_bytes(stored: str, *images: bytes) -> None:
    for image in images:
        assert base64.b64encode(image).decode() not in stored
        assert base64.urlsafe_b64encode(image).decode() not in stored
    assert "inline_data" not in stored
    assert "inlineData" not in stored


def inline_images(contents: list[types.Content]) -> list[tuple[str, str | None, bytes | None]]:
    """Return each inline image of a request as (where, MIME type, bytes), in order."""
    found: list[tuple[str, str | None, bytes | None]] = []
    for content in contents:
        for part in content.parts or []:
            if part.inline_data is not None:
                found.append((f"{content.role}", part.inline_data.mime_type, part.inline_data.data))
            if part.function_response is not None:
                found.extend(
                    (
                        f"function_response {part.function_response.name}",
                        response_part.inline_data.mime_type,
                        response_part.inline_data.data,
                    )
                    for response_part in part.function_response.parts or []
                    if response_part.inline_data is not None
                )
    return found


def file_data_uris(contents: list[types.Content]) -> list[str | None]:
    uris = [
        part.file_data.file_uri
        for content in contents
        for part in content.parts or []
        if part.file_data is not None
    ]
    uris.extend(
        response_part.file_data.file_uri
        for content in contents
        for part in content.parts or []
        if part.function_response is not None
        for response_part in part.function_response.parts or []
        if response_part.file_data is not None
    )
    return uris


async def test_user_and_tool_images_reach_the_model_inline_and_replay_after_a_worker_kill(
    image_harness: AdkAgentHarness,
) -> None:
    worker = await image_harness.stack.start_worker()
    media = LocalDirectoryMediaStore(image_harness.state_dir / MEDIA_DIRECTORY_NAME)
    photo = await media.put_image(PNG)
    screenshot = screenshot_reference(media)
    session_id = image_harness.new_session_id()
    await image_harness.sessions.start_session(session_id)
    assert await image_harness.sessions.send_user_message(
        session_id, "screenshot-hold hold-gate", images=[photo]
    )
    gate = image_harness.gate("hold-gate")
    await gate.wait_until_reached()

    first_call, second_call, held_call = read_model_requests(image_harness.state_dir)
    # The user content holds the text, then the image bytes with the reference's MIME type.
    assert first_call[-1].role == "user"
    assert [(part.text, part.inline_data) for part in first_call[-1].parts or []] == [
        ("screenshot-hold hold-gate", None),
        (None, types.Blob(mime_type="image/png", data=PNG)),
    ]
    tool_image = ("function_response take_screenshot", "image/png", screenshot_png())
    assert inline_images(second_call) == [("user", "image/png", PNG), tool_image]
    # The first tool result replays from the run state, the second from its record.
    assert inline_images(held_call) == [("user", "image/png", PNG), tool_image, tool_image]
    assert file_data_uris(held_call) == []
    assert [
        part.function_response.response
        for content in held_call
        for part in content.parts or []
        if part.function_response is not None
    ] == [{"result": SCREENSHOT_RESULT}] * 2
    held = await image_harness.sessions.get_agent_snapshot(session_id)
    assert [record.images for record in held.tool_calls] == [[screenshot], [screenshot]]
    run_state = (await image_harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE))[
        FRAMEWORK_RUN_STATE
    ]
    assert run_state is not None
    assert_holds_no_image_bytes(run_state, PNG, screenshot_png())
    assert photo.uri in run_state
    assert screenshot.uri in run_state

    worker.kill()
    gate.release()
    await image_harness.stack.start_worker()
    snapshot = await image_harness.wait_for_turns(session_id, 1)

    # The new Worker rebuilt the same model input from the stored run state and the store.
    assert read_model_requests(image_harness.state_dir)[3] == held_call
    assert snapshot.messages[-1].text == f"take_screenshot: {SCREENSHOT_RESULT}"
    assert [(message.role, message.images) for message in snapshot.messages] == [
        (MessageRole.USER, [photo]),
        (MessageRole.ASSISTANT, []),
        (MessageRole.TOOL, [screenshot]),
        (MessageRole.ASSISTANT, []),
        (MessageRole.TOOL, [screenshot]),
        (MessageRole.ASSISTANT, []),
    ]
    assert snapshot.messages[2].tool_output == SCREENSHOT_RESULT
    stored_tool_response = json.loads(snapshot.messages[2].framework_item)["content"]["parts"][0]
    assert stored_tool_response["function_response"]["parts"] == [
        {"file_data": {"file_uri": screenshot.uri, "mime_type": "image/png"}}
    ]

    assert await image_harness.sessions.send_user_message(session_id, "describe")
    second = await image_harness.wait_for_turns(session_id, 2)

    assert second.messages[-1].text == "saw 3 images"
    later_call = read_model_requests(image_harness.state_dir)[-1]
    # The next turn replays every image from the stored framework items.
    assert later_call[: len(held_call)] == held_call
    assert inline_images(later_call) == inline_images(held_call)
    assert_holds_no_image_bytes(
        json.dumps([message.framework_item for message in second.messages]),
        PNG,
        screenshot_png(),
    )
    media_files = sorted(
        path.name for path in (image_harness.state_dir / MEDIA_DIRECTORY_NAME / "sha256").iterdir()
    )
    assert media_files == sorted([photo.sha256, screenshot.sha256])


async def test_a_runner_without_a_media_store_refuses_images(harness: AdkAgentHarness) -> None:
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
    assert [(message.text, message.images) for message in snapshot.messages] == [
        ("say hi", []),
        ("said: hi", []),
    ]
