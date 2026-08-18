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

from dataclasses import asdict

from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import (
    accepted,
    new_flow_id,
    required_int_query,
    required_query,
    started_flow,
)
from dex_examples.products.subscription.customer import Customer
from dex_examples.products.subscription.subscription import Subscription


def create_subscription_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("subscription", __name__, url_prefix="/products/subscription")

    @blueprint.get("/start")
    async def start() -> Response:
        customer = Customer(
            "Quanzheng",
            "Long",
            "qlong",
            "qlong@example.com",
            Subscription(
                trial_period_seconds=20,
                billing_period_seconds=10,
                max_billing_periods=10,
                billing_period_charge=100,
            ),
        )
        flow_id = new_flow_id("subscription")
        run_id = await app_state.client.start_flow(
            app_state.subscription,
            flow_id,
            customer,
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/cancel")
    async def cancel() -> Response:
        await app_state.client.publish(
            required_query("workflowId"),
            app_state.subscription.cancel_subscription,
            None,
        )
        return accepted()

    @blueprint.get("/updateChargeAmount")
    async def update_charge_amount() -> Response:
        await app_state.client.publish(
            required_query("workflowId"),
            app_state.subscription.update_charge_amount,
            required_int_query("newChargeAmount"),
        )
        return accepted()

    @blueprint.get("/describe")
    async def describe() -> Response:
        subscription = await app_state.client.invoke_rpc(
            app_state.subscription.describe,
            required_query("workflowId"),
        )
        return jsonify(asdict(subscription))

    return blueprint
