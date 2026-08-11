# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass
from typing import Protocol

from dex._native import NativeBlobCache


@dataclass(frozen=True)
class BlobCacheConfig:
    """Configure the local persistent cache for large Dex values.

    Attributes:
        directory: The writable directory that stores cache data and metadata.
        max_bytes: The positive maximum on-disk payload size in bytes.
        frequency_counters: Admission-policy counter count. Zero selects the default
            of 10,000; larger values improve frequency estimates at a memory cost.
    """

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
    """Provide local, bounded storage for content-addressed Dex blobs.

    Implementations may be called concurrently by Clients and Workers. Blob IDs are
    opaque server identifiers; callers should not derive their own IDs.
    """

    @property
    def config(self) -> BlobCacheConfig:
        """Return the configuration used to open this cache.

        Returns:
            The effective cache configuration.
        """
        ...

    def get(self, blob_id: str) -> bytes | None:
        """Read a cached payload without contacting Dex.

        Args:
            blob_id: The non-empty, opaque blob identifier.

        Returns:
            The payload bytes, or ``None`` when the entry is absent.
        """
        ...

    def put(self, blob_id: str, payload: bytes) -> bool:
        """Offer a payload to the bounded cache.

        Args:
            blob_id: The non-empty, opaque blob identifier.
            payload: The bytes to retain; the cache does not take mutable ownership.

        Returns:
            ``True`` when admitted, or ``False`` when the policy rejects the entry.
        """
        ...

    def delete(self, blob_id: str) -> None:
        """Delete one cached payload if present.

        Args:
            blob_id: The opaque identifier to remove.
        """
        ...

    def delete_all(self) -> None:
        """Delete every cached payload while keeping the cache open."""
        ...

    def close(self) -> None:
        """Flush metadata and release native cache resources.

        Repeated calls are safe; other methods must not be used after close.
        """
        ...


class _RustBlobCache:
    def __init__(self, config: BlobCacheConfig) -> None:
        self.config = config
        self._native = NativeBlobCache(
            config.directory,
            config.max_bytes,
            config.frequency_counters,
        )

    def get(self, blob_id: str) -> bytes | None:
        return self._native.get(blob_id)

    def put(self, blob_id: str, payload: bytes) -> bool:
        return self._native.put(blob_id, payload)

    def delete(self, blob_id: str) -> None:
        self._native.delete(blob_id)

    def delete_all(self) -> None:
        self._native.delete_all()

    def close(self) -> None:
        self._native.close()


def open_blob_cache(config: BlobCacheConfig) -> BlobCache:
    """Open or create a native BlobCache.

    Args:
        config: Valid directory, byte capacity, and admission-policy settings.

    Returns:
        An open cache owned by the caller; call :meth:`BlobCache.close` at shutdown.

    Raises:
        ValueError: If a configuration value is invalid.
        OSError: If the directory cannot be created, locked, or opened.

    Examples:
        >>> cache = open_blob_cache(BlobCacheConfig(".dex-cache", 64 * 1024**2))
        >>> try:
        ...     cache.put("blob-1", b"payload")
        ... finally:
        ...     cache.close()
    """
    return _RustBlobCache(config)
