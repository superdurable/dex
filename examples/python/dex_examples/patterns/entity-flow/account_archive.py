# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

import threading


class FakeAccountArchive:
    """In-memory stand-in for the system that keeps records of closed accounts."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._unavailable_flow_ids: set[str] = set()
        self._archive_counts: dict[str, int] = {}

    def archive_account(self, flow_id: str) -> None:
        with self._lock:
            if flow_id in self._unavailable_flow_ids:
                raise RuntimeError("account archive is unavailable")
            self._archive_counts[flow_id] = self._archive_counts.get(flow_id, 0) + 1

    def make_unavailable(self, flow_id: str) -> None:
        with self._lock:
            self._unavailable_flow_ids.add(flow_id)

    def make_available(self, flow_id: str) -> None:
        with self._lock:
            self._unavailable_flow_ids.discard(flow_id)

    def archive_count(self, flow_id: str) -> int:
        with self._lock:
            return self._archive_counts.get(flow_id, 0)
