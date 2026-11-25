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

import pytest
from dex import (
    AsyncClient,
    FlowConfig,
    FlowNotFoundError,
    FlowStatus,
    IdReusePolicy,
    StartFlowOptions,
    StepDurability,
    StopFlowOptions,
    StopType,
)

from dex_examples.app import ExampleApp
from dex_examples.patterns.child_flows_business_identity.order_fulfillment_flow import (
    SHIPMENT_OUTCOME,
    SHIPMENT_RECORDS,
    SHIPMENT_REJECTED,
    SHIPMENT_TIMEOUT,
    Order,
    Shipment,
    ShipmentRecord,
    ShipmentReport,
    ShipmentRequest,
    shipment_start_request_id,
)
from tests.integ.conftest import WAIT_TIMEOUT, wait_for_flow_status, wait_until
from tests.integ.test_workflow_patterns import retry_on_lock_conflict


async def test_child_flows_business_identity_records_the_report_before_the_shipment_closes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    order_id = new_flow_id("order")
    delivered = f"{order_id}-1"
    await _start_order(
        app,
        client,
        order_id,
        Order(shipments=[Shipment(delivered, "Seattle", 1)], reconcile_interval_seconds=3600),
    )
    # The reconcile Timer waits an hour, so only the report can fill the outcome.
    await _wait_for_shipment_record(
        client, order_id, delivered, ShipmentRecord(outcome="delivered to Seattle")
    )
    await wait_for_flow_status(client, order_id, FlowStatus.RUNNING)
    await _terminate_flows(client, order_id)


async def test_child_flows_business_identity_merges_reports_and_reconcile_reads(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    order_id = new_flow_id("order")
    delivered = f"{order_id}-1"
    canceled = f"{order_id}-2"
    await _start_order(
        app,
        client,
        order_id,
        Order(
            shipments=[
                Shipment(delivered, "Seattle", 1),
                Shipment(canceled, "Portland", 3600),
            ],
            reconcile_interval_seconds=2,
        ),
    )
    expected = ShipmentRecord(status="completed", outcome="delivered to Seattle")
    await _wait_for_shipment_record(client, order_id, delivered, expected)

    # A repeated or different report for a recorded shipment changes nothing.
    for outcome in ("delivered to Seattle", "delivered to Boise"):
        await _report_shipment_outcome(
            app, client, order_id, ShipmentReport(shipment_number=delivered, outcome=outcome)
        )
    assert await _read_shipment_record(client, order_id, delivered) == expected

    await wait_for_flow_status(client, canceled, FlowStatus.RUNNING)
    await client.stop_flow(
        canceled,
        StopFlowOptions(type=StopType.CANCEL, reason="customer canceled the parcel"),
    )
    result = await client.wait_for_flow(order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED, result.error_message
    assert await _read_shipment_record(client, order_id, canceled) == ShipmentRecord(
        status="canceled"
    )


async def test_child_flows_business_identity_rejects_a_shipment_number_owned_by_another_request(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    first_order_id = new_flow_id("order")
    shipment = Shipment(f"{first_order_id}-1", "Boise", 3600)
    order = Order(shipments=[shipment], reconcile_interval_seconds=3600)
    await _start_order(app, client, first_order_id, order)
    await wait_for_flow_status(client, shipment.shipment_number, FlowStatus.RUNNING)

    # Replaying the same start request returns the running shipment.
    run_id = await client.start_flow(
        app.shipment,
        shipment.shipment_number,
        ShipmentRequest(order_flow_id=first_order_id, shipment=shipment),
        StartFlowOptions(
            timeout=SHIPMENT_TIMEOUT,
            id_reuse_policy=IdReusePolicy.DISALLOW,
            request_id=shipment_start_request_id(first_order_id, shipment),
            ignore_already_started=True,
        ),
    )
    info = await client.describe_flow(shipment.shipment_number)
    assert info.run_id == run_id

    # Another order with the same shipment number sends a different request.
    second_order_id = new_flow_id("order")
    await _start_order(app, client, second_order_id, order)
    result = await client.wait_for_flow(second_order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED, result.error_message
    assert await _read_shipment_record(
        client, second_order_id, shipment.shipment_number
    ) == ShipmentRecord(status=SHIPMENT_REJECTED)
    await _terminate_flows(client, shipment.shipment_number, first_order_id)


async def test_child_flows_business_identity_fails_an_order_with_a_duplicate_shipment_number(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    order_id = new_flow_id("order")
    number = f"{order_id}-1"
    await _start_order(
        app,
        client,
        order_id,
        Order(
            shipments=[Shipment(number, "Seattle", 1), Shipment(number, "Portland", 1)],
            reconcile_interval_seconds=2,
        ),
    )
    result = await client.wait_for_flow(order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.FAILED
    assert f"duplicate shipment number: {number}" in (result.error_message or "")
    with pytest.raises(FlowNotFoundError):
        await client.describe_flow(number)


async def test_child_flows_business_identity_stores_the_outcome_before_reporting_under_async_defaults(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    order_id = new_flow_id("order")
    await _start_order(
        app,
        client,
        order_id,
        Order(
            shipments=[Shipment(f"{order_id}-1", "Boise", 3600)],
            reconcile_interval_seconds=3600,
        ),
    )
    number = f"{order_id}-async"
    await client.start_flow(
        app.shipment,
        number,
        ShipmentRequest(order_flow_id=order_id, shipment=Shipment(number, "Austin", 1)),
        StartFlowOptions(
            config_override=FlowConfig(step_durability=StepDurability.ASYNC)
        ),
    )
    await _wait_for_shipment_record(
        client, order_id, number, ShipmentRecord(outcome="delivered to Austin")
    )
    values = await client.get_attributes(number, SHIPMENT_OUTCOME)
    assert values.get(SHIPMENT_OUTCOME) == "delivered to Austin"
    await _terminate_flows(client, f"{order_id}-1", order_id)


async def _start_order(
    app: ExampleApp, client: AsyncClient, order_id: str, order: Order
) -> None:
    await client.start_flow(app.order_fulfillment, order_id, order, StartFlowOptions())


async def _report_shipment_outcome(
    app: ExampleApp, client: AsyncClient, order_id: str, report: ShipmentReport
) -> None:
    async def invoke() -> None:
        await client.invoke_rpc(app.order_fulfillment.record_shipment_outcome, order_id, report)

    await retry_on_lock_conflict(invoke)


async def _wait_for_shipment_record(
    client: AsyncClient,
    order_id: str,
    shipment_number: str,
    expected: ShipmentRecord,
) -> None:
    instance = SHIPMENT_RECORDS.instance(shipment_number)

    async def has_expected_record() -> bool:
        values = await client.get_attributes(order_id, instance)
        return values.get(instance) == expected

    await wait_until(f"record of {shipment_number}", has_expected_record)


async def _read_shipment_record(
    client: AsyncClient, order_id: str, shipment_number: str
) -> ShipmentRecord:
    instance = SHIPMENT_RECORDS.instance(shipment_number)
    values = await client.get_attributes(order_id, instance)
    assert instance in values, f"record of {shipment_number}"
    return values[instance]


async def _terminate_flows(client: AsyncClient, *flow_ids: str) -> None:
    for flow_id in flow_ids:
        await client.stop_flow(flow_id, StopFlowOptions(type=StopType.TERMINATE))
