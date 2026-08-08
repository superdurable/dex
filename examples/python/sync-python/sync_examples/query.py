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

from uuid import uuid4

from flask import Response, jsonify, request


def required_query(name: str) -> str:
    value = request.args.get(name)
    if value is None or value == "":
        raise ValueError(f"missing query parameter {name}")
    return value


def optional_query(name: str, default: str) -> str:
    value = request.args.get(name)
    if value is None or value == "":
        return default
    return value


def required_int_query(name: str) -> int:
    return int(required_query(name))


def new_flow_id(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex}"


def started_flow(flow_id: str, run_id: str) -> Response:
    return jsonify({"workflowId": flow_id, "runId": run_id})
