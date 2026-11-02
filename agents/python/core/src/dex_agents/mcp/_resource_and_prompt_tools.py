# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Read tools for MCP resources and prompts, which ``McpToolSource`` offers when enabled."""

from __future__ import annotations

import json
from collections.abc import Awaitable, Callable, Mapping, Sequence
from typing import Any

from mcp import Client, MCPError
from mcp_types import METHOD_NOT_FOUND
from pydantic import BaseModel

from dex_agents.mcp._tools import McpTool, McpToolAnnotations
from dex_agents.runner import ToolExecutionError
from dex_agents.values import ToolEffect

#: Name of the read tool that lists a server's resources, resource templates, and prompts.
MCP_LIST_RESOURCES_AND_PROMPTS_TOOL_NAME = "mcp_list_resources_and_prompts"

#: Name of the read tool that reads one resource of a server.
MCP_READ_RESOURCE_TOOL_NAME = "mcp_read_resource"

#: Name of the read tool that gets one prompt of a server.
MCP_GET_PROMPT_TOOL_NAME = "mcp_get_prompt"

RESOURCE_AND_PROMPT_TOOL_NAMES = frozenset(
    {
        MCP_LIST_RESOURCES_AND_PROMPTS_TOOL_NAME,
        MCP_READ_RESOURCE_TOOL_NAME,
        MCP_GET_PROMPT_TOOL_NAME,
    }
)
_LISTING_PAGE_LIMIT = 100


def build_resource_and_prompt_tools(server_names: Sequence[str]) -> tuple[McpTool, ...]:
    """Return the three read tools; the ``server`` argument names one of ``server_names``."""
    server = {
        "type": "string",
        "enum": list(server_names),
        "description": "Name of a configured MCP server.",
    }
    definitions: list[tuple[str, str, dict[str, Any]]] = [
        (
            MCP_LIST_RESOURCES_AND_PROMPTS_TOOL_NAME,
            "List the resources, resource templates, and prompts of a configured MCP server.",
            {"server": server},
        ),
        (
            MCP_READ_RESOURCE_TOOL_NAME,
            "Read one resource of a configured MCP server by its URI.",
            {
                "server": server,
                "uri": {
                    "type": "string",
                    "description": "URI of the resource, as the server lists it.",
                },
            },
        ),
        (
            MCP_GET_PROMPT_TOOL_NAME,
            "Get one prompt of a configured MCP server, rendered with its arguments.",
            {
                "server": server,
                "name": {
                    "type": "string",
                    "description": "Name of the prompt, as the server lists it.",
                },
                "arguments": {
                    "type": "object",
                    "additionalProperties": {"type": "string"},
                    "description": "Prompt arguments by name. Defaults to none.",
                },
            },
        ),
    ]
    return tuple(
        McpTool(
            name=name,
            server_name="",
            mcp_tool_name="",
            description=description,
            input_schema={
                "type": "object",
                "properties": properties,
                "required": [key for key in properties if key != "arguments"],
                "additionalProperties": False,
            },
            annotations=McpToolAnnotations(read_only_hint=True),
            framework_effect=ToolEffect.READ,
        )
        for name, description, properties in definitions
    )


def read_server_name(arguments: Mapping[str, Any]) -> str:
    """Return the ``server`` argument, or raise the ``ToolExecutionError`` the model sees."""
    server_name = arguments.get("server")
    if not isinstance(server_name, str):
        raise ToolExecutionError("the server argument must be a string")
    return server_name


async def run_resource_and_prompt_tool(
    client: Client, tool_name: str, arguments: Mapping[str, Any]
) -> str:
    """Send one read tool's requests on an open connection and return the output JSON text."""
    try:
        if tool_name == MCP_LIST_RESOURCES_AND_PROMPTS_TOOL_NAME:
            output: dict[str, Any] = {
                "resources": await _list_all(client.list_resources, "resources"),
                "resource_templates": await _list_all(
                    client.list_resource_templates, "resource_templates"
                ),
                "prompts": await _list_all(client.list_prompts, "prompts"),
            }
        elif tool_name == MCP_READ_RESOURCE_TOOL_NAME:
            uri = arguments.get("uri")
            if not isinstance(uri, str):
                raise ToolExecutionError("the uri argument must be a string")
            read = await client.read_resource(uri)
            output = {"contents": [_wire_json(content) for content in read.contents]}
        else:
            name = arguments.get("name")
            if not isinstance(name, str):
                raise ToolExecutionError("the name argument must be a string")
            prompt_arguments = arguments.get("arguments")
            if prompt_arguments is not None and (
                not isinstance(prompt_arguments, dict)
                or not all(isinstance(value, str) for value in prompt_arguments.values())
            ):
                raise ToolExecutionError("the arguments argument must map names to strings")
            prompt = await client.get_prompt(name, prompt_arguments)
            output = {"messages": [_wire_json(message) for message in prompt.messages]}
            if prompt.description is not None:
                output["description"] = prompt.description
    except MCPError as error:
        raise ToolExecutionError(f"MCP error {error.code}: {error.message}") from error
    return serialize_tool_output_json(output)


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


async def _list_all(list_page: Callable[..., Awaitable[Any]], field_name: str) -> list[Any]:
    items: list[Any] = []
    cursor: str | None = None
    for _ in range(_LISTING_PAGE_LIMIT):
        try:
            page = await list_page(cursor=cursor)
        except MCPError as error:
            if error.code == METHOD_NOT_FOUND:
                # The server does not offer this kind of item.
                return items
            raise
        items.extend(_wire_json(item) for item in getattr(page, field_name))
        cursor = page.next_cursor
        if not cursor:
            break
    return items


def _wire_json(model: BaseModel) -> Any:
    # Only the fields the server sent, under their wire names.
    return model.model_dump(mode="json", by_alias=True, exclude_unset=True)
