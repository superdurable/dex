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

"""Query-string and response helpers shared by the example controllers."""

from __future__ import annotations

import time

from quart import Response, abort, jsonify, request


def required_query(name: str) -> str:
    value = request.args.get(name)
    if not value:
        abort(400, description=f"{name} is required")
    return value


def optional_query(name: str, default: str) -> str:
    value = request.args.get(name)
    return default if value is None else value


def required_int_query(name: str) -> int:
    value = required_query(name)
    try:
        return int(value)
    except ValueError:
        abort(400, description=f"{name} must be an integer")


async def required_body_field(name: str) -> str:
    body = await request.get_json(silent=True)
    value = body.get(name) if isinstance(body, dict) else None
    if not isinstance(value, str) or not value:
        abort(400, description=f"{name} is required in the request body")
    return value


async def required_bool_body_field(name: str) -> bool:
    body = await request.get_json(silent=True)
    value = body.get(name) if isinstance(body, dict) else None
    if not isinstance(value, bool):
        abort(400, description=f"{name} must be a boolean in the request body")
    return value


def new_flow_id(prefix: str) -> str:
    return f"{prefix}-{time.time_ns()}"


def started_flow(flow_id: str, run_id: str) -> Response:
    return jsonify({"flowID": flow_id, "runID": run_id})


def accepted() -> Response:
    return jsonify({})
