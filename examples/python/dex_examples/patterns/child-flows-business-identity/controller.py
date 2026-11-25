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

from dex import FlowNotFoundError, StartFlowOptions, StopFlowOptions, StopType
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.patterns.child_flows_business_identity.order_fulfillment_flow import (
    Order,
)
from dex_examples.patterns.child_flows_business_identity.shipment_flow import (
    SHIPMENT_OUTCOME,
    Shipment,
)
from dex_examples.shared.query import required_query

ORDER_TIMEOUT = timedelta(days=40)


def create_child_flows_business_identity_blueprint(app_state: ExampleApp) -> Blueprint:
    """Serves the order routes and the shipment routes that find a shipment by its number."""
    blueprint = Blueprint(
        "pattern_child_flows_business_identity",
        __name__,
        url_prefix="/patterns/child-flows-business-identity",
    )

    @blueprint.get("/start")
    async def start_order() -> str:
        """Starts an order with two shipments: one delivers in seconds, one takes a day."""
        order_id = required_query("workflowId")
        return await app_state.client.start_flow(
            app_state.order_fulfillment,
            order_id,
            Order(
                shipments=[
                    Shipment(f"{order_id}-1", "Seattle", 5),
                    Shipment(f"{order_id}-2", "Portland", 86400),
                ],
                reconcile_interval_seconds=60,
            ),
            StartFlowOptions(timeout=ORDER_TIMEOUT),
        )

    @blueprint.get("/shipment")
    async def get_shipment() -> tuple[dict[str, str], int]:
        """Looks a shipment up by its shipment number, which is its Flow ID."""
        number = required_query("shipmentNumber")
        try:
            info = await app_state.client.describe_flow(number)
        except FlowNotFoundError:
            return {"error": f"shipment {number} not found"}, 404
        values = await app_state.client.get_attributes(number, SHIPMENT_OUTCOME)
        return {
            "shipmentNumber": number,
            "status": info.status.value,
            "outcome": values.get(SHIPMENT_OUTCOME, ""),
        }, 200

    @blueprint.get("/cancel-shipment")
    async def cancel_shipment() -> str:
        """Stops a shipment by its number. Its order records it at the next reconcile read."""
        await app_state.client.stop_flow(
            required_query("shipmentNumber"),
            StopFlowOptions(type=StopType.CANCEL, reason="shipment canceled"),
        )
        return "canceled"

    return blueprint
