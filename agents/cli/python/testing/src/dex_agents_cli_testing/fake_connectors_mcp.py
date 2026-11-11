# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A fake connectors MCP server: ``python -m dex_agents_cli_testing.fake_connectors_mcp <port>``.

It serves a Hacker News read and a Slack write with the connector ``_meta``
keys the CLI core filters by, and logs every tool run to
``$FAKE_MCP_STATE_DIR/mcp-invocations.log``.
"""

from __future__ import annotations

import asyncio
import os
import sys
from pathlib import Path
from typing import Any

from mcp.server.mcpserver import Context, MCPServer
from mcp_types import ToolAnnotations

from dex_agents.testing import InvocationLog
from dex_agents_cli_core import canonical_operation_name

STATE_DIR_ENVIRONMENT_VARIABLE = "FAKE_MCP_STATE_DIR"
INVOCATION_LOG_NAME = "mcp-invocations.log"
READY_LINE = "Uvicorn running on"

server = MCPServer("fake-connectors")


def _meta(connector: str, connection: str, operation: str) -> dict[str, Any]:
    return {
        "io.superdurable.connectors/connector": connector,
        "io.superdurable.connectors/connection": connection,
        "io.superdurable.connectors/operation": operation,
        "io.superdurable.connectors/operationName": canonical_operation_name(operation),
    }


@server.tool(
    annotations=ToolAnnotations(read_only_hint=True),
    meta=_meta("hackernews", "default", "getTopStories"),
)
def hackernews_get_top_stories(context: Context) -> str:
    """List the top Hacker News stories."""
    _record("hackernews_get_top_stories")
    return "1. Durable agents; 2. Local first"


@server.tool(meta=_meta("slack", "support-slack", "postThreadReply"))
def slack_post_thread_reply(thread: str, text: str, context: Context) -> str:
    """Reply in a Slack thread."""
    _record("slack_post_thread_reply")
    return f"posted {text} in {thread}"


def _record(tool_name: str) -> None:
    state_dir = Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
    InvocationLog(state_dir / INVOCATION_LOG_NAME).record(tool_name)


if __name__ == "__main__":
    asyncio.run(server.run_streamable_http_async(port=int(sys.argv[1])))
