# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""A local stand-in for the Messages API, so the real Claude Code CLI runs without a key.

It answers POST /v1/messages on 127.0.0.1 with the demo model's replies from
demo_model_script, as streamed SSE or JSON, and records every request.
"""

from __future__ import annotations

import json
import threading
from collections.abc import Iterator
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from agent_harness_examples.demo_model_script import DemoToolRequest, plan_demo_reply

STUB_USAGE = {"input_tokens": 10, "output_tokens": 5}


@dataclass(frozen=True)
class RecordedRequest:
    path: str
    api_key: str | None
    authorization: str | None


@dataclass
class ClaudeMessagesApiStub:
    requests: list[RecordedRequest] = field(default_factory=list)
    _server: ThreadingHTTPServer | None = None
    _thread: threading.Thread | None = None

    @property
    def base_url(self) -> str:
        if self._server is None:
            raise RuntimeError("start the Messages API stub first")
        host, port = self._server.server_address[:2]
        return f"http://{host!s}:{port}"

    def start(self) -> None:
        stub = self

        class MessagesRequestHandler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(length) or b"{}")
                stub.requests.append(RecordedRequest(
                    self.path, self.headers.get("x-api-key"), self.headers.get("authorization")
                ))
                if self.path.split("?", 1)[0] != "/v1/messages":
                    self._send_json(404, {"type": "error", "error": {"type": "not_found_error"}})
                    return
                message = reply_to_messages_request(body)
                if body.get("stream"):
                    self._send_events(message)
                else:
                    self._send_json(200, message)

            def do_GET(self) -> None:
                stub.requests.append(RecordedRequest(
                    self.path, self.headers.get("x-api-key"), self.headers.get("authorization")
                ))
                self._send_json(404, {"type": "error", "error": {"type": "not_found_error"}})

            def log_message(self, format: str, *args: Any) -> None:
                return

            def _send_json(self, status: int, payload: dict[str, Any]) -> None:
                data = json.dumps(payload).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def _send_events(self, message: dict[str, Any]) -> None:
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                for event in _stream_events(message):
                    frame = f"event: {event['type']}\ndata: {json.dumps(event)}\n\n"
                    self.wfile.write(frame.encode())
                    self.wfile.flush()

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), MessagesRequestHandler)
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    def stop(self) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
        if self._thread is not None:
            self._thread.join(timeout=5)


def reply_to_messages_request(body: dict[str, Any]) -> dict[str, Any]:
    messages: list[dict[str, Any]] = body.get("messages") or []
    prompt_indexes = [
        index for index, message in enumerate(messages) if _prompt_text(message) is not None
    ]
    last_prompt_index = prompt_indexes[-1] if prompt_indexes else -1
    prompt = _prompt_text(messages[last_prompt_index]) if prompt_indexes else ""
    outputs = [
        _block_text(block.get("content"))
        for message in messages[last_prompt_index + 1 :]
        for block in _content_blocks(message)
        if block.get("type") == "tool_result"
    ]
    reply = plan_demo_reply(prompt or "", outputs)
    content: list[dict[str, Any]]
    if isinstance(reply, DemoToolRequest):
        tool_names = [str(tool.get("name")) for tool in body.get("tools") or []]
        tool_name = next(
            (name for name in tool_names if name.endswith(f"__{reply.tool_name}")),
            reply.tool_name,
        )
        tool_use_id = f"toolu_{len(prompt_indexes)}_{reply.tool_name}"
        content = [
            {"type": "tool_use", "id": tool_use_id, "name": tool_name, "input": reply.arguments}
        ]
    else:
        content = [{"type": "text", "text": reply}]
    return {
        "id": f"msg_stub_{len(messages)}",
        "type": "message",
        "role": "assistant",
        "model": body.get("model", "claude-stub"),
        "content": content,
        "stop_reason": "tool_use" if isinstance(reply, DemoToolRequest) else "end_turn",
        "stop_sequence": None,
        "usage": STUB_USAGE,
    }


def _stream_events(message: dict[str, Any]) -> Iterator[dict[str, Any]]:
    yield {"type": "message_start", "message": {**message, "content": [], "stop_reason": None}}
    for index, block in enumerate(message["content"]):
        start: dict[str, Any]
        if block["type"] == "text":
            start = {"type": "text", "text": ""}
            delta = {"type": "text_delta", "text": block["text"]}
        else:
            start = {**block, "input": {}}
            delta = {"type": "input_json_delta", "partial_json": json.dumps(block["input"])}
        yield {"type": "content_block_start", "index": index, "content_block": start}
        yield {"type": "content_block_delta", "index": index, "delta": delta}
        yield {"type": "content_block_stop", "index": index}
    yield {
        "type": "message_delta",
        "delta": {"stop_reason": message["stop_reason"], "stop_sequence": None},
        "usage": {"output_tokens": STUB_USAGE["output_tokens"]},
    }
    yield {"type": "message_stop"}


def _prompt_text(message: dict[str, Any]) -> str | None:
    """Returns a user message's typed text; tool results and CLI reminders are not prompts."""
    if message.get("role") != "user":
        return None
    content = message.get("content")
    if isinstance(content, str):
        return content.strip() or None
    blocks = _content_blocks(message)
    if any(block.get("type") == "tool_result" for block in blocks):
        return None
    texts = [
        str(block.get("text", "")).strip()
        for block in blocks
        if block.get("type") == "text" and not str(block.get("text", "")).lstrip().startswith("<")
    ]
    return texts[-1] if texts else None


def _content_blocks(message: dict[str, Any]) -> list[dict[str, Any]]:
    content = message.get("content")
    if not isinstance(content, list):
        return []
    return [block for block in content if isinstance(block, dict)]


def _block_text(content: object) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return " ".join(
            str(block.get("text", ""))
            for block in content
            if isinstance(block, dict) and block.get("type") == "text"
        )
    return ""
