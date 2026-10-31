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

from dex import FlowNotFoundError
from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import required_query, started_flow


def create_attribute_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("primitive_attribute", __name__, url_prefix="/primitives/attribute")

    @blueprint.get("/start")
    async def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = await app_state.client.start_flow(
            app_state.attribute,
            flow_id,
            required_query("message"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/read")
    async def read_attributes() -> Response:
        flow = app_state.attribute
        payment_progress = flow.progress.instance("payment")
        try:
            values = await app_state.client.get_attributes(
                required_query("workflowId"),
                flow.status,
                payment_progress,
                flow.email,
            )
        except FlowNotFoundError as error:
            response = jsonify({"error": str(error)})
            response.status_code = 404
            return response
        return jsonify(
            {
                "status": values.get(flow.status, "not started"),
                "paymentProgress": values.get(payment_progress, "not started"),
                # This Flow never sets email, so get returns None.
                "email": values.get(flow.email),
            }
        )

    return blueprint
