# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Agent tool definitions derived from MCP tools, and their effect classification."""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any

from mcp_types import CallToolResult, TextContent, Tool

from dex_agents.values import ToolEffect

# Model APIs accept tool names of at most 64 letters, digits, '_', and '-'.
_TOOL_NAME_LENGTH_LIMIT = 64
_TOOL_NAME_DISALLOWED_CHARACTERS = re.compile(r"[^A-Za-z0-9_-]")


@dataclass(frozen=True, kw_only=True)
class McpToolAnnotations:
    """Hold the behavior hints an MCP server declares for one tool.

    Annotations are hints, not guarantees. ``McpToolSource`` uses
    ``read_only_hint`` and ``idempotent_hint`` to classify a tool only when
    its server configuration trusts the server's annotations.

    Attributes:
        read_only_hint: MCP ``readOnlyHint``: the tool does not modify its
            environment. ``None`` when the server omits it.
        idempotent_hint: MCP ``idempotentHint``: repeating a call with the
            same arguments has no further effect. ``None`` when omitted.
        destructive_hint: MCP ``destructiveHint``: the tool may perform
            destructive updates. ``None`` when omitted.
        open_world_hint: MCP ``openWorldHint``: the tool interacts with
            external entities. ``None`` when omitted.
    """

    read_only_hint: bool | None = None
    idempotent_hint: bool | None = None
    destructive_hint: bool | None = None
    open_world_hint: bool | None = None


def tool_effect_from_mcp_annotations(annotations: McpToolAnnotations | None) -> ToolEffect | None:
    """Return the tool effect that MCP annotations declare.

    Pass the result as ``framework_effect`` to
    ``AgentTurnContext.resolve_tool_policy``, so an explicit ``ToolPolicy``
    still wins. ``McpToolSource`` already does this for its tools; call this
    function when a framework hands you MCP annotations directly.

    Example:
        >>> tool_effect_from_mcp_annotations(McpToolAnnotations(read_only_hint=True))
        <ToolEffect.READ: 'read'>

    Args:
        annotations: The tool's annotations, or ``None`` when it has none.

    Returns:
        ``READ`` when ``read_only_hint`` is ``True``; otherwise
        ``IDEMPOTENT_WRITE`` when ``idempotent_hint`` is ``True``; otherwise
        ``None``, so the session's ``default_tool_effect``, ``UNCLASSIFIED`` by
        default, applies.
    """
    if annotations is None:
        return None
    if annotations.read_only_hint is True:
        return ToolEffect.READ
    if annotations.idempotent_hint is True:
        return ToolEffect.IDEMPOTENT_WRITE
    return None


@dataclass(frozen=True, kw_only=True)
class McpTool:
    """Describe one MCP tool as an agent tool definition.

    ``McpToolSource.list_tools`` returns these. Offer each to the model with
    its ``name``, ``description``, and ``input_schema``; the model then calls
    the tool by ``name``.

    Attributes:
        name: Agent tool name, ``"<server_name>__<mcp_tool_name>"``. Characters
            other than letters, digits, ``_``, and ``-`` become ``_``. A name
            longer than 64 characters is cut to 55 and ends with ``_`` and 8 hex
            digits of its SHA-256. Key ``ToolPolicy`` entries by this name.
        server_name: Name of the configured server that offers the tool.
        mcp_tool_name: The tool's name on its MCP server.
        description: Tool description for the model.
        input_schema: JSON Schema of the tool's arguments object.
        annotations: The server's behavior hints, or ``None`` when it sent none.
        framework_effect: Effect from the annotations when the server
            configuration trusts them, else ``None``. An explicit
            ``ToolPolicy`` overrides it.
    """

    name: str
    server_name: str
    mcp_tool_name: str
    description: str
    input_schema: Mapping[str, Any] = field(repr=False)
    annotations: McpToolAnnotations | None
    framework_effect: ToolEffect | None


def mcp_tool_from_listing(server_name: str, tool: Tool, trusts_annotations: bool) -> McpTool:
    annotations = None
    if tool.annotations is not None:
        annotations = McpToolAnnotations(
            read_only_hint=tool.annotations.read_only_hint,
            idempotent_hint=tool.annotations.idempotent_hint,
            destructive_hint=tool.annotations.destructive_hint,
            open_world_hint=tool.annotations.open_world_hint,
        )
    return McpTool(
        name=build_agent_tool_name(server_name, tool.name),
        server_name=server_name,
        mcp_tool_name=tool.name,
        description=tool.description or tool.title or f"MCP tool {tool.name} of {server_name}",
        input_schema=tool.input_schema,
        annotations=annotations,
        framework_effect=tool_effect_from_mcp_annotations(annotations)
        if trusts_annotations
        else None,
    )


def build_agent_tool_name(server_name: str, mcp_tool_name: str) -> str:
    candidate = _TOOL_NAME_DISALLOWED_CHARACTERS.sub("_", f"{server_name}__{mcp_tool_name}")
    if len(candidate) <= _TOOL_NAME_LENGTH_LIMIT:
        return candidate
    digest = hashlib.sha256(candidate.encode()).hexdigest()[:8]
    return f"{candidate[:55]}_{digest}"


def render_tool_result_text(result: CallToolResult) -> str:
    """Join text blocks; other content blocks and structured content render as JSON."""
    parts: list[str] = []
    for block in result.content:
        if isinstance(block, TextContent):
            parts.append(block.text)
        else:
            parts.append(
                json.dumps(block.model_dump(mode="json", by_alias=True, exclude_none=True))
            )
    if not parts and result.structured_content is not None:
        parts.append(json.dumps(result.structured_content))
    return "\n".join(parts)
