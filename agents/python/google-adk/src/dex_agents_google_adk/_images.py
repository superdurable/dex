# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Image references in ADK events, and their bytes in the model request.

Stored ADK events hold an image as a placeholder: a ``file_data`` part whose
``file_uri`` is the reference's ``uri`` and whose ``mime_type`` is the
reference's type. A user image is a ``types.Part`` after the text part; a tool
result image is a ``FunctionResponsePart`` in ``FunctionResponse.parts``. The
full reference comes from ``collect_turn_images`` by its URI. Right before a
model call, ``inline_image_references`` swaps each placeholder for the image's
bytes as ``inline_data``.
"""

from __future__ import annotations

from collections.abc import Iterator, Mapping, Sequence
from typing import Any

from google.adk.models import LlmRequest
from google.genai import types

from dex_agents import (
    SUPPORTED_IMAGE_MIME_TYPES,
    AgentImageReference,
    AgentToolCall,
    MediaStore,
    load_images,
)

# ADK moves Parts found in a tool result into FunctionResponse.parts and drops the emptied key.
TOOL_RESULT_IMAGES_KEY = "__dex_images__"


def image_placeholder_part(reference: AgentImageReference) -> types.Part:
    """Return the placeholder part of one image."""
    return types.Part(
        file_data=types.FileData(file_uri=reference.uri, mime_type=reference.mime_type)
    )


def with_tool_result_images(response: dict[str, Any], record: AgentToolCall) -> dict[str, Any]:
    """Add a record's images to its ADK tool result, which ADK turns into placeholders."""
    if not record.images:
        return response
    return {
        **response,
        TOOL_RESULT_IMAGES_KEY: [image_placeholder_part(image) for image in record.images],
    }


def extract_tool_result_images(result: object) -> tuple[object, list[bytes]]:
    """Take the image ``inline_data`` Parts out of an ADK tool result, as ADK does.

    Returns:
        The result without those Parts, and their bytes in order. A Part on
        its own leaves ``{}``. Parts are found at the top level and as values
        of a top-level dict or list. Other results come back unchanged.
    """
    image = _inline_image_bytes(result)
    if image is not None:
        return {}, [image]
    images: list[bytes] = []
    if isinstance(result, dict):
        kept_items: dict[Any, Any] = {}
        for key, value in result.items():
            image = _inline_image_bytes(value)
            if image is None:
                kept_items[key] = value
            else:
                images.append(image)
        return (kept_items, images) if images else (result, [])
    if isinstance(result, list | tuple):
        kept_values: list[Any] = []
        for value in result:
            image = _inline_image_bytes(value)
            if image is None:
                kept_values.append(value)
            else:
                images.append(image)
        return (kept_values, images) if images else (result, [])
    return result, []


def _inline_image_bytes(value: object) -> bytes | None:
    if not isinstance(value, types.Part) or value.inline_data is None:
        return None
    blob = value.inline_data
    if blob.data is None or blob.mime_type not in SUPPORTED_IMAGE_MIME_TYPES:
        return None
    return blob.data


def image_references_in_parts(
    parts: Sequence[types.Part], references_by_uri: Mapping[str, AgentImageReference]
) -> list[AgentImageReference]:
    """Return the references whose placeholders the parts hold, in part order."""
    return [
        references_by_uri[uri]
        for part in parts
        for uri in _placeholder_uris(part)
        if uri in references_by_uri
    ]


async def inline_image_references(
    llm_request: LlmRequest,
    store: MediaStore,
    references_by_uri: Mapping[str, AgentImageReference],
) -> None:
    """Replace the request's image placeholders with the images' bytes.

    ADK shares nested part objects with the session events, so this builds
    new contents and never changes a part in place.

    Raises:
        Exception: Any error of ``load_images``, which fails the model call.
    """
    references = [
        reference
        for content in llm_request.contents
        for reference in image_references_in_parts(content.parts or [], references_by_uri)
    ]
    if not references:
        return
    images = await load_images(store, references)
    llm_request.contents = [
        content.model_copy(
            update={
                "parts": [
                    _with_inline_image(part, references_by_uri, images)
                    for part in content.parts or []
                ]
            }
        )
        for content in llm_request.contents
    ]


def _with_inline_image(
    part: types.Part,
    references_by_uri: Mapping[str, AgentImageReference],
    images: Mapping[str, bytes],
) -> types.Part:
    reference = _placeholder_reference(part.file_data, references_by_uri)
    if reference is not None:
        return types.Part(
            inline_data=types.Blob(mime_type=reference.mime_type, data=images[reference.sha256])
        )
    function_response = part.function_response
    if function_response is None or not function_response.parts:
        return part
    response_parts: list[types.FunctionResponsePart] = []
    for response_part in function_response.parts:
        reference = _placeholder_reference(response_part.file_data, references_by_uri)
        response_parts.append(
            response_part
            if reference is None
            else types.FunctionResponsePart.from_bytes(
                data=images[reference.sha256], mime_type=reference.mime_type
            )
        )
    return part.model_copy(
        update={"function_response": function_response.model_copy(update={"parts": response_parts})}
    )


def _placeholder_uris(part: types.Part) -> Iterator[str]:
    if part.file_data is not None and part.file_data.file_uri:
        yield part.file_data.file_uri
    if part.function_response is not None:
        for response_part in part.function_response.parts or []:
            if response_part.file_data is not None and response_part.file_data.file_uri:
                yield response_part.file_data.file_uri


def _placeholder_reference(
    file_data: types.FileData | types.FunctionResponseFileData | None,
    references_by_uri: Mapping[str, AgentImageReference],
) -> AgentImageReference | None:
    if file_data is None or not file_data.file_uri:
        return None
    return references_by_uri.get(file_data.file_uri)
