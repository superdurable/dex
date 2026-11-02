# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A gateway in front of an MCP server that breaks tools/call answers after the server ran the call.

Run ``python -m tests.fake_mcp_gateway <port> <upstream url> <mode>``. In mode ``502`` it
answers ``tools/call`` with a text/plain 502; in mode ``badjson`` with a JSON content type and a
body that is not JSON. Every other request passes through unchanged.
"""

from __future__ import annotations

import json
import sys

import httpx2
import uvicorn
from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import Response
from starlette.routing import Route

_HOP_BY_HOP_HEADERS = frozenset({"host", "content-length", "transfer-encoding", "connection"})


def build_gateway(upstream_url: str, mode: str) -> Starlette:
    async def forward(request: Request) -> Response:
        body = await request.body()
        headers = {
            name: value
            for name, value in request.headers.items()
            if name.lower() not in _HOP_BY_HOP_HEADERS
        }
        try:
            method = json.loads(body).get("method") if body else None
        except (json.JSONDecodeError, AttributeError):
            method = None
        async with httpx2.AsyncClient(timeout=60) as client:
            upstream = await client.request(
                request.method, upstream_url, content=body, headers=headers
            )
        if method == "tools/call" and mode == "502":
            return Response("Bad Gateway", status_code=502, media_type="text/plain")
        if method == "tools/call" and mode == "badjson":
            return Response("<html>oops</html>", status_code=200, media_type="application/json")
        response_headers = {
            name: value
            for name, value in upstream.headers.items()
            if name.lower() not in _HOP_BY_HOP_HEADERS | {"content-encoding"}
        }
        return Response(upstream.content, upstream.status_code, response_headers)

    return Starlette(routes=[Route("/mcp", forward, methods=["GET", "POST", "DELETE"])])


if __name__ == "__main__":
    port, upstream_url, mode = int(sys.argv[1]), sys.argv[2], sys.argv[3]
    uvicorn.run(build_gateway(upstream_url, mode), host="127.0.0.1", port=port, log_level="info")
