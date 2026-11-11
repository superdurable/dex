# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Image references in stored Claude Code transcript entries, bytes only for the CLI.

Stored entries never hold image bytes. In every Claude ``image`` block whose
``source`` is ``{"type": "base64", "media_type": ..., "data": <base64>}``, the
stored ``data`` is ``{"dex_image_reference": <AgentImageReference fields>}``
instead; every other key keeps its value and position, so restoring the bytes
rebuilds the exact entry the CLI wrote.
"""

from __future__ import annotations

import base64
import binascii
import hashlib
from collections.abc import Iterable, Mapping
from dataclasses import asdict
from typing import Any

from dex_agents import AgentImageReference, MediaStore, load_images

IMAGE_REFERENCE_KEY = "dex_image_reference"


class TranscriptImageCodec:
    """Swap image bytes for references before storing, and back before the CLI reads them."""

    def __init__(
        self, media_store: MediaStore, known_references: Iterable[AgentImageReference]
    ) -> None:
        self._media_store = media_store
        self._references_by_sha256 = {reference.sha256: reference for reference in known_references}

    async def strip_image_bytes(self, value: Any) -> Any:
        """Return a copy of ``value`` whose base64 image blocks hold references.

        An image without a known reference is uploaded, so images the CLI made
        itself, such as an inline tool's, are stored by reference too.

        Raises:
            ValueError: If an image's base64 is not canonical, so its bytes
                could not be restored as the CLI wrote them.
            ImageValidationError: If the media store refuses the image.
        """
        if isinstance(value, list):
            return [await self.strip_image_bytes(item) for item in value]
        if not isinstance(value, Mapping):
            return value
        source = _base64_image_source(value)
        if source is not None and isinstance(source["data"], str):
            reference = await self._reference_for(source["data"])
            stripped_source = {**source, "data": {IMAGE_REFERENCE_KEY: asdict(reference)}}
            return {
                key: stripped_source if key == "source" else item for key, item in value.items()
            }
        return {key: await self.strip_image_bytes(item) for key, item in value.items()}

    async def restore_image_bytes(self, value: Any) -> Any:
        """Return a copy of ``value`` with the bytes of every referenced image as base64.

        Raises:
            MediaStoreError: If the store cannot return an image's bytes.
        """
        references = image_references_in(value)
        if not references:
            return value
        images = await load_images(self._media_store, references)
        return _replace_references(value, images)

    async def _reference_for(self, data: str) -> AgentImageReference:
        try:
            image_bytes = base64.b64decode(data, validate=True)
        except binascii.Error as error:
            raise ValueError(
                f"the Claude Code transcript has invalid image base64: {error}"
            ) from None
        if base64.b64encode(image_bytes).decode("ascii") != data:
            raise ValueError("the Claude Code transcript has an image in non-canonical base64")
        sha256 = hashlib.sha256(image_bytes).hexdigest()
        reference = self._references_by_sha256.get(sha256)
        if reference is None:
            reference = await self._media_store.put_image(image_bytes)
            self._references_by_sha256[sha256] = reference
        return reference


def image_block(reference: AgentImageReference) -> dict[str, Any]:
    """Return the Claude image block of one reference, holding the reference, not bytes."""
    return {
        "type": "image",
        "source": {
            "type": "base64",
            "media_type": reference.mime_type,
            "data": {IMAGE_REFERENCE_KEY: asdict(reference)},
        },
    }


def image_references_in(value: Any) -> list[AgentImageReference]:
    """Return the references of the image blocks in ``value``, in document order."""
    references: list[AgentImageReference] = []
    _collect_references(value, references)
    return references


def decode_image_reference(fields: Mapping[str, Any]) -> AgentImageReference:
    return AgentImageReference(
        uri=str(fields["uri"]),
        mime_type=str(fields["mime_type"]),
        sha256=str(fields["sha256"]),
        size_bytes=int(fields["size_bytes"]),
    )


def _collect_references(value: Any, references: list[AgentImageReference]) -> None:
    if isinstance(value, list):
        for item in value:
            _collect_references(item, references)
        return
    if not isinstance(value, Mapping):
        return
    fields = _stored_reference_fields(value)
    if fields is not None:
        references.append(decode_image_reference(fields))
        return
    for item in value.values():
        _collect_references(item, references)


def _replace_references(value: Any, images: Mapping[str, bytes]) -> Any:
    if isinstance(value, list):
        return [_replace_references(item, images) for item in value]
    if not isinstance(value, Mapping):
        return value
    fields = _stored_reference_fields(value)
    if fields is not None:
        data = base64.b64encode(images[str(fields["sha256"])]).decode("ascii")
        source = {**value["source"], "data": data}
        return {key: source if key == "source" else item for key, item in value.items()}
    return {key: _replace_references(item, images) for key, item in value.items()}


def _base64_image_source(block: Mapping[str, Any]) -> Mapping[str, Any] | None:
    if block.get("type") != "image":
        return None
    source = block.get("source")
    if not isinstance(source, Mapping) or source.get("type") != "base64":
        return None
    return source


def _stored_reference_fields(block: Mapping[str, Any]) -> Mapping[str, Any] | None:
    source = _base64_image_source(block)
    data = None if source is None else source.get("data")
    if not isinstance(data, Mapping):
        return None
    fields = data.get(IMAGE_REFERENCE_KEY)
    return fields if isinstance(fields, Mapping) else None
