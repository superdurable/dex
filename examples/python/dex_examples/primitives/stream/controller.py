# Copyright (c) 2026 Super Durable, Inc.
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

from datetime import timedelta

from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import optional_query, required_query, started_flow


def create_stream_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("primitive_stream", __name__, url_prefix="/primitives/stream")

    @blueprint.get("/start")
    async def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = await app_state.client.start_flow(
            app_state.stream,
            flow_id,
            required_query("input"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/write")
    async def write() -> str:
        await app_state.client.write_stream(
            required_query("workflowId"),
            app_state.stream.progress,
            required_query("idempotencyKey"),
            required_query("message"),
        )
        return "done"

    @blueprint.get("/read")
    async def read() -> Response:
        message = await app_state.client.read_stream(
            required_query("workflowId"),
            app_state.stream.progress,
            optional_query("resumeToken", ""),
            timedelta(seconds=20),
        )
        return jsonify(
            value=message.value,
            resume_token=message.resume_token,
            created_time=message.created_time.isoformat(),
            idempotency_key=message.idempotency_key,
        )

    return blueprint
