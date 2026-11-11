# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""LiteLLMModelClient image parts on the wire: Responses API, OpenAI chat, Anthropic, and Gemini."""

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
    _gemini_contents_with_one_user_turn,
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


def inline_data(reference: AgentImageReference, data: bytes) -> dict[str, str]:
    return {"mime_type": reference.mime_type, "data": base64.b64encode(data).decode()}


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


async def test_gemini_gets_tool_images_in_the_function_response_turn_after_the_responses(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "gemini/gemini-2.5-flash", api_base=transport.base_url, api_key_provider=lambda: "key"
    )
    transport.responses.append((200, _gemini_text_stream("Two screenshots.")))

    await client.call_model(
        image_request(
            [
                UserTextItem(text="Look.", images=[PNG_REFERENCE]),
                ModelOutputItem(
                    provider="dex-scripted",
                    api="scripted",
                    item="{}",
                    tool_calls=(
                        ModelToolCall(
                            tool_call_id="call_1", tool_name="screenshot", arguments='{"page": 1}'
                        ),
                        ModelToolCall(
                            tool_call_id="call_2", tool_name="screenshot", arguments='{"page": 2}'
                        ),
                    ),
                ),
                ToolResultItem(
                    tool_call_id="call_1",
                    tool_name="screenshot",
                    output="First.",
                    images=[JPEG_REFERENCE],
                ),
                ToolResultItem(
                    tool_call_id="call_2",
                    tool_name="screenshot",
                    output="Second.",
                    images=[PNG_REFERENCE],
                ),
                UserTextItem(text="And now?"),
            ]
        ),
        RecordedOutput(),
    )

    _, body = transport.requests[0]
    assert body["system_instruction"] == {"parts": [{"text": "You help with orders."}]}
    # One user turn holds the function responses, which carry text only because Gemini 2.5
    # refuses images there, and then the images, so user and model turns alternate.
    assert body["contents"] == [
        {
            "role": "user",
            "parts": [{"text": "Look."}, {"inline_data": inline_data(PNG_REFERENCE, PNG)}],
        },
        {
            "role": "model",
            "parts": [
                {"function_call": {"name": "screenshot", "args": {"page": 1}}},
                {"function_call": {"name": "screenshot", "args": {"page": 2}}},
            ],
        },
        {
            "role": "user",
            "parts": [
                {"function_response": {"name": "screenshot", "response": {"content": "First."}}},
                {"function_response": {"name": "screenshot", "response": {"content": "Second."}}},
                {
                    "text": TOOL_RESULT_IMAGES_TEXT.format(
                        tool_name="screenshot", tool_call_id="call_1"
                    )
                },
                {"inline_data": inline_data(JPEG_REFERENCE, JPEG)},
                {
                    "text": TOOL_RESULT_IMAGES_TEXT.format(
                        tool_name="screenshot", tool_call_id="call_2"
                    )
                },
                {"inline_data": inline_data(PNG_REFERENCE, PNG)},
                {"text": "And now?"},
            ],
        },
    ]


async def test_gemini_contents_stay_litellm_s_own_without_tool_images(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "gemini/gemini-2.5-flash", api_base=transport.base_url, api_key_provider=lambda: "key"
    )
    transport.responses.append((200, _gemini_text_stream("Nothing to see.")))

    await client.call_model(
        image_request(
            [
                UserTextItem(text="Look.", images=[PNG_REFERENCE]),
                neutral_tool_calls("call_1"),
                ToolResultItem(tool_call_id="call_1", tool_name="screenshot", output="None."),
            ]
        ),
        RecordedOutput(),
    )

    _, body = transport.requests[0]
    assert [content["role"] for content in body["contents"]] == ["user", "model", "user"]
    assert body["contents"][2]["parts"] == [
        {"function_response": {"name": "screenshot", "response": {"content": "None."}}}
    ]


@pytest.mark.parametrize(
    ("model", "merges"),
    [
        ("vertex_ai/gemini-2.5-flash", True),
        ("vertex_ai_beta/gemini-2.5-flash", True),
        ("gemini/gemini-2.5-flash", True),
        ("vertex_ai/claude-sonnet-4-5", False),
        ("openai/gpt-4o", False),
    ],
)
def test_only_gemini_models_get_their_user_contents_merged(model: str, merges: bool) -> None:
    messages: list[dict[str, Any]] = [
        {"role": "system", "content": "Be brief."},
        {"role": "user", "content": "Look."},
        {
            "role": "assistant",
            "content": None,
            "tool_calls": [
                {
                    "id": "call_1",
                    "type": "function",
                    "function": {"name": "screenshot", "arguments": "{}"},
                }
            ],
        },
        {"role": "tool", "tool_call_id": "call_1", "content": "Took one."},
        {
            "role": "user",
            "content": [
                {"type": "text", "text": "Its images."},
                {"type": "image_url", "image_url": {"url": data_url(JPEG_REFERENCE, JPEG)}},
            ],
        },
    ]

    contents = _gemini_contents_with_one_user_turn(messages, model)

    if not merges:
        assert contents is None
        return
    assert contents is not None
    assert [content["role"] for content in contents] == ["user", "model", "user"]
    assert contents[0]["parts"] == [{"text": "Look."}]
    assert contents[2]["parts"] == [
        {"function_response": {"name": "screenshot", "response": {"content": "Took one."}}},
        {"text": "Its images."},
        {"inline_data": inline_data(JPEG_REFERENCE, JPEG)},
    ]


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


def _gemini_text_stream(text: str) -> list[tuple[str, dict[str, Any]]]:
    return [
        ("message", {"candidates": [{"content": {"role": "model", "parts": [{"text": text}]},
                                     "finishReason": "STOP", "index": 0}],
                     "usageMetadata": {"promptTokenCount": 20, "candidatesTokenCount": 3,
                                       "totalTokenCount": 23},
                     "modelVersion": "gemini-2.5-flash"}),
    ]  # fmt: skip
