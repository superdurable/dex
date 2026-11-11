# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A MediaStore over one local directory, shared by a test and its Worker children.

``InMemoryMediaStore`` cannot cross the Worker process boundary, so the
session tests keep image bytes here: one file per distinct image, named by
its SHA-256.
"""

from __future__ import annotations

import os
from pathlib import Path

from dex_agents import (
    AgentImageReference,
    MediaStoreError,
    build_image_reference,
    require_store_image_reference,
    verify_image_bytes,
)

DIRECTORY_MEDIA_STORE_URI = "directory://test-media"


class DirectoryMediaStore:
    def __init__(self, directory: Path) -> None:
        self._directory = directory
        self.store_uri = DIRECTORY_MEDIA_STORE_URI

    @property
    def image_files(self) -> list[Path]:
        if not self._directory.exists():
            return []
        return sorted(path for path in self._directory.iterdir() if not path.name.startswith("."))

    async def put_image(self, data: bytes) -> AgentImageReference:
        reference = build_image_reference(data, self.store_uri)
        self._directory.mkdir(parents=True, exist_ok=True)
        path = self._directory / reference.sha256
        if not path.exists():
            # A rename is atomic, so a reader never sees a partial image.
            staging = self._directory / f".{reference.sha256}.{os.getpid()}"
            staging.write_bytes(data)
            staging.replace(path)
        return reference

    async def get_image(self, reference: AgentImageReference) -> bytes:
        require_store_image_reference(reference, self.store_uri)
        path = self._directory / reference.sha256
        if not path.exists():
            raise MediaStoreError(f"image {reference.uri} is not in the media store")
        return verify_image_bytes(reference, path.read_bytes())
