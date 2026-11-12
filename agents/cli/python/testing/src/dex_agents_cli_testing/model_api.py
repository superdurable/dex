# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A local HTTP model API stub that logs every request with its credentials."""

from __future__ import annotations

import json
import threading
from abc import ABC, abstractmethod
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


@dataclass(frozen=True)
class ModelApiRequest:
    """One request a stub received.

    Attributes:
        method: ``GET`` or ``POST``.
        path: The path with its query.
        headers: The headers, with lowercase names.
        body: The JSON body, ``{}`` without one.
    """

    method: str
    path: str
    headers: Mapping[str, str]
    body: dict[str, Any]


@dataclass(frozen=True)
class ModelApiResponse:
    """A stub's answer: one JSON body, or server-sent events.

    Attributes:
        status: The HTTP status.
        body: The JSON body, for a non-streaming answer.
        events: ``(event name or None, JSON data)`` pairs, for a streaming answer.
        stream_end: Raw ``data:`` text sent after the events, such as ``[DONE]``.
    """

    status: int = 200
    body: Mapping[str, Any] | None = None
    events: Iterable[tuple[str | None, Mapping[str, Any]]] | None = None
    stream_end: str | None = None


class ModelApiStub(ABC):
    """Serve one model API on 127.0.0.1 until ``stop``; ``requests`` lists what it saw."""

    def __init__(self) -> None:
        """Create the stub without starting it."""
        self.requests: list[ModelApiRequest] = []
        self._server: ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None

    @property
    def base_url(self) -> str:
        """Return ``http://127.0.0.1:<port>``."""
        if self._server is None:
            raise RuntimeError("start the stub first")
        host, port = self._server.server_address[:2]
        return f"http://{host!s}:{port}"

    def model_requests(self) -> list[ModelApiRequest]:
        """Return the requests that asked the model for a reply."""
        return [request for request in self.requests if self.is_model_request(request)]

    def start(self, port: int = 0) -> None:
        """Serve on ``port``, or a free port for 0, in a background thread."""
        stub = self

        class StubRequestHandler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def do_POST(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(length) or b"{}")
                self._answer("POST", body)

            def do_GET(self) -> None:
                self._answer("GET", {})

            def log_message(self, format: str, *args: Any) -> None:
                return

            def _answer(self, method: str, body: dict[str, Any]) -> None:
                request = ModelApiRequest(
                    method,
                    self.path,
                    {name.lower(): value for name, value in self.headers.items()},
                    body,
                )
                stub.requests.append(request)
                response = stub.respond(request)
                if response.events is not None:
                    self.send_response(response.status)
                    self.send_header("Content-Type", "text/event-stream")
                    self.send_header("Cache-Control", "no-cache")
                    self.send_header("Connection", "close")
                    self.end_headers()
                    self.close_connection = True
                    try:
                        for event_name, data in response.events:
                            prefix = f"event: {event_name}\n" if event_name else ""
                            self.wfile.write(f"{prefix}data: {json.dumps(data)}\n\n".encode())
                            self.wfile.flush()
                        if response.stream_end is not None:
                            self.wfile.write(f"data: {response.stream_end}\n\n".encode())
                            self.wfile.flush()
                    except (BrokenPipeError, ConnectionResetError):
                        # A cancelled turn closes a slow stream early.
                        return
                    return
                payload = json.dumps(response.body or {}).encode()
                self.send_response(response.status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

        self._server = ThreadingHTTPServer(("127.0.0.1", port), StubRequestHandler)
        self._server.daemon_threads = True
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    def stop(self) -> None:
        """Stop serving."""
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
            self._server = None

    @abstractmethod
    def is_model_request(self, request: ModelApiRequest) -> bool:
        """Report whether a request asked the model for a reply."""

    @abstractmethod
    def respond(self, request: ModelApiRequest) -> ModelApiResponse:
        """Answer one request."""
