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

from typing import Callable

from dex import AsyncClient, FlowStatus, StartFlowOptions, StopFlowOptions, StopType

from dex_examples.app import ExampleApp
from dex_examples.patterns.child_flows_business_identity.order_fulfillment_flow import (
    SHIPMENT_RECORDS,
    SHIPMENT_REJECTED,
    Order,
    ShipmentRecord,
)
from dex_examples.patterns.child_flows_business_identity.shipment_flow import (
    SHIPMENT_OUTCOME,
    Shipment,
)
from tests.integ.conftest import WAIT_TIMEOUT, wait_for_flow_status


async def test_child_flows_business_identity_records_reported_and_canceled_shipments(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    order_id = new_flow_id("order")
    delivered = f"{order_id}-1"
    canceled = f"{order_id}-2"
    await client.start_flow(
        app.order_fulfillment,
        order_id,
        Order(
            shipments=[
                Shipment(delivered, "Seattle", 1),
                Shipment(canceled, "Portland", 3600),
            ],
            reconcile_interval_seconds=2,
        ),
        StartFlowOptions(),
    )
    await wait_for_flow_status(client, canceled, FlowStatus.RUNNING)
    await client.stop_flow(
        canceled,
        StopFlowOptions(type=StopType.CANCEL, reason="customer canceled the parcel"),
    )

    result = await client.wait_for_flow(order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED, result.error_message
    delivered_record = await _read_shipment_record(client, order_id, delivered)
    assert delivered_record.outcome == "delivered to Seattle"
    assert await _read_shipment_record(client, order_id, canceled) == ShipmentRecord(
        status="canceled"
    )

    await wait_for_flow_status(client, delivered, FlowStatus.COMPLETED)
    values = await client.get_attributes(delivered, SHIPMENT_OUTCOME)
    assert values.get(SHIPMENT_OUTCOME) == "delivered to Seattle"


async def test_child_flows_business_identity_rejects_a_shipment_number_owned_by_another_order(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    first_order_id = new_flow_id("order")
    shared = f"{first_order_id}-1"
    order = Order(
        shipments=[Shipment(shared, "Boise", 3600)],
        reconcile_interval_seconds=3600,
    )
    await client.start_flow(app.order_fulfillment, first_order_id, order, StartFlowOptions())
    await wait_for_flow_status(client, shared, FlowStatus.RUNNING)

    second_order_id = new_flow_id("order")
    await client.start_flow(app.order_fulfillment, second_order_id, order, StartFlowOptions())
    result = await client.wait_for_flow(second_order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED, result.error_message
    assert await _read_shipment_record(client, second_order_id, shared) == ShipmentRecord(
        status=SHIPMENT_REJECTED
    )

    for flow_id in (shared, first_order_id):
        await client.stop_flow(flow_id, StopFlowOptions(type=StopType.TERMINATE))


async def _read_shipment_record(
    client: AsyncClient, order_id: str, shipment_number: str
) -> ShipmentRecord:
    instance = SHIPMENT_RECORDS.instance(shipment_number)
    values = await client.get_attributes(order_id, instance)
    assert instance in values, f"record of {shipment_number}"
    return values[instance]
