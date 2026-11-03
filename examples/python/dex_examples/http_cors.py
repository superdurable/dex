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

from typing import Any

CORS_HEADERS = {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
    "Access-Control-Allow-Headers": "Origin, Content-Type, Accept, Authorization",
}
# Routes that serve their own page and take a session token answer only same-origin requests.
SAME_ORIGIN_PATH_PREFIXES = ("/products/ai-agent/",)


def allows_cross_origin(path: str) -> bool:
    return not path.startswith(SAME_ORIGIN_PATH_PREFIXES)


def apply_cors_headers(response: Any) -> Any:
    for name, value in CORS_HEADERS.items():
        response.headers[name] = value
    return response


def install_quart_cors(app: Any) -> None:
    from quart import make_response, request

    @app.before_request
    async def handle_cors_preflight() -> Any:
        if request.method == "OPTIONS" and allows_cross_origin(request.path):
            return apply_cors_headers(await make_response("", 204))
        return None

    @app.after_request
    async def add_cors_headers(response: Any) -> Any:
        return apply_cors_headers(response) if allows_cross_origin(request.path) else response


def install_flask_cors(app: Any) -> None:
    from flask import make_response, request

    @app.before_request
    def handle_cors_preflight() -> Any:
        if request.method == "OPTIONS" and allows_cross_origin(request.path):
            return apply_cors_headers(make_response("", 204))
        return None

    @app.after_request
    def add_cors_headers(response: Any) -> Any:
        return apply_cors_headers(response) if allows_cross_origin(request.path) else response
