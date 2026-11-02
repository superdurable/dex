# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A local MCP server for the tool source tests, served over stdio or Streamable HTTP.

Run ``python -m tests.fake_mcp_server stdio`` or ``... http <port>``. Every
real tool run appends ``<tool> <idempotency key>`` to the ``InvocationLog`` in
``$FAKE_MCP_STATE_DIR``. Tools:

* ``lookup`` (``readOnlyHint``): ``"crash_first": true`` exits the process
  during its first run, breaking the response stream.
* ``audit_lookup`` (``readOnlyHint``): a read that the test agent's policy
  turns into a write.
* ``record_note`` (``idempotentHint``).
* ``send_message`` (no annotations): ``"gate"`` waits at a ``FileGate``;
  ``"crash": true`` exits the process mid-call.
* ``long_report`` (``readOnlyHint``): answers with a task handle that
  ``tasks/get`` completes on its second poll. With ``"gate"``, every poll
  marks that ``FileGate`` and the task completes once the gate is released.
* ``ask_account`` (``readOnlyHint``): answers ``input_required``.
* ``repeat_request`` (``readOnlyHint``): always answers a state-only
  ``input_required``.
* ``reject_order`` (``readOnlyHint``): returns an ``isError`` result.
* ``purge_cache`` (``idempotentHint`` and ``destructiveHint``).
* ``audit.lookup``: its agent tool name collides with ``audit_lookup``.
"""

from __future__ import annotations

import asyncio
import os
import sys
import uuid
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from mcp import MCPError
from mcp.server.context import CallNext, HandlerResult, ServerRequestContext
from mcp.server.extension import Extension, MethodBinding
from mcp.server.mcpserver import Context, MCPServer
from mcp.server.mcpserver.exceptions import ToolError
from mcp_types import (
    CLIENT_CAPABILITIES_META_KEY,
    INVALID_PARAMS,
    CallToolRequestParams,
    ElicitRequest,
    ElicitRequestFormParams,
    InputRequiredResult,
    RequestParams,
    ToolAnnotations,
)

from dex_agents.mcp import IDEMPOTENCY_KEY_META_KEY, TASKS_EXTENSION_IDENTIFIER
from dex_agents.testing import FileGate, InvocationLog

STATE_DIR_ENVIRONMENT_VARIABLE = "FAKE_MCP_STATE_DIR"
INVOCATION_LOG_NAME = "mcp-invocations.log"
READY_LINE = "Uvicorn running on"

_READ_ONLY = ToolAnnotations(read_only_hint=True)


class _TaskIdParams(RequestParams):
    task_id: str


class _FakeTasksExtension(Extension):
    identifier = TASKS_EXTENSION_IDENTIFIER

    def __init__(self) -> None:
        self._tasks: dict[str, dict[str, Any]] = {}

    def methods(self) -> list[MethodBinding]:
        return [
            MethodBinding("tasks/get", _TaskIdParams, self._get_task),
            MethodBinding("tasks/cancel", _TaskIdParams, self._cancel_task),
        ]

    async def intercept_tool_call(
        self,
        params: CallToolRequestParams,
        ctx: ServerRequestContext[Any, Any],
        call_next: CallNext,
    ) -> HandlerResult:
        if params.name == "ask_account":
            return InputRequiredResult(
                input_requests={
                    "account": ElicitRequest(
                        params=ElicitRequestFormParams(
                            message="Which account?",
                            requested_schema={
                                "type": "object",
                                "properties": {"account": {"type": "string"}},
                            },
                        )
                    )
                }
            )
        if params.name == "repeat_request":
            return InputRequiredResult(request_state="repeat")
        if params.name == "long_report" and _declares_tasks(ctx):
            _record(params.name, ctx)
            now = datetime.now(UTC).isoformat()
            task_id = uuid.uuid4().hex
            task: dict[str, Any] = {
                "taskId": task_id,
                "status": "working",
                "statusMessage": "writing the report",
                "createdAt": now,
                "lastUpdatedAt": now,
                "ttlMs": 60_000,
                "pollIntervalMs": 50,
            }
            arguments = params.arguments or {}
            self._tasks[task_id] = {
                **task,
                "polls": 0,
                "topic": arguments.get("topic"),
                "gate": arguments.get("gate"),
            }
            return {"resultType": "task", **task}
        return await call_next(ctx)

    async def _get_task(
        self, ctx: ServerRequestContext[Any, Any], params: _TaskIdParams
    ) -> HandlerResult:
        stored = self._tasks.get(params.task_id)
        if stored is None:
            raise MCPError(INVALID_PARAMS, f"unknown task {params.task_id}")
        stored["polls"] += 1
        task = {
            key: value for key, value in stored.items() if key not in ("polls", "topic", "gate")
        }
        gate = None if stored["gate"] is None else FileGate(_state_dir(), str(stored["gate"]))
        if gate is not None:
            gate.mark_reached()
        if (gate is None and stored["polls"] >= 2) or (gate is not None and gate.is_released()):
            task["status"] = "completed"
            task["statusMessage"] = "done"
            task["result"] = {"content": [{"type": "text", "text": f"report on {stored['topic']}"}]}
        return {"resultType": "complete", **task}

    async def _cancel_task(
        self, ctx: ServerRequestContext[Any, Any], params: _TaskIdParams
    ) -> HandlerResult:
        _invocations().record(f"cancel {params.task_id}")
        return {}


server = MCPServer("fake-mcp", extensions=[_FakeTasksExtension()])


@server.tool(annotations=_READ_ONLY)
def lookup(query: str, ctx: Context, crash_first: bool = False) -> str:
    """Look up a record."""
    is_first_run = not any(name.startswith("lookup ") for name in _invocations().names())
    _record("lookup", ctx.request_context)
    if crash_first and is_first_run:
        os._exit(1)
    return f"found {query}"


@server.tool(annotations=_READ_ONLY)
def audit_lookup(query: str, ctx: Context) -> str:
    """Look up a record and write an audit entry."""
    _record("audit_lookup", ctx.request_context)
    return f"audited {query}"


@server.tool(annotations=ToolAnnotations(idempotent_hint=True))
def record_note(note: str, ctx: Context) -> str:
    """Store a note; repeating a call changes nothing."""
    _record("record_note", ctx.request_context)
    return f"noted {note}"


@server.tool()
async def send_message(
    text: str, ctx: Context, gate: str | None = None, crash: bool = False
) -> str:
    """Send a message to a person."""
    _record("send_message", ctx.request_context)
    if crash:
        os._exit(1)
    if gate is not None:
        file_gate = FileGate(_state_dir(), gate)
        file_gate.mark_reached()
        await file_gate.wait_for_release()
    await ctx.report_progress(1, 1, f"sent {text}")
    return f"sent {text}"


@server.tool(annotations=_READ_ONLY)
def long_report(topic: str, gate: str | None = None) -> str:
    """Write a long report; answers with a task handle."""
    return f"report on {topic} without a task"


@server.tool(annotations=_READ_ONLY)
def ask_account() -> str:
    """Ask which account to use."""
    return "never reached"


@server.tool(annotations=_READ_ONLY)
def repeat_request() -> str:
    """Ask for the same request again, forever."""
    return "never reached"


@server.tool(annotations=ToolAnnotations(idempotent_hint=True, destructive_hint=True))
def purge_cache(key: str) -> str:
    """Delete one cache entry; deleting it again changes nothing."""
    return f"purged {key}"


@server.tool(name="audit.lookup")
def audit_lookup_with_a_colliding_name(query: str) -> str:
    """Collides with audit_lookup once the name is turned into an agent tool name."""
    return f"colliding {query}"


@server.tool(annotations=_READ_ONLY)
def reject_order(order_id: str) -> str:
    """Reject an order."""
    raise ToolError(f"order {order_id} is already shipped")


def _state_dir() -> Path:
    return Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])


def _invocations() -> InvocationLog:
    return InvocationLog(_state_dir() / INVOCATION_LOG_NAME)


def _record(tool_name: str, request_context: ServerRequestContext[Any, Any]) -> None:
    key = (request_context.meta or {}).get(IDEMPOTENCY_KEY_META_KEY, "no-key")
    _invocations().record(f"{tool_name} {key}")


def _declares_tasks(request_context: ServerRequestContext[Any, Any]) -> bool:
    capabilities = (request_context.meta or {}).get(CLIENT_CAPABILITIES_META_KEY)
    extensions = capabilities.get("extensions") if isinstance(capabilities, dict) else None
    return isinstance(extensions, dict) and TASKS_EXTENSION_IDENTIFIER in extensions


if __name__ == "__main__":
    if sys.argv[1] == "stdio":
        server.run("stdio")
    else:
        asyncio.run(server.run_streamable_http_async(port=int(sys.argv[2])))
