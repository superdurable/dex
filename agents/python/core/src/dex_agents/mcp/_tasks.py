# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Wire types of the MCP tasks extension, io.modelcontextprotocol/tasks (SEP-2663).

The MCP SDK 2.3 has no client for the extension, so the tool source declares
it, claims the ``"task"`` result type, and polls ``tasks/get`` itself.
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any, Literal

from mcp.client.extension import ClaimContext, ClientExtension, ResultClaim
from mcp_types import CallToolResult, Request, RequestParams, Result

#: Identifier of the MCP tasks extension that ``McpToolSource`` declares on every request.
TASKS_EXTENSION_IDENTIFIER = "io.modelcontextprotocol/tasks"
TERMINAL_TASK_STATUSES = frozenset({"completed", "failed", "cancelled"})


class McpTaskSnapshot(Result):
    """Task fields shared by ``CreateTaskResult`` and the ``tasks/get`` result."""

    task_id: str
    status: str
    status_message: str | None = None
    poll_interval_ms: int | None = None
    result: dict[str, Any] | None = None
    error: dict[str, Any] | None = None


class CreatedMcpTask(McpTaskSnapshot):
    """``CreateTaskResult``: a ``tools/call`` answered with a task handle."""

    result_type: Literal["task"]


class PolledMcpTask(McpTaskSnapshot):
    """``tasks/get`` result; ``input_required`` tasks carry ``inputRequests``."""

    input_requests: dict[str, Any] | None = None


class McpTaskIdParams(RequestParams):
    task_id: str


class GetMcpTaskRequest(Request[McpTaskIdParams, Literal["tasks/get"]]):
    method: Literal["tasks/get"] = "tasks/get"
    name_param = "taskId"


class CancelMcpTaskRequest(Request[McpTaskIdParams, Literal["tasks/cancel"]]):
    method: Literal["tasks/cancel"] = "tasks/cancel"
    name_param = "taskId"


class McpTasksClientExtension(ClientExtension):
    """Declares the tasks extension so servers may answer ``tools/call`` with a task."""

    identifier = TASKS_EXTENSION_IDENTIFIER

    def claims(self) -> Sequence[ResultClaim[Any]]:
        return (
            ResultClaim(
                result_type="task", model=CreatedMcpTask, resolve=_reject_resolution_by_client
            ),
        )


async def _reject_resolution_by_client(
    task: CreatedMcpTask, context: ClaimContext
) -> CallToolResult:
    # McpToolSource calls the session with allow_claimed=True and polls with heartbeats itself.
    raise RuntimeError(f"MCP task {task.task_id} must be polled by McpToolSource")
