# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Image references in stored LangChain messages, and their bytes in chat model input."""

from __future__ import annotations

import base64
from collections.abc import Mapping, Sequence
from dataclasses import asdict
from typing import Any, TypeVar

from dex_agents import (
    AgentImageReference,
    ImageValidationError,
    MediaStore,
    ToolExecutionError,
    load_images,
)
from langchain_core.messages import BaseMessage

MessageT = TypeVar("MessageT", bound=BaseMessage)

# Placeholder: a standard image block with url=uri, mime_type, and the reference under this key.
IMAGE_REFERENCE_EXTRAS_KEY = "dex_image_reference"


def image_reference_block(reference: AgentImageReference) -> dict[str, Any]:
    return {
        "type": "image",
        "url": reference.uri,
        "mime_type": reference.mime_type,
        "extras": {IMAGE_REFERENCE_EXTRAS_KEY: asdict(reference)},
    }


def image_reference_from_block(block: object) -> AgentImageReference | None:
    if not isinstance(block, Mapping) or block.get("type") != "image":
        return None
    extras = block.get("extras")
    fields = extras.get(IMAGE_REFERENCE_EXTRAS_KEY) if isinstance(extras, Mapping) else None
    if not isinstance(fields, Mapping):
        return None
    return AgentImageReference(
        uri=str(fields["uri"]),
        mime_type=str(fields["mime_type"]),
        sha256=str(fields["sha256"]),
        size_bytes=int(fields["size_bytes"]),
        type=str(fields.get("type", "image")),
    )


def message_image_references(message: BaseMessage) -> list[AgentImageReference]:
    if not isinstance(message.content, list):
        return []
    return [
        reference
        for block in message.content
        if (reference := image_reference_from_block(block)) is not None
    ]


async def inline_message_images(
    messages: Sequence[MessageT], media_store: MediaStore | None
) -> list[MessageT] | None:
    references = [
        reference for message in messages for reference in message_image_references(message)
    ]
    if not references:
        return None
    if media_store is None:
        raise RuntimeError(
            "the session history holds image references, but the runner has no media_store "
            "to read their bytes"
        )
    images = await load_images(media_store, references)
    # Copies, so the graph state, and so stored state, keeps its placeholders.
    return [
        message.model_copy(update={"content": _inline_content_images(message.content, images)})
        if message_image_references(message)
        else message
        for message in messages
    ]


async def upload_content_images(
    content: list[Any], media_store: MediaStore, tool_name: str
) -> tuple[list[Any], list[AgentImageReference]]:
    """Upload a tool's inline image blocks and return the other blocks and the references.

    An image block with ``base64`` data or a ``data:`` URL is uploaded; the store
    detects its type from the bytes, whatever ``mime_type`` declares. Other
    blocks, such as an image ``url`` on the web, stay in the content.

    Raises:
        ToolExecutionError: An inline image is not valid base64 or not an image
            the store accepts, so its bytes never reach the tool output.
    """
    remaining: list[Any] = []
    references: list[AgentImageReference] = []
    for block in content:
        encoded = _inline_image_base64(block)
        if encoded is None:
            remaining.append(block)
            continue
        number = len(references) + 1
        try:
            data = base64.b64decode(encoded, validate=True)
        except ValueError:
            # binascii.Error, or a ValueError for text that is not ASCII.
            data = None
        if data is None or base64.b64encode(data).decode("ascii") != encoded:
            raise ToolExecutionError(
                f"tool {tool_name} returned image {number}, which is not valid base64"
            )
        try:
            references.append(await media_store.put_image(data))
        except ImageValidationError as error:
            raise ToolExecutionError(
                f"tool {tool_name} returned image {number}, which is not a supported image: {error}"
            ) from None
    return remaining, references


def _inline_image_base64(block: object) -> str | None:
    if not isinstance(block, Mapping) or block.get("type") != "image":
        return None
    if isinstance(block.get("base64"), str):
        return str(block["base64"])
    url = block.get("url")
    if isinstance(url, str) and url.startswith("data:"):
        header, separator, encoded = url.partition(",")
        # Only base64 data URLs carry image bytes; refuse other encodings instead of keeping them.
        return encoded if separator and header.endswith(";base64") else ""
    return None


def _inline_content_images(content: Any, images: Mapping[str, bytes]) -> list[Any]:
    inlined: list[Any] = []
    for block in content:
        reference = image_reference_from_block(block)
        inlined.append(
            block
            if reference is None
            else {
                "type": "image",
                "base64": base64.b64encode(images[reference.sha256]).decode("ascii"),
                "mime_type": reference.mime_type,
            }
        )
    return inlined
