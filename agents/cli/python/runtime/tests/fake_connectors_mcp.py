# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A fake connectors MCP server: ``python -m tests.fake_connectors_mcp <port>``.

Each tool's ``_meta`` names its connector, connection, and operation with the
keys of the connectors library's MCP server. Every real run appends ``<tool> <idempotency
key>`` to ``mcp-invocations.log`` in ``$FAKE_MCP_STATE_DIR``.

* ``hackernews_get_top_stories`` (``readOnlyHint``): hackernews/default getTopStories.
* ``gmail_get_message`` (``readOnlyHint``): gmail/personal getMessage.
* ``slack_post_thread_reply`` (no hints): slack/support-slack postThreadReply, or the
  connection in ``$FAKE_MCP_SLACK_CONNECTION``.
* ``misc_untagged`` (``readOnlyHint``): no connector ``_meta`` at all.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
from pathlib import Path
from typing import Any

from mcp.server.mcpserver import Context, MCPServer
from mcp_types import ToolAnnotations

from dex_agents.mcp import IDEMPOTENCY_KEY_META_KEY
from dex_agents.testing import InvocationLog
from dex_agents_cli import canonical_operation_name

STATE_DIR_ENVIRONMENT_VARIABLE = "FAKE_MCP_STATE_DIR"
#: The connection ``slack_post_thread_reply`` runs on; a restart can move the name to another.
SLACK_CONNECTION_ENVIRONMENT_VARIABLE = "FAKE_MCP_SLACK_CONNECTION"
INVOCATION_LOG_NAME = "mcp-invocations.log"
REQUEST_META_LOG_NAME = "mcp-request-meta.jsonl"
READY_LINE = "Uvicorn running on"

server = MCPServer("fake-connectors")


def _meta(connector: str, connection: str, operation: str) -> dict[str, Any]:
    # The keys the connectors library's MCP server sends, spelled out to pin the contract.
    return {
        "io.superdurable.connectors/connector": connector,
        "io.superdurable.connectors/connection": connection,
        "io.superdurable.connectors/operation": operation,
        "io.superdurable.connectors/operationName": canonical_operation_name(operation),
        "io.superdurable.connectors/connectorVersion": "v0.1.0",
    }


@server.tool(
    annotations=ToolAnnotations(read_only_hint=True),
    meta=_meta("hackernews", "default", "getTopStories"),
)
def hackernews_get_top_stories(context: Context) -> str:
    """List the top Hacker News stories."""
    _record("hackernews_get_top_stories", context)
    return "1. Durable agents; 2. Local first"


@server.tool(
    annotations=ToolAnnotations(read_only_hint=True),
    meta=_meta("gmail", "personal", "getMessage"),
)
def gmail_get_message(message_id: str, context: Context) -> str:
    """Read one email."""
    _record("gmail_get_message", context)
    return f"message {message_id}: hello"


@server.tool(
    meta=_meta(
        "slack",
        os.environ.get(SLACK_CONNECTION_ENVIRONMENT_VARIABLE, "support-slack"),
        "postThreadReply",
    )
)
def slack_post_thread_reply(thread: str, text: str, context: Context) -> str:
    """Reply in a Slack thread."""
    _record("slack_post_thread_reply", context)
    return f"posted {text!r} in {thread}"


@server.tool(annotations=ToolAnnotations(read_only_hint=True))
def misc_untagged(context: Context) -> str:
    """A tool without connector metadata, hidden while connectors.enabled is set."""
    _record("misc_untagged", context)
    return "untagged"


def _record(tool_name: str, context: Context) -> None:
    meta = context.request_context.meta or {}
    key = meta.get(IDEMPOTENCY_KEY_META_KEY, "no-key")
    state_dir = Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
    InvocationLog(state_dir / INVOCATION_LOG_NAME).record(f"{tool_name} {key}")
    with (state_dir / REQUEST_META_LOG_NAME).open("a") as log:
        log.write(json.dumps({"tool": tool_name, "meta": dict(meta)}, sort_keys=True) + "\n")


if __name__ == "__main__":
    asyncio.run(server.run_streamable_http_async(port=int(sys.argv[1])))
