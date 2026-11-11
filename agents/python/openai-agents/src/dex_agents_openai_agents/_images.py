# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Image placeholders in stored SDK input items, and their bytes in each model request.

A stored ``input_image`` part's ``image_url`` is the reference's ``uri``; the
model input filter swaps in a ``data:`` URL of the bytes.
"""

from __future__ import annotations

import base64
import inspect
import re
from collections.abc import Iterator, Mapping, Sequence
from typing import Any, Literal

from agents import ToolOutputImage, ToolOutputText
from agents.run_config import CallModelData, CallModelInputFilter, ModelInputData

from dex_agents import AgentImageReference, MediaStore, load_images

IMAGE_DETAIL: Literal["auto"] = "auto"
# A reference URI ends in /sha256/<sha256>; other image URLs are the application's own.
_PLACEHOLDER_PATTERN = re.compile(r"/sha256/[0-9a-f]{64}$")
_PART_LIST_KEYS = ("content", "output")


def build_input_image_part(reference: AgentImageReference) -> dict[str, Any]:
    return {"type": "input_image", "image_url": reference.uri, "detail": IMAGE_DETAIL}


def build_user_image_input_item(text: str, images: Sequence[AgentImageReference]) -> dict[str, Any]:
    content = [{"type": "input_text", "text": text}, *map(build_input_image_part, images)]
    return {"role": "user", "content": content}


def build_tool_image_outputs(
    text: str, images: Sequence[AgentImageReference]
) -> list[ToolOutputText | ToolOutputImage]:
    """Return a tool result the SDK turns into a ``function_call_output`` with image parts."""
    images_output = [
        ToolOutputImage(image_url=reference.uri, detail=IMAGE_DETAIL) for reference in images
    ]
    return [ToolOutputText(text=text), *images_output]


def has_input_image_parts(parts: Any) -> bool:
    return isinstance(parts, list) and any(_is_input_image_part(part) for part in parts)


class ImageBytesInputFilter:
    """Give one model call the bytes of every image placeholder, after the application's filter."""

    def __init__(
        self,
        media_store: MediaStore,
        references: Sequence[AgentImageReference],
        application_filter: CallModelInputFilter | None,
    ) -> None:
        self._media_store = media_store
        self._references_by_uri = {reference.uri: reference for reference in references}
        self._application_filter = application_filter

    async def __call__(self, call: CallModelData[Any]) -> ModelInputData:
        model_data = call.model_data
        if self._application_filter is not None:
            filtered = self._application_filter(call)
            model_data = await filtered if inspect.isawaitable(filtered) else filtered
        uris = list(dict.fromkeys(_iterate_placeholder_uris(model_data.input)))
        if not uris:
            return model_data
        references = [self._find_reference(uri) for uri in uris]
        contents = await load_images(self._media_store, references)
        data_urls = {
            reference.uri: _data_url(reference.mime_type, contents[reference.sha256])
            for reference in references
        }
        # Items are shared with the run state, so changed items are new dicts.
        items = [_replace_placeholders(item, data_urls) for item in model_data.input]
        return ModelInputData(input=items, instructions=model_data.instructions)

    def _find_reference(self, uri: str) -> AgentImageReference:
        reference = self._references_by_uri.get(uri)
        if reference is None:
            raise RuntimeError(
                f"the model input shows image {uri}, which no message or tool call of the session "
                "references"
            )
        return reference


def _iterate_placeholder_uris(items: Sequence[Any]) -> Iterator[str]:
    for item in items:
        if not isinstance(item, Mapping):
            continue
        for key in _PART_LIST_KEYS:
            parts = item.get(key)
            if isinstance(parts, list):
                for part in parts:
                    if _is_placeholder_part(part):
                        yield part["image_url"]


def _replace_placeholders(item: Any, data_urls: Mapping[str, str]) -> Any:
    if not isinstance(item, Mapping):
        return item
    replaced = dict(item)
    for key in _PART_LIST_KEYS:
        parts = item.get(key)
        if isinstance(parts, list) and any(_is_placeholder_part(part) for part in parts):
            replaced[key] = [
                {**part, "image_url": data_urls[part["image_url"]]}
                if _is_placeholder_part(part)
                else part
                for part in parts
            ]
    return replaced


def _is_input_image_part(part: Any) -> bool:
    return isinstance(part, Mapping) and part.get("type") == "input_image"


def _is_placeholder_part(part: Any) -> bool:
    if not _is_input_image_part(part):
        return False
    image_url = part.get("image_url")
    return (
        isinstance(image_url, str)
        and _PLACEHOLDER_PATTERN.search(image_url) is not None
        and not image_url.startswith("data:")
    )


def _data_url(mime_type: str, data: bytes) -> str:
    return f"data:{mime_type};base64,{base64.b64encode(data).decode('ascii')}"
