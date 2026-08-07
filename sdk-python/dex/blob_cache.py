# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass
from typing import Protocol

from dex._utils import PhaseNotImplementedError


@dataclass(frozen=True)
class BlobCacheConfig:
    directory: str
    max_bytes: int
    frequency_counters: int = 10_000

    def __post_init__(self) -> None:
        if not self.directory:
            raise ValueError("blob cache directory is required")
        if self.max_bytes <= 0:
            raise ValueError("blob cache max_bytes must be positive")
        if self.frequency_counters < 0:
            raise ValueError("blob cache frequency_counters must not be negative")
        if self.frequency_counters == 0:
            object.__setattr__(self, "frequency_counters", 10_000)


class BlobCache(Protocol):
    @property
    def config(self) -> BlobCacheConfig: ...

    def get(self, blob_id: str) -> bytes | None: ...

    def put(self, blob_id: str, payload: bytes) -> bool: ...

    def delete(self, blob_id: str) -> None: ...

    def delete_all(self) -> None: ...

    def close(self) -> None: ...


def open_blob_cache(config: BlobCacheConfig) -> BlobCache:
    del config
    raise PhaseNotImplementedError("BlobCache bridge belongs to a later phase")
