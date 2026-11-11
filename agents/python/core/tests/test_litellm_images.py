# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""LiteLLMModelClient image parts on the wire: Responses API, OpenAI chat, and Anthropic."""

from __future__ import annotations

import base64
import json
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest

from dex_agents import AgentImageReference, build_image_reference
from dex_agents.builtin_loop import (
    ModelInputItem,
    ModelOutputItem,
    ModelToolCall,
    ToolResultItem,
    UserTextItem,
)
from dex_agents.litellm import (
    ESTIMATED_IMAGE_INPUT_TOKENS,
    TOOL_RESULT_IMAGES_TEXT,
    LiteLLMModelClient,
)
from tests.test_litellm_model_client import (
    FakeProviderTransport,
    RecordedOutput,
    _anthropic_text_stream,
    _request,
    _responses_text_stream,
    transport,
)

__all__ = ["transport"]

IMAGES: dict[str, Any] = {
    case["name"]: case
    for case in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
}
PNG = base64.b64decode(IMAGES["png"]["base64"])
JPEG = base64.b64decode(IMAGES["jpeg"]["base64"])
PNG_REFERENCE = build_image_reference(PNG, "memory://wire")
JPEG_REFERENCE = build_image_reference(JPEG, "memory://wire")
IMAGE_BYTES = {PNG_REFERENCE.sha256: PNG, JPEG_REFERENCE.sha256: JPEG}


def data_url(reference: AgentImageReference, data: bytes) -> str:
    return f"data:{reference.mime_type};base64,{base64.b64encode(data).decode()}"


def neutral_tool_calls(*tool_call_ids: str) -> ModelOutputItem:
    """An item from another provider, so every client replays it as neutral tool calls."""
    return ModelOutputItem(
        provider="dex-scripted",
        api="scripted",
        item="{}",
        tool_calls=tuple(
            ModelToolCall(tool_call_id=tool_call_id, tool_name="screenshot", arguments="{}")
            for tool_call_id in tool_call_ids
        ),
    )


def image_request(items: list[ModelInputItem]) -> Any:
    return replace(_request(items), image_bytes=IMAGE_BYTES)


async def test_the_responses_api_gets_input_image_parts_in_the_message_and_the_tool_output(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "openai/gpt-5-mini", api_base=f"{transport.base_url}/v1", api_key_provider=lambda: "sk"
    )
    transport.responses.append((200, _responses_text_stream("A chart.")))

    await client.call_model(
        image_request(
            [
                UserTextItem(text="What is this?", images=[PNG_REFERENCE]),
                neutral_tool_calls("call_1"),
                ToolResultItem(
                    tool_call_id="call_1",
                    tool_name="screenshot",
                    output="Took one.",
                    images=[JPEG_REFERENCE],
                ),
            ]
        ),
        RecordedOutput(),
    )

    path, body = transport.requests[0]
    assert path == "/v1/responses"
    assert body["input"][0] == {
        "type": "message",
        "role": "user",
        "content": [
            {"type": "input_text", "text": "What is this?"},
            {"type": "input_image", "image_url": data_url(PNG_REFERENCE, PNG), "detail": "auto"},
        ],
    }
    assert body["input"][-1] == {
        "type": "function_call_output",
        "call_id": "call_1",
        "output": [
            {"type": "input_text", "text": "Took one."},
            {"type": "input_image", "image_url": data_url(JPEG_REFERENCE, JPEG), "detail": "auto"},
        ],
    }


async def test_openai_chat_gets_tool_images_in_a_user_message_after_the_parallel_results(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "openai/gpt-4o",
        api="chat_completions",
        api_base=f"{transport.base_url}/v1",
        api_key_provider=lambda: "sk",
    )
    transport.responses.append((200, _chat_completion_text_stream("Two screenshots.")))

    await client.call_model(
        image_request(
            [
                UserTextItem(text="Look.", images=[PNG_REFERENCE]),
                neutral_tool_calls("call_1", "call_2"),
                ToolResultItem(
                    tool_call_id="call_1",
                    tool_name="screenshot",
                    output="First.",
                    images=[JPEG_REFERENCE],
                ),
                ToolResultItem(tool_call_id="call_2", tool_name="screenshot", output="Second."),
                UserTextItem(text="And now?"),
            ]
        ),
        RecordedOutput(),
    )

    path, body = transport.requests[0]
    assert path == "/v1/chat/completions"
    messages = body["messages"]
    assert [message["role"] for message in messages] == [
        "system",
        "user",
        "assistant",
        "tool",
        "tool",
        "user",
        "user",
    ]
    assert messages[1]["content"] == [
        {"type": "text", "text": "Look."},
        {"type": "image_url", "image_url": {"url": data_url(PNG_REFERENCE, PNG)}},
    ]
    assert [message["content"] for message in messages[3:5]] == ["First.", "Second."]
    assert messages[5]["content"] == [
        {
            "type": "text",
            "text": TOOL_RESULT_IMAGES_TEXT.format(tool_name="screenshot", tool_call_id="call_1"),
        },
        {"type": "image_url", "image_url": {"url": data_url(JPEG_REFERENCE, JPEG)}},
    ]
    assert messages[6]["content"] == "And now?"


async def test_anthropic_gets_image_blocks_in_the_user_message_and_inside_the_tool_result(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "anthropic/claude-sonnet-4-5", api_base=transport.base_url, api_key_provider=lambda: "sk"
    )
    transport.responses.append((200, _anthropic_text_stream("A screenshot.")))

    await client.call_model(
        image_request(
            [
                UserTextItem(text="Look.", images=[PNG_REFERENCE]),
                neutral_tool_calls("toolu_1"),
                ToolResultItem(
                    tool_call_id="toolu_1",
                    tool_name="screenshot",
                    output="Took one.",
                    images=[JPEG_REFERENCE],
                ),
            ]
        ),
        RecordedOutput(),
    )

    path, body = transport.requests[0]
    assert path == "/v1/messages"
    user = body["messages"][0]
    assert user["role"] == "user"
    assert [block["type"] for block in user["content"]] == ["text", "image"]
    assert user["content"][1]["source"] == {
        "type": "base64",
        "media_type": "image/png",
        "data": base64.b64encode(PNG).decode(),
    }
    tool_result = body["messages"][-1]["content"][0]
    assert (tool_result["type"], tool_result["tool_use_id"]) == ("tool_result", "toolu_1")
    assert [block["type"] for block in tool_result["content"]] == ["text", "image"]
    assert tool_result["content"][0]["text"] == "Took one."
    assert base64.b64decode(tool_result["content"][1]["source"]["data"]) == JPEG
    assert tool_result["content"][1]["source"]["media_type"] == "image/jpeg"


async def test_a_request_without_the_bytes_of_an_image_fails_before_it_is_sent(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "openai/gpt-5-mini", api_base=f"{transport.base_url}/v1", api_key_provider=lambda: "sk"
    )

    with pytest.raises(ValueError, match="has no bytes for image memory://wire/sha256/"):
        await client.call_model(
            _request([UserTextItem(text="Look.", images=[PNG_REFERENCE])]), RecordedOutput()
        )
    assert transport.requests == []


def test_the_estimate_counts_each_image_without_reading_its_bytes() -> None:
    client = LiteLLMModelClient("openai/gpt-5-mini")
    without_images = client.estimate_input_tokens(
        _request([UserTextItem(text="Look."), _screenshot_result(())])
    )

    with_images = client.estimate_input_tokens(
        _request(
            [
                UserTextItem(text="Look.", images=[PNG_REFERENCE]),
                _screenshot_result((JPEG_REFERENCE,)),
            ]
        )
    )

    assert with_images - without_images == 2 * ESTIMATED_IMAGE_INPUT_TOKENS


def _screenshot_result(images: tuple[AgentImageReference, ...]) -> ToolResultItem:
    return ToolResultItem(tool_call_id="call_1", tool_name="screenshot", output="ok", images=images)


def _chat_completion_text_stream(text: str) -> list[tuple[str, dict[str, Any]]]:
    chunk = {
        "id": "chatcmpl-1",
        "object": "chat.completion.chunk",
        "created": 1_790_000_000,
        "model": "gpt-4o-2026-08-06",
    }
    return [
        ("message", {**chunk, "choices": [{"index": 0, "finish_reason": None,
                                           "delta": {"role": "assistant", "content": text}}]}),
        ("message", {**chunk, "choices": [{"index": 0, "finish_reason": "stop", "delta": {}}]}),
        ("message", {**chunk, "choices": [],
                     "usage": {"prompt_tokens": 20, "completion_tokens": 3, "total_tokens": 23}}),
    ]  # fmt: skip
