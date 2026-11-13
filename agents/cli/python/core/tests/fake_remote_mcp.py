# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A fake stateless remote MCP server: ``python -m tests.fake_remote_mcp <port> <token>``.

Every request must carry ``Authorization: Bearer <token>``; any other gets HTTP 401
and is counted in ``rejected-requests.log`` in ``$FAKE_MCP_STATE_DIR``. Every real
tool run appends ``<tool> <idempotency key>`` to ``mcp-invocations.log`` there.

* ``search_assets`` (``readOnlyHint``): returns the query it searched for.
* ``publish_asset`` (no hints): returns the asset it published.
"""

from __future__ import annotations

import asyncio
import os
import sys
from pathlib import Path
from typing import Any

import uvicorn
from mcp.server.mcpserver import Context, MCPServer
from mcp_types import ToolAnnotations

from dex_agents.mcp import IDEMPOTENCY_KEY_META_KEY
from dex_agents.testing import InvocationLog
from tests.fake_connectors_mcp import (
    INVOCATION_LOG_NAME,
    READY_LINE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
)

REJECTED_REQUESTS_LOG_NAME = "rejected-requests.log"
__all__ = ["READY_LINE", "REJECTED_REQUESTS_LOG_NAME"]

server = MCPServer("fake-remote")


@server.tool(annotations=ToolAnnotations(read_only_hint=True))
def search_assets(query: str, context: Context) -> str:
    """Search the asset library."""
    _record("search_assets", context)
    return f"found 2 assets for {query}"


@server.tool()
def publish_asset(asset: str, context: Context) -> str:
    """Publish one asset."""
    _record("publish_asset", context)
    return f"published {asset}"


def _record(tool_name: str, context: Context) -> None:
    key = (context.request_context.meta or {}).get(IDEMPOTENCY_KEY_META_KEY, "no-key")
    state_dir = Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
    InvocationLog(state_dir / INVOCATION_LOG_NAME).record(f"{tool_name} {key}")


class _BearerTokenCheck:
    """Answer 401 to any HTTP request without the expected bearer token."""

    def __init__(self, application: Any, token: str) -> None:
        self._application = application
        self._expected = f"Bearer {token}".encode()

    async def __call__(self, scope: Any, receive: Any, send: Any) -> None:
        if scope["type"] == "http" and dict(scope["headers"]).get(b"authorization") != (
            self._expected
        ):
            state_dir = Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
            with (state_dir / REJECTED_REQUESTS_LOG_NAME).open("a") as log:
                log.write(f"{scope['method']}\n")
            await send({"type": "http.response.start", "status": 401, "headers": []})
            await send({"type": "http.response.body", "body": b"unauthorized"})
            return
        await self._application(scope, receive, send)


async def _serve(port: int, token: str) -> None:
    application = server.streamable_http_app(stateless_http=True, json_response=True)
    config = uvicorn.Config(
        _BearerTokenCheck(application, token), host="127.0.0.1", port=port, log_level="info"
    )
    await uvicorn.Server(config).serve()


if __name__ == "__main__":
    asyncio.run(_serve(int(sys.argv[1]), sys.argv[2]))
