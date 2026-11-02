# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""LiteLLMModelClient against a fake provider transport: provider-exact replay, one request."""

from __future__ import annotations

import asyncio
import json
from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass, field
from typing import Any

import pytest
import pytest_asyncio

from dex_agents.litellm import CHAT_COMPLETIONS_API, RESPONSES_API, LiteLLMModelClient
from dex_agents.stepwise import (
    ModelInputItem,
    ModelOutputItem,
    ModelRequest,
    ModelRequestMetadata,
    ModelSettings,
    ModelToolDefinition,
    ToolResultItem,
    UserTextItem,
)

ENCRYPTED_REASONING = "gAAAAABo-Opaque+/=encrypted" + chr(0x2028) + "reasoning\\content"
THINKING_TEXT = "The user wants order 7.\nI should call lookup_order."
THINKING_SIGNATURE = "EqQBCgIYAhIM1gbcDa9GJwZA2b3hGgxBdjrkzLoky3dl1pkiMOYds+/=sig"
LOOKUP_TOOL = ModelToolDefinition(
    name="lookup_order",
    description="Look up an order.",
    parameters={"type": "object", "properties": {"order_id": {"type": "string"}}},
)


@dataclass
class FakeProviderTransport:
    """An HTTP server that answers each POST with the next scripted SSE body."""

    responses: list[tuple[int, list[tuple[str, dict[str, Any]]]]] = field(default_factory=list)
    requests: list[tuple[str, dict[str, Any]]] = field(default_factory=list)
    port: int = 0

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    async def handle(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        request_line = (await reader.readline()).decode()
        headers: dict[str, str] = {}
        while (line := (await reader.readline()).decode()) not in ("\r\n", ""):
            name, _, value = line.partition(":")
            headers[name.strip().lower()] = value.strip()
        body = await reader.readexactly(int(headers.get("content-length", "0")))
        self.requests.append((request_line.split(" ")[1], json.loads(body or b"{}")))
        status, events = self.responses.pop(0)
        if status != 200:
            payload = json.dumps({"error": {"message": "overloaded", "type": "server_error"}})
            writer.write(
                f"HTTP/1.1 {status} Error\r\nContent-Type: application/json\r\n"
                f"Content-Length: {len(payload)}\r\nConnection: close\r\n\r\n{payload}".encode()
            )
        else:
            stream = "".join(
                f"event: {name}\ndata: {json.dumps(data)}\n\n" for name, data in events
            ).encode()
            writer.write(
                b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"
                + f"Content-Length: {len(stream)}\r\nConnection: close\r\n\r\n".encode()
                + stream
            )
        await writer.drain()
        writer.close()


@pytest_asyncio.fixture
async def transport() -> AsyncIterator[FakeProviderTransport]:
    fake = FakeProviderTransport()
    server = await asyncio.start_server(fake.handle, "127.0.0.1", 0)
    fake.port = server.sockets[0].getsockname()[1]
    try:
        yield fake
    finally:
        server.close()
        await server.wait_closed()


@dataclass
class RecordedOutput:
    text: list[str] = field(default_factory=list)
    reasoning: list[str] = field(default_factory=list)

    def write_text_delta(self, text: str) -> None:
        self.text.append(text)

    def write_reasoning_delta(self, text: str) -> None:
        self.reasoning.append(text)


async def test_openai_reasoning_items_replay_byte_for_byte_without_provider_state(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "openai/gpt-5-mini", api_base=f"{transport.base_url}/v1", api_key_provider=lambda: "sk"
    )
    assert (client.provider, client.api) == ("openai", RESPONSES_API)
    transport.responses.append((200, _responses_tool_call_stream()))
    output = RecordedOutput()

    first = await client.call_model(_request([UserTextItem(text="Where is order 7?")]), output)

    assert output.reasoning == ["Looking up ", "the order."]
    assert [(item.provider, item.api) for item in first.output_items] == [
        ("openai", RESPONSES_API),
        ("openai", RESPONSES_API),
    ]
    reasoning_item = json.loads(first.output_items[0].item)
    assert reasoning_item["encrypted_content"] == ENCRYPTED_REASONING
    assert [(call.tool_call_id, call.tool_name, call.arguments) for call in first.tool_calls] == [
        ("call_7", "lookup_order", '{"order_id":"7"}')
    ]
    assert (first.usage.model_calls, first.usage.input_tokens, first.usage.output_tokens) == (
        1,
        42,
        17,
    )
    assert (first.usage.cached_input_tokens, first.usage.reasoning_tokens) == (8, 11)

    transport.responses.append((200, _responses_text_stream("Order 7 shipped.")))
    second = await client.call_model(
        _request(
            [
                UserTextItem(text="Where is order 7?"),
                *first.output_items,
                ToolResultItem(tool_call_id="call_7", tool_name="lookup_order", output="shipped"),
            ]
        ),
        output,
    )

    assert second.text == "Order 7 shipped."
    assert "".join(output.text) == "Order 7 shipped."
    path, replayed = transport.requests[1]
    assert path == "/v1/responses"
    assert replayed["store"] is False
    assert replayed["include"] == ["reasoning.encrypted_content"]
    assert "previous_response_id" not in replayed
    assert replayed["input"][1] == reasoning_item
    assert replayed["input"][1]["encrypted_content"] == ENCRYPTED_REASONING
    assert replayed["input"][2] == json.loads(first.output_items[1].item)
    assert replayed["input"][3] == {
        "type": "function_call_output",
        "call_id": "call_7",
        "output": "shipped",
    }


async def test_anthropic_thinking_blocks_keep_their_signatures_on_replay(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "anthropic/claude-sonnet-4-5", api_base=transport.base_url, api_key_provider=lambda: "sk"
    )
    assert (client.provider, client.api) == ("anthropic", CHAT_COMPLETIONS_API)
    transport.responses.append((200, _anthropic_tool_call_stream()))
    output = RecordedOutput()

    first = await client.call_model(_request([UserTextItem(text="Where is order 7?")]), output)

    assert "".join(output.reasoning) == THINKING_TEXT
    stored = json.loads(first.output_items[0].item)
    assert stored["thinking_blocks"] == [
        {"type": "thinking", "thinking": THINKING_TEXT, "signature": THINKING_SIGNATURE}
    ]
    assert [(call.tool_call_id, call.tool_name) for call in first.tool_calls] == [
        ("toolu_7", "lookup_order")
    ]
    assert json.loads(first.tool_calls[0].arguments) == {"order_id": "7"}

    transport.responses.append((200, _anthropic_text_stream("Order 7 shipped.")))
    second = await client.call_model(
        _request(
            [
                UserTextItem(text="Where is order 7?"),
                *first.output_items,
                ToolResultItem(tool_call_id="toolu_7", tool_name="lookup_order", output="shipped"),
            ]
        ),
        output,
    )

    assert second.text == "Order 7 shipped."
    path, replayed = transport.requests[1]
    assert path == "/v1/messages"
    assistant = next(message for message in replayed["messages"] if message["role"] == "assistant")
    thinking = [block for block in assistant["content"] if block["type"] == "thinking"]
    assert thinking == [
        {"type": "thinking", "thinking": THINKING_TEXT, "signature": THINKING_SIGNATURE}
    ]
    tool_use = [block for block in assistant["content"] if block["type"] == "tool_use"]
    assert [(block["id"], block["name"], block["input"]) for block in tool_use] == [
        ("toolu_7", "lookup_order", {"order_id": "7"})
    ]


async def test_a_model_switch_replays_text_and_tool_calls_and_drops_reasoning(
    transport: FakeProviderTransport,
) -> None:
    openai_items = [
        ModelOutputItem(
            provider="openai",
            api=RESPONSES_API,
            item=json.dumps({"type": "reasoning", "id": "rs_1", "encrypted_content": "x"}),
        ),
        ModelOutputItem(
            provider="openai",
            api=RESPONSES_API,
            item=json.dumps({"type": "message", "id": "msg_1", "role": "assistant"}),
            text="Let me check.",
        ),
    ]
    client = LiteLLMModelClient(
        "anthropic/claude-sonnet-4-5", api_base=transport.base_url, api_key_provider=lambda: "sk"
    )
    transport.responses.append((200, _anthropic_text_stream("Done.")))

    await client.call_model(
        _request([UserTextItem(text="hi"), *openai_items, UserTextItem(text="and now?")]),
        RecordedOutput(),
    )

    _, replayed = transport.requests[0]
    assistant = next(message for message in replayed["messages"] if message["role"] == "assistant")
    assert json.dumps(assistant).count("encrypted_content") == 0
    assert [block["type"] for block in assistant["content"]] == ["text"]
    assert assistant["content"][0]["text"] == "Let me check."


async def test_a_failed_request_is_not_retried_by_the_client(
    transport: FakeProviderTransport,
) -> None:
    client = LiteLLMModelClient(
        "anthropic/claude-sonnet-4-5", api_base=transport.base_url, api_key_provider=lambda: "sk"
    )
    transport.responses.extend([(529, []), (200, _anthropic_text_stream("too late"))])

    with pytest.raises(Exception):  # noqa: B017 - LiteLLM maps the status to its own error types
        await client.call_model(_request([UserTextItem(text="hi")]), RecordedOutput())

    assert len(transport.requests) == 1


def _request(items: Sequence[ModelInputItem]) -> ModelRequest:
    return ModelRequest(
        instructions="You help with orders.",
        input_items=items,
        tools=[LOOKUP_TOOL],
        tool_choice="auto",
        settings=ModelSettings(),
        metadata=ModelRequestMetadata(
            flow_id="session-1", turn_id="turn-1", step_execution_id="RunModel-1", attempt=1
        ),
    )


def _responses_envelope(status: str, output: list[dict[str, Any]]) -> dict[str, Any]:
    return {
        "id": "resp_1",
        "object": "response",
        "created_at": 1_790_000_000,
        "status": status,
        "model": "gpt-5-mini-2026-08-07",
        "output": output,
        "parallel_tool_calls": True,
        "tool_choice": "auto",
        "tools": [],
        "usage": {
            "input_tokens": 42,
            "input_tokens_details": {"cached_tokens": 8},
            "output_tokens": 17,
            "output_tokens_details": {"reasoning_tokens": 11},
            "total_tokens": 59,
        }
        if status == "completed"
        else None,
    }


def _responses_tool_call_stream() -> list[tuple[str, dict[str, Any]]]:
    reasoning = {
        "type": "reasoning",
        "id": "rs_1",
        "summary": [{"type": "summary_text", "text": "Looking up the order."}],
        "encrypted_content": ENCRYPTED_REASONING,
    }
    call = {
        "type": "function_call",
        "id": "fc_1",
        "call_id": "call_7",
        "name": "lookup_order",
        "arguments": '{"order_id":"7"}',
        "status": "completed",
    }
    return [
        ("response.created", {"type": "response.created", "sequence_number": 0,
                              "response": _responses_envelope("in_progress", [])}),
        ("response.output_item.added", {"type": "response.output_item.added",
                                        "sequence_number": 1, "output_index": 0,
                                        "item": {**reasoning, "summary": []}}),
        ("response.reasoning_summary_text.delta", {
            "type": "response.reasoning_summary_text.delta", "sequence_number": 2,
            "item_id": "rs_1", "output_index": 0, "summary_index": 0, "delta": "Looking up "}),
        ("response.reasoning_summary_text.delta", {
            "type": "response.reasoning_summary_text.delta", "sequence_number": 3,
            "item_id": "rs_1", "output_index": 0, "summary_index": 0, "delta": "the order."}),
        ("response.output_item.done", {"type": "response.output_item.done", "sequence_number": 4,
                                       "output_index": 0, "item": reasoning}),
        ("response.output_item.done", {"type": "response.output_item.done", "sequence_number": 5,
                                       "output_index": 1, "item": call}),
        ("response.completed", {"type": "response.completed", "sequence_number": 6,
                                "response": _responses_envelope("completed", [reasoning, call])}),
    ]  # fmt: skip


def _responses_text_stream(text: str) -> list[tuple[str, dict[str, Any]]]:
    message = {
        "type": "message",
        "id": "msg_2",
        "role": "assistant",
        "status": "completed",
        "content": [{"type": "output_text", "text": text, "annotations": []}],
    }
    half = len(text) // 2
    return [
        ("response.created", {"type": "response.created", "sequence_number": 0,
                              "response": _responses_envelope("in_progress", [])}),
        *[
            ("response.output_text.delta", {"type": "response.output_text.delta",
                                            "sequence_number": 1 + index, "item_id": "msg_2",
                                            "output_index": 0, "content_index": 0, "delta": part})
            for index, part in enumerate((text[: half + 1], text[half + 1 :]))
        ],
        ("response.output_item.done", {"type": "response.output_item.done", "sequence_number": 3,
                                       "output_index": 0, "item": message}),
        ("response.completed", {"type": "response.completed", "sequence_number": 4,
                                "response": _responses_envelope("completed", [message])}),
    ]  # fmt: skip


def _anthropic_message_start() -> tuple[str, dict[str, Any]]:
    return (
        "message_start",
        {
            "type": "message_start",
            "message": {
                "id": "msg_1",
                "type": "message",
                "role": "assistant",
                "model": "claude-sonnet-4-5-20250929",
                "content": [],
                "stop_reason": None,
                "stop_sequence": None,
                "usage": {"input_tokens": 30, "output_tokens": 1},
            },
        },
    )


def _anthropic_tool_call_stream() -> list[tuple[str, dict[str, Any]]]:
    first_half, second_half = THINKING_TEXT[:10], THINKING_TEXT[10:]
    return [
        _anthropic_message_start(),
        ("content_block_start", {"type": "content_block_start", "index": 0,
                                 "content_block": {"type": "thinking", "thinking": "",
                                                   "signature": ""}}),
        ("content_block_delta", {"type": "content_block_delta", "index": 0,
                                 "delta": {"type": "thinking_delta", "thinking": first_half}}),
        ("content_block_delta", {"type": "content_block_delta", "index": 0,
                                 "delta": {"type": "thinking_delta", "thinking": second_half}}),
        ("content_block_delta", {"type": "content_block_delta", "index": 0,
                                 "delta": {"type": "signature_delta",
                                           "signature": THINKING_SIGNATURE}}),
        ("content_block_stop", {"type": "content_block_stop", "index": 0}),
        ("content_block_start", {"type": "content_block_start", "index": 1,
                                 "content_block": {"type": "tool_use", "id": "toolu_7",
                                                   "name": "lookup_order", "input": {}}}),
        ("content_block_delta", {"type": "content_block_delta", "index": 1,
                                 "delta": {"type": "input_json_delta",
                                           "partial_json": '{"order_id": "7"}'}}),
        ("content_block_stop", {"type": "content_block_stop", "index": 1}),
        ("message_delta", {"type": "message_delta",
                           "delta": {"stop_reason": "tool_use", "stop_sequence": None},
                           "usage": {"output_tokens": 25}}),
        ("message_stop", {"type": "message_stop"}),
    ]  # fmt: skip


def _anthropic_text_stream(text: str) -> list[tuple[str, dict[str, Any]]]:
    return [
        _anthropic_message_start(),
        ("content_block_start", {"type": "content_block_start", "index": 0,
                                 "content_block": {"type": "text", "text": ""}}),
        ("content_block_delta", {"type": "content_block_delta", "index": 0,
                                 "delta": {"type": "text_delta", "text": text}}),
        ("content_block_stop", {"type": "content_block_stop", "index": 0}),
        ("message_delta", {"type": "message_delta",
                           "delta": {"stop_reason": "end_turn", "stop_sequence": None},
                           "usage": {"output_tokens": 5}}),
        ("message_stop", {"type": "message_stop"}),
    ]  # fmt: skip
