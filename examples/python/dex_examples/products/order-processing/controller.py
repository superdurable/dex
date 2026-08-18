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

from datetime import timedelta

from dex import StepExecutionId
from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import (
    new_flow_id,
    optional_query,
    required_query,
    started_flow,
)
from dex_examples.products.order_processing.order_request import OrderRequest

CHARGE_STEP = StepExecutionId("ChargeStep")
CHARGE_WAIT_TIMEOUT = timedelta(minutes=5)


def create_order_processing_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "order_processing",
        __name__,
        url_prefix="/products/order-processing",
    )

    @blueprint.get("/start")
    async def start() -> Response:
        flow_id = new_flow_id("order-processing")
        request = OrderRequest(
            flow_id,
            "buyer@example.com",
            "customer-1",
            42,
            optional_query("testFailAtShipping", "") == "true",
        )
        run_id = await app_state.client.start_flow(
            app_state.order_processing,
            flow_id,
            request,
            start_options(),
        )
        await app_state.client.wait_for_step_completion(
            flow_id,
            CHARGE_STEP,
            CHARGE_WAIT_TIMEOUT,
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/approve")
    async def approve() -> Response:
        output = await app_state.client.invoke_rpc(
            app_state.order_processing.approve,
            required_query("workflowId"),
            optional_query("notes", ""),
        )
        return jsonify(output)

    @blueprint.get("/describe")
    async def describe() -> Response:
        flow_id = required_query("workflowId")
        status = await app_state.client.invoke_rpc(
            app_state.order_processing.describe,
            flow_id,
        )
        return jsonify({"flowID": flow_id, "status": status})

    return blueprint
