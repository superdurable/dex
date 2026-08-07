# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

class NativeBlobCache:
    def __init__(
        self,
        directory: str,
        max_bytes: int,
        frequency_counters: int,
    ) -> None: ...
    def get(self, blob_id: str) -> bytes | None: ...
    def put(self, blob_id: str, payload: bytes) -> bool: ...
    def delete(self, blob_id: str) -> None: ...
    def delete_all(self) -> None: ...
    def close(self) -> None: ...
