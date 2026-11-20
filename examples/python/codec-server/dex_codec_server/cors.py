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

"""Answers CORS for the configured Dex Web origins only."""

from __future__ import annotations

from collections.abc import Iterable

from aiohttp import web


class CorsOriginAllowList:
    """Decide CORS for exact, configured origins. ``*`` is never allowed."""

    def __init__(self, origins: Iterable[str], allow_credentials: bool) -> None:
        """Remember the origins.

        Args:
            origins: Lowercase ``scheme://host[:port]`` origins.
            allow_credentials: Whether browsers may send cookies.
        """
        self._origins = frozenset(origins)
        self._allow_credentials = allow_credentials

    def is_allowed_origin(self, origin: str) -> bool:
        """Return whether ``origin`` is configured, ignoring case."""
        return origin.lower() in self._origins

    def set_allowed_origin_headers(self, response: web.StreamResponse, origin: str) -> None:
        """Add the headers that let an allowed origin read the response."""
        response.headers["Access-Control-Allow-Origin"] = origin
        if self._allow_credentials:
            response.headers["Access-Control-Allow-Credentials"] = "true"

    @staticmethod
    def set_preflight_headers(response: web.StreamResponse) -> None:
        """Add the headers that answer a CORS preflight."""
        response.headers["Access-Control-Allow-Methods"] = "POST"
        response.headers["Access-Control-Allow-Headers"] = "Authorization, Content-Type"
        response.headers["Access-Control-Max-Age"] = "600"
