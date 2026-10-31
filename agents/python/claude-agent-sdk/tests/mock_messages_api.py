# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted local stand-in for the Anthropic Messages API, for the real Claude Code CLI.

``MockMessagesApi`` serves ``POST /v1/messages`` on 127.0.0.1 with streaming
SSE or JSON replies and logs every request. The scripted model reads commands
from the last prompt text of the conversation:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with every earlier prompt.
* ``call <tool> <json>`` calls one tool; join calls with `` && `` for one reply
  with several tool uses. A tool name matches the request's tool list
  exactly or as the ``mcp__<server>__<name>`` suffix. Once every call has a
  result, it answers ``<tool>: <result>`` joined with ``; ``.
"""

from __future__ import annotations

import json
import threading
import uuid
from collections.abc import Iterator
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

MOCK_MODEL_NAME = "claude-mock"
MOCK_USAGE = {
    "input_tokens": 10,
    "output_tokens": 5,
    "cache_read_input_tokens": 3,
    "cache_creation_input_tokens": 0,
}


@dataclass
class MessagesApiRequest:
    path: str
    api_key: str | None
    body: dict[str, Any]


@dataclass
class MockMessagesApi:
    """Serve the scripted model until ``stop``; ``requests`` lists every request seen."""

    requests: list[MessagesApiRequest] = field(default_factory=list)
    _server: ThreadingHTTPServer | None = None
    _thread: threading.Thread | None = None

    @property
    def base_url(self) -> str:
        if self._server is None:
            raise RuntimeError("start the mock Messages API first")
        host, port = self._server.server_address[:2]
        return f"http://{host!s}:{port}"

    def start(self) -> None:
        api = self

        class MessagesApiRequestHandler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(length) or b"{}")
                api.requests.append(
                    MessagesApiRequest(self.path, self.headers.get("x-api-key"), body)
                )
                if not self.path.split("?", 1)[0].endswith("/v1/messages"):
                    self._send_json(404, {"type": "error", "error": {"type": "not_found_error"}})
                    return
                message = scripted_reply(body)
                if body.get("stream"):
                    self._send_sse(message)
                else:
                    self._send_json(200, message)

            def do_GET(self) -> None:
                api.requests.append(
                    MessagesApiRequest(self.path, self.headers.get("x-api-key"), {})
                )
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

            def _send_sse(self, message: dict[str, Any]) -> None:
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.end_headers()
                for event in stream_events(message):
                    frame = f"event: {event['type']}\ndata: {json.dumps(event)}\n\n"
                    self.wfile.write(frame.encode())
                    self.wfile.flush()

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), MessagesApiRequestHandler)
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    def stop(self) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
        if self._thread is not None:
            self._thread.join(timeout=5)


def scripted_reply(body: dict[str, Any]) -> dict[str, Any]:
    tool_names = [str(tool.get("name")) for tool in body.get("tools") or []]
    messages = body.get("messages") or []
    prompts = [text for message in messages if (text := _prompt_text(message)) is not None]
    command = prompts[-1] if prompts else ""
    content: list[dict[str, Any]]
    if command.startswith("say "):
        content = [_text_block(f"said: {command.removeprefix('say ')}")]
    elif command == "recall":
        content = [_text_block("history: " + " | ".join(prompts[:-1]))]
    elif command.startswith("call "):
        content = _next_tool_content(command, messages, tool_names)
    else:
        content = [_text_block(f"unknown command: {command[:80]}")]
    has_tool_use = any(block["type"] == "tool_use" for block in content)
    return {
        "id": f"msg_{uuid.uuid4().hex[:20]}",
        "type": "message",
        "role": "assistant",
        "model": MOCK_MODEL_NAME,
        "content": content,
        "stop_reason": "tool_use" if has_tool_use else "end_turn",
        "stop_sequence": None,
        "usage": MOCK_USAGE,
    }


def stream_events(message: dict[str, Any]) -> Iterator[dict[str, Any]]:
    yield {"type": "message_start", "message": {**message, "content": [], "stop_reason": None}}
    for index, block in enumerate(message["content"]):
        if block["type"] == "text":
            yield {
                "type": "content_block_start",
                "index": index,
                "content_block": {"type": "text", "text": ""},
            }
            yield {
                "type": "content_block_delta",
                "index": index,
                "delta": {"type": "text_delta", "text": block["text"]},
            }
        else:
            yield {
                "type": "content_block_start",
                "index": index,
                "content_block": {**block, "input": {}},
            }
            yield {
                "type": "content_block_delta",
                "index": index,
                "delta": {"type": "input_json_delta", "partial_json": json.dumps(block["input"])},
            }
        yield {"type": "content_block_stop", "index": index}
    yield {
        "type": "message_delta",
        "delta": {"stop_reason": message["stop_reason"], "stop_sequence": None},
        "usage": {"output_tokens": MOCK_USAGE["output_tokens"]},
    }
    yield {"type": "message_stop"}


def _next_tool_content(
    command: str, messages: list[dict[str, Any]], tool_names: list[str]
) -> list[dict[str, Any]]:
    requests = []
    for request_index, call in enumerate(command.split(" && ")):
        _, tool_name, tool_input = call.split(" ", 2)
        requests.append((request_index, _resolve_tool_name(tool_name, tool_names), tool_input))
    results = _latest_results_by_request(messages)
    if not results:
        return [
            {
                "type": "tool_use",
                "id": f"toolu_{request_index}_{uuid.uuid4().hex[:12]}",
                "name": tool_name,
                "input": json.loads(tool_input),
            }
            for request_index, tool_name, tool_input in requests
        ]
    for request_index, tool_name, tool_input in requests:
        if "again by itself" in results.get(request_index, ""):
            return [
                {
                    "type": "tool_use",
                    "id": f"toolu_{request_index}_{uuid.uuid4().hex[:12]}",
                    "name": tool_name,
                    "input": json.loads(tool_input),
                }
            ]
    summary = "; ".join(
        f"{tool_name.rsplit('__', 1)[-1]}: {results.get(request_index, '')}"
        for request_index, tool_name, _ in requests
    )
    return [_text_block(summary)]


def _latest_results_by_request(messages: list[dict[str, Any]]) -> dict[int, str]:
    last_prompt_index = max(
        (index for index, message in enumerate(messages) if _prompt_text(message) is not None),
        default=-1,
    )
    results: dict[int, str] = {}
    for message in messages[last_prompt_index + 1 :]:
        content = message.get("content")
        if not isinstance(content, list):
            continue
        for block in content:
            if isinstance(block, dict) and block.get("type") == "tool_result":
                tool_use_id = str(block.get("tool_use_id", ""))
                if tool_use_id.startswith("toolu_"):
                    request_index = int(tool_use_id.split("_")[1])
                    results[request_index] = _block_text(block.get("content"))
    return results


def _resolve_tool_name(tool_name: str, tool_names: list[str]) -> str:
    for candidate in tool_names:
        if candidate == tool_name or candidate.endswith(f"__{tool_name}"):
            return candidate
    return tool_name


def _prompt_text(message: dict[str, Any]) -> str | None:
    if message.get("role") != "user":
        return None
    content = message.get("content")
    if isinstance(content, str):
        return content.strip() or None
    if not isinstance(content, list):
        return None
    texts = [
        str(block.get("text", ""))
        for block in content
        if isinstance(block, dict) and block.get("type") == "text"
    ]
    has_tool_result = any(
        isinstance(block, dict) and block.get("type") == "tool_result" for block in content
    )
    commands = [text.strip() for text in texts if not text.lstrip().startswith("<")]
    if has_tool_result or not commands:
        return None
    return commands[-1]


def _block_text(content: object) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(
            str(block.get("text", ""))
            for block in content
            if isinstance(block, dict) and block.get("type") == "text"
        )
    return ""


def _text_block(text: str) -> dict[str, Any]:
    return {"type": "text", "text": text}
