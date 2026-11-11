# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Image content of MCP tool results, stored in a media store as image references."""

from __future__ import annotations

import base64
import re

from mcp_types import CallToolResult, ImageContent, TextContent

from dex_agents.mcp._tools import render_tool_result_text, serialize_tool_output_json
from dex_agents.media import MAX_IMAGES_PER_MESSAGE, ImageValidationError, MediaStore
from dex_agents.runner import ToolExecutionError, ToolExecutionResult
from dex_agents.values import AgentImageReference

# Canonical padded base64, the same check as the TypeScript core's.
_BASE64_PATTERN = re.compile(r"(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?")


def has_image_content(result: CallToolResult) -> bool:
    """Report whether a result carries at least one ``image`` content block."""
    return any(isinstance(block, ImageContent) for block in result.content)


def render_tool_failure_text(result: CallToolResult) -> str:
    """Render a failed result's text with ``[image <n> omitted]`` for each image block.

    The text becomes ``AgentToolCall.error``, so image bytes never reach session state.
    """
    if not has_image_content(result):
        return render_tool_result_text(result)
    parts: list[str] = []
    image_count = 0
    for block in result.content:
        if isinstance(block, TextContent):
            parts.append(block.text)
        elif isinstance(block, ImageContent):
            image_count += 1
            parts.append(f"[image {image_count} omitted]")
        else:
            parts.append(
                serialize_tool_output_json(
                    block.model_dump(mode="json", by_alias=True, exclude_none=True)
                )
            )
    return "\n".join(parts)


async def store_tool_result_images(
    result: CallToolResult, store: MediaStore, tool_name: str
) -> ToolExecutionResult:
    """Upload a successful result's image blocks and return the tool output with their references.

    Each image block becomes ``[image <n>]`` in the output text, ``n`` counting
    from 1 in ``ToolExecutionResult.images`` order. A result without image
    blocks keeps today's text.

    Raises:
        ToolExecutionError: The result has more than ``MAX_IMAGES_PER_MESSAGE``
            images, or an image is not valid base64 or not an image the store accepts.
    """
    if not has_image_content(result):
        return ToolExecutionResult(output=render_tool_result_text(result))
    image_blocks = [block for block in result.content if isinstance(block, ImageContent)]
    if len(image_blocks) > MAX_IMAGES_PER_MESSAGE:
        raise ToolExecutionError(
            f"MCP tool {tool_name} returned {len(image_blocks)} images; "
            f"a tool result carries at most {MAX_IMAGES_PER_MESSAGE}"
        )
    references: list[AgentImageReference] = []
    parts: list[str] = []
    for block in result.content:
        if isinstance(block, TextContent):
            parts.append(block.text)
        elif isinstance(block, ImageContent):
            references.append(await _store_image(block, store, tool_name, len(references) + 1))
            parts.append(f"[image {len(references)}]")
        else:
            parts.append(
                serialize_tool_output_json(
                    block.model_dump(mode="json", by_alias=True, exclude_none=True)
                )
            )
    return ToolExecutionResult(output="\n".join(parts), images=references)


async def _store_image(
    block: ImageContent, store: MediaStore, tool_name: str, number: int
) -> AgentImageReference:
    data = base64.b64decode(block.data) if _BASE64_PATTERN.fullmatch(block.data) else None
    # Re-encoding refuses unused trailing bits, so one image has one accepted text.
    if data is None or base64.b64encode(data).decode("ascii") != block.data:
        raise ToolExecutionError(
            f"MCP tool {tool_name} returned image {number}, which is not valid base64"
        )
    try:
        return await store.put_image(data)
    except ImageValidationError as error:
        raise ToolExecutionError(
            f"MCP tool {tool_name} returned image {number}, which is not a supported image: {error}"
        ) from None
