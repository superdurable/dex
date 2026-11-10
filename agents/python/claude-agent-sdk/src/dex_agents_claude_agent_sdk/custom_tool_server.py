# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The in-process MCP server that serves custom and source tools by the CLI's tool use ID."""

from __future__ import annotations

from typing import Any

from claude_agent_sdk import McpSdkServerConfig, _build_meta, _convert_tool_content
from mcp.server import Server
from mcp.server.context import ServerRequestContext
from mcp.types import (
    CallToolRequestParams,
    CallToolResult,
    ListToolsResult,
    PaginatedRequestParams,
    Tool,
)

from dex_agents_claude_agent_sdk.tool_gate import (
    CLAUDE_CODE_TOOL_USE_ID_META_KEY,
    ToolCallGate,
    ToolCatalog,
)

REPLAY_ONLY_TOOL_DESCRIPTION = "Not available now: its tool source does not offer it."


def build_custom_tool_server(
    tool_catalog: ToolCatalog, tool_call_gate: ToolCallGate
) -> McpSdkServerConfig:
    # create_sdk_mcp_server drops the request _meta, which carries the CLI's tool use ID.
    wire_tools = [
        Tool.model_validate(
            {
                "name": custom_tool.name,
                "description": custom_tool.description,
                "inputSchema": tool_catalog.input_schema(custom_tool.name),
                "annotations": custom_tool.annotations,
                "_meta": _build_meta(custom_tool),
            }
        )
        for custom_tool in tool_catalog.custom_tools
    ]
    # No annotations: the CLI runs unannotated MCP tools one at a time, in the model's order.
    wire_tools += [
        Tool.model_validate(
            {
                "name": source_tool.definition.name,
                "description": source_tool.definition.description,
                "inputSchema": dict(source_tool.definition.input_schema),
            }
        )
        for source_tool in tool_catalog.source_tools
    ]
    # Served so the CLI can resume a recorded call; the gate refuses new calls.
    wire_tools += [
        Tool.model_validate(
            {
                "name": tool_name,
                "description": REPLAY_ONLY_TOOL_DESCRIPTION,
                "inputSchema": {"type": "object"},
            }
        )
        for tool_name in tool_catalog.replay_only_tool_names
    ]

    async def list_custom_tools(
        context: ServerRequestContext[Any, Any], params: PaginatedRequestParams | None
    ) -> ListToolsResult:
        return ListToolsResult(tools=wire_tools)

    async def call_custom_tool(
        context: ServerRequestContext[Any, Any], params: CallToolRequestParams
    ) -> CallToolResult:
        if not tool_catalog.is_served_tool_name(params.name):
            return _error_result(f"Tool '{params.name}' not found")
        custom_tool = tool_catalog.custom_tool(params.name)
        meta = params.meta or {}
        tool_use_id = meta.get(CLAUDE_CODE_TOOL_USE_ID_META_KEY)
        try:
            result = await tool_call_gate.call_custom_tool(
                params.name,
                tool_use_id if isinstance(tool_use_id, str) else None,
                dict(params.arguments or {}),
                None if custom_tool is None else custom_tool.handler,
            )
        except Exception as error:
            return _error_result(str(error) or type(error).__name__)
        return CallToolResult.model_validate(
            {
                "content": _convert_tool_content(result.get("content", [])),
                "isError": bool(result.get("is_error", False)),
            }
        )

    server: Server[Any] = Server(
        tool_catalog.custom_tool_server_name,
        version="1.0.0",
        on_list_tools=list_custom_tools,
        on_call_tool=call_custom_tool,
    )
    return McpSdkServerConfig(
        type="sdk", name=tool_catalog.custom_tool_server_name, instance=server
    )


def _error_result(text: str) -> CallToolResult:
    return CallToolResult.model_validate(
        {"content": [{"type": "text", "text": text}], "isError": True}
    )
