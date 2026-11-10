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

from dex_agents.mcp._dex_conventions import read_dex_tool_declaration
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
            digits of its SHA-256. Key ``ToolPolicy`` entries by this name. The
            resource and prompt tools have the fixed names
            ``mcp_list_resources_and_prompts``, ``mcp_read_resource``, and
            ``mcp_get_prompt``.
        server_name: Name of the configured server that offers the tool, or
            ``""`` for a resource and prompt tool, which takes the server as
            its ``server`` argument.
        mcp_tool_name: The tool's name on its MCP server, or ``""`` for a
            resource and prompt tool.
        description: Tool description for the model.
        input_schema: JSON Schema of the tool's arguments object.
        annotations: The server's behavior hints, or ``None`` when it sent none.
        framework_effect: The effect the server declares when its
            configuration trusts it, else ``None``: ``dex_effect`` when the
            tool declares one, otherwise the effect from the annotations. An
            explicit ``ToolPolicy`` overrides it, except for a tool with a
            ``dex_effect``, where ``McpToolSource.deferred_tool_call`` lets the
            policy change only approval.
        dex_effect: The effect the tool declares under
            ``TOOL_EFFECT_META_KEY`` in its definition's ``_meta``, when the
            server configuration trusts the server, else ``None``. A tool
            with a ``dex_effect`` follows the Dex MCP conventions in the
            package README: its effect never changes, and it waits for
            approval only when ``is_approval_recommended`` is ``True`` or a
            session ``ToolPolicy`` asks for approval.

            .. versionadded:: 0.5.0
        is_approval_recommended: Whether the tool declares
            ``TOOL_APPROVAL_META_KEY`` together with a ``dex_effect``. Always
            ``False`` without a ``dex_effect``.

            .. versionadded:: 0.5.0
    """

    name: str
    server_name: str
    mcp_tool_name: str
    description: str
    input_schema: Mapping[str, Any] = field(repr=False)
    annotations: McpToolAnnotations | None
    framework_effect: ToolEffect | None
    dex_effect: ToolEffect | None = None
    is_approval_recommended: bool = False


def mcp_tool_from_listing(server_name: str, tool: Tool, trusts_annotations: bool) -> McpTool:
    annotations = None
    if tool.annotations is not None:
        annotations = McpToolAnnotations(
            read_only_hint=tool.annotations.read_only_hint,
            idempotent_hint=tool.annotations.idempotent_hint,
            destructive_hint=tool.annotations.destructive_hint,
            open_world_hint=tool.annotations.open_world_hint,
        )
    declaration = (
        read_dex_tool_declaration(tool.meta, server_name, tool.name) if trusts_annotations else None
    )
    framework_effect: ToolEffect | None = None
    if declaration is not None:
        # A Dex declaration replaces the annotations, even when its effect is invalid.
        framework_effect = declaration.effect
    elif trusts_annotations:
        framework_effect = tool_effect_from_mcp_annotations(annotations)
    return McpTool(
        name=build_agent_tool_name(server_name, tool.name),
        server_name=server_name,
        mcp_tool_name=tool.name,
        description=tool.description or tool.title or f"MCP tool {tool.name} of {server_name}",
        input_schema=tool.input_schema,
        annotations=annotations,
        framework_effect=framework_effect,
        dex_effect=None if declaration is None else declaration.effect,
        is_approval_recommended=declaration is not None
        and declaration.effect is not None
        and declaration.is_approval_recommended,
    )


def build_agent_tool_name(server_name: str, mcp_tool_name: str) -> str:
    candidate = _TOOL_NAME_DISALLOWED_CHARACTERS.sub("_", f"{server_name}__{mcp_tool_name}")
    if len(candidate) <= _TOOL_NAME_LENGTH_LIMIT:
        return candidate
    digest = hashlib.sha256(candidate.encode()).hexdigest()[:8]
    return f"{candidate[:55]}_{digest}"


def render_tool_result_text(result: CallToolResult) -> str:
    """Join text blocks; other content blocks and structured content render as JSON.

    The JSON is ``serialize_tool_output_json`` text, as the TypeScript core writes it.
    """
    parts: list[str] = []
    for block in result.content:
        if isinstance(block, TextContent):
            parts.append(block.text)
        else:
            parts.append(
                serialize_tool_output_json(
                    block.model_dump(mode="json", by_alias=True, exclude_none=True)
                )
            )
    if not parts and result.structured_content is not None:
        parts.append(serialize_tool_output_json(result.structured_content))
    return "\n".join(parts)


def serialize_tool_output_json(value: Any) -> str:
    """Serialize with sorted keys, as the TypeScript core does, so both cores show the same text."""
    return json.dumps(_write_whole_floats_as_integers(value), sort_keys=True, ensure_ascii=False)


def _write_whole_floats_as_integers(value: Any) -> Any:
    # JavaScript writes 1.0 as 1; the SDK's float fields, such as priority, turn 1 into 1.0.
    if isinstance(value, float) and value.is_integer():
        return int(value)
    if isinstance(value, list):
        return [_write_whole_floats_as_integers(item) for item in value]
    if isinstance(value, dict):
        return {key: _write_whole_floats_as_integers(item) for key, item in value.items()}
    return value
