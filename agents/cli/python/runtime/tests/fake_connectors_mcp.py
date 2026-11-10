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
* ``reddit_export_report`` (``readOnlyHint``): reddit/default exportReport, or the
  connection in ``$FAKE_MCP_REDDIT_CONNECTION``; a client that polls tasks gets a task.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
import uuid
from collections.abc import Mapping
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from mcp import MCPError
from mcp.server.context import CallNext, HandlerResult, ServerRequestContext
from mcp.server.extension import Extension, MethodBinding
from mcp.server.mcpserver import Context, MCPServer
from mcp_types import (
    CLIENT_CAPABILITIES_META_KEY,
    INVALID_PARAMS,
    CallToolRequestParams,
    RequestParams,
    ToolAnnotations,
)

from dex_agents.mcp import IDEMPOTENCY_KEY_META_KEY, TASKS_EXTENSION_IDENTIFIER
from dex_agents.testing import InvocationLog
from dex_agents_cli import canonical_operation_name

STATE_DIR_ENVIRONMENT_VARIABLE = "FAKE_MCP_STATE_DIR"
#: The connection ``slack_post_thread_reply`` runs on; a restart can move the name to another.
SLACK_CONNECTION_ENVIRONMENT_VARIABLE = "FAKE_MCP_SLACK_CONNECTION"
#: The connection ``reddit_export_report`` runs on.
REDDIT_CONNECTION_ENVIRONMENT_VARIABLE = "FAKE_MCP_REDDIT_CONNECTION"
INVOCATION_LOG_NAME = "mcp-invocations.log"
REQUEST_META_LOG_NAME = "mcp-request-meta.jsonl"
READY_LINE = "Uvicorn running on"


class _ReportTasks(Extension):
    """Answers ``reddit_export_report`` with a task that completes on its second poll."""

    identifier = TASKS_EXTENSION_IDENTIFIER

    def __init__(self) -> None:
        self._tasks: dict[str, dict[str, Any]] = {}

    def methods(self) -> list[MethodBinding]:
        return [MethodBinding("tasks/get", _TaskIdParams, self._get_task)]

    async def intercept_tool_call(
        self,
        params: CallToolRequestParams,
        ctx: ServerRequestContext[Any, Any],
        call_next: CallNext,
    ) -> HandlerResult:
        if params.name != "reddit_export_report" or not _declares_tasks(ctx):
            return await call_next(ctx)
        _record_request("reddit_export_report", ctx.meta or {})
        now = datetime.now(UTC).isoformat()
        task_id = uuid.uuid4().hex
        task: dict[str, Any] = {
            "taskId": task_id,
            "status": "working",
            "createdAt": now,
            "lastUpdatedAt": now,
            "ttlMs": 60_000,
            "pollIntervalMs": 50,
        }
        self._tasks[task_id] = {**task, "polls": 0}
        return {"resultType": "task", **task}

    async def _get_task(
        self, ctx: ServerRequestContext[Any, Any], params: _TaskIdParams
    ) -> HandlerResult:
        stored = self._tasks.get(params.task_id)
        if stored is None:
            raise MCPError(INVALID_PARAMS, f"unknown task {params.task_id}")
        stored["polls"] += 1
        task = {key: value for key, value in stored.items() if key != "polls"}
        if stored["polls"] >= 2:
            task["status"] = "completed"
            task["result"] = {"content": [{"type": "text", "text": "report exported"}]}
        return {"resultType": "complete", **task}


class _TaskIdParams(RequestParams):
    task_id: str


def _declares_tasks(ctx: ServerRequestContext[Any, Any]) -> bool:
    capabilities = (ctx.meta or {}).get(CLIENT_CAPABILITIES_META_KEY)
    extensions = capabilities.get("extensions") if isinstance(capabilities, dict) else None
    return isinstance(extensions, dict) and TASKS_EXTENSION_IDENTIFIER in extensions


server = MCPServer("fake-connectors", extensions=[_ReportTasks()])


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


@server.tool(
    annotations=ToolAnnotations(read_only_hint=True),
    meta=_meta(
        "reddit",
        os.environ.get(REDDIT_CONNECTION_ENVIRONMENT_VARIABLE, "default"),
        "exportReport",
    ),
)
def reddit_export_report(context: Context) -> str:
    """Export a report; a client that polls tasks gets a task instead."""
    _record("reddit_export_report", context)
    return "report exported at once"


def _record(tool_name: str, context: Context) -> None:
    _record_request(tool_name, context.request_context.meta or {})


def _record_request(tool_name: str, meta: Mapping[str, Any]) -> None:
    key = meta.get(IDEMPOTENCY_KEY_META_KEY, "no-key")
    state_dir = Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
    InvocationLog(state_dir / INVOCATION_LOG_NAME).record(f"{tool_name} {key}")
    with (state_dir / REQUEST_META_LOG_NAME).open("a") as log:
        log.write(json.dumps({"tool": tool_name, "meta": dict(meta)}, sort_keys=True) + "\n")


if __name__ == "__main__":
    asyncio.run(server.run_streamable_http_async(port=int(sys.argv[1])))
