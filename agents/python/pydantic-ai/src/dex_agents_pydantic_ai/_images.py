# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Image references in stored Pydantic AI messages, and their bytes in model requests."""

from __future__ import annotations

from collections.abc import Iterable, Mapping, Sequence
from dataclasses import asdict, replace
from typing import Any

from pydantic_ai import (
    BinaryContent,
    ImageUrl,
    ModelMessage,
    ModelRequest,
    ToolReturnPart,
    UserPromptPart,
)

from dex_agents import (
    SUPPORTED_IMAGE_MIME_TYPES,
    AgentImageReference,
    MediaStore,
    load_images,
)

# Placeholder: ImageUrl(url=uri, media_type, identifier=sha256) with the reference under this key.
IMAGE_REFERENCE_METADATA_KEY = "dex_image_reference"


def image_reference_placeholder(reference: AgentImageReference) -> ImageUrl:
    return ImageUrl(
        url=reference.uri,
        media_type=reference.mime_type,
        identifier=reference.sha256,
        vendor_metadata={IMAGE_REFERENCE_METADATA_KEY: asdict(reference)},
    )


def placeholder_image_reference(content: object) -> AgentImageReference | None:
    if not isinstance(content, ImageUrl) or not content.vendor_metadata:
        return None
    fields = content.vendor_metadata.get(IMAGE_REFERENCE_METADATA_KEY)
    if not isinstance(fields, Mapping):
        return None
    return AgentImageReference(
        uri=str(fields["uri"]),
        mime_type=str(fields["mime_type"]),
        sha256=str(fields["sha256"]),
        size_bytes=int(fields["size_bytes"]),
        type=str(fields.get("type", "image")),
    )


def tool_return_image_references(message: ModelMessage) -> list[AgentImageReference]:
    if not isinstance(message, ModelRequest):
        return []
    return [
        reference
        for part in message.parts
        if isinstance(part, ToolReturnPart)
        for reference in _content_image_references(part.content)
    ]


async def inline_image_bytes(
    messages: Sequence[ModelMessage], media_store: MediaStore | None
) -> list[ModelMessage] | None:
    # New message and part objects, so the run's own history keeps its placeholders.
    references = [
        reference
        for message in messages
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, UserPromptPart | ToolReturnPart)
        for reference in _content_image_references(part.content)
    ]
    if not references:
        return None
    if media_store is None:
        raise RuntimeError(
            "the session history holds image references, but the runner has no media_store "
            "to read their bytes"
        )
    images = await load_images(media_store, references)
    return [_inline_message_images(message, images) for message in messages]


async def upload_tool_return_images(
    tool_return: ToolReturnPart, media_store: MediaStore
) -> list[AgentImageReference]:
    return [
        await media_store.put_image(file.data)
        for file in tool_return.files
        if isinstance(file, BinaryContent) and file.media_type in SUPPORTED_IMAGE_MIME_TYPES
    ]


def _inline_message_images(message: ModelMessage, images: Mapping[str, bytes]) -> ModelMessage:
    if not isinstance(message, ModelRequest):
        return message
    parts = [
        replace(part, content=_inline_content_images(part.content, images))
        if isinstance(part, UserPromptPart | ToolReturnPart)
        and _content_image_references(part.content)
        else part
        for part in message.parts
    ]
    return replace(message, parts=parts)


def _inline_content_images(content: Any, images: Mapping[str, bytes]) -> Any:
    reference = placeholder_image_reference(content)
    if reference is not None:
        return BinaryContent(data=images[reference.sha256], media_type=reference.mime_type)
    if isinstance(content, list):
        return [_inline_content_images(item, images) for item in content]
    return content


def _content_image_references(content: object) -> list[AgentImageReference]:
    items: Iterable[object] = content if isinstance(content, list) else (content,)
    return [
        reference for item in items if (reference := placeholder_image_reference(item)) is not None
    ]
