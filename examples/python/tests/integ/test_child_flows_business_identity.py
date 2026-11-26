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
    PENDING_SHIPMENT_REPORTS,
    SHIPMENT_OUTCOME,
    SHIPMENT_RECORDS,
    SHIPMENT_REJECTED,
    SHIPMENT_STARTED,
    SHIPMENT_STARTING,
    SHIPMENT_TIMEOUT,
    SHIPMENT_UNAVAILABLE,
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
    run_id = await _wait_for_claimed_run(client, order_id, delivered)
    # The reconcile Timer waits an hour, so only the report can fill the outcome.
    await _wait_for_shipment_record(
        client,
        order_id,
        delivered,
        ShipmentRecord(status=SHIPMENT_STARTED, run_id=run_id, outcome="delivered to Seattle"),
    )
    await wait_for_flow_status(client, order_id, FlowStatus.RUNNING)
    await _terminate_flows(client, order_id)


async def test_child_flows_business_identity_accepts_reports_only_from_the_claimed_run(
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
    delivered_run_id = await _wait_for_claimed_run(client, order_id, delivered)
    canceled_run_id = await _wait_for_claimed_run(client, order_id, canceled)
    expected_delivered = ShipmentRecord(
        status="completed", run_id=delivered_run_id, outcome="delivered to Seattle"
    )
    await _wait_for_shipment_record(client, order_id, delivered, expected_delivered)

    # A repeated report, a report from another run, and an unknown number change nothing.
    for report in (
        ShipmentReport(delivered, delivered_run_id, "delivered to Boise"),
        ShipmentReport(canceled, "another-run", "delivered to Boise"),
        ShipmentReport(canceled, "", "delivered to Boise"),
        ShipmentReport(f"{order_id}-9", "another-run", "delivered to Boise"),
    ):
        await _report_shipment_outcome(app, client, order_id, report)
    assert await _read_shipment_record(client, order_id, delivered) == expected_delivered
    assert await _read_shipment_record(client, order_id, canceled) == ShipmentRecord(
        status=SHIPMENT_STARTED, run_id=canceled_run_id
    )
    await _require_no_shipment_record(client, order_id, f"{order_id}-9")

    await client.stop_flow(
        canceled,
        StopFlowOptions(type=StopType.CANCEL, reason="customer canceled the parcel"),
    )
    result = await client.wait_for_flow(order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED, result.error_message
    assert await _read_shipment_record(client, order_id, canceled) == ShipmentRecord(
        status="canceled", run_id=canceled_run_id
    )


async def test_child_flows_business_identity_rejects_a_shipment_number_owned_by_another_request(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    first_order_id = new_flow_id("order")
    shipment = Shipment(f"{first_order_id}-1", "Boise", 3600)
    await _start_order(
        app,
        client,
        first_order_id,
        Order(shipments=[shipment], reconcile_interval_seconds=3600),
    )
    run_id = await _wait_for_claimed_run(client, first_order_id, shipment.shipment_number)

    # Replaying the same start request returns the claimed run.
    replayed_run_id = await client.start_flow(
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
    assert replayed_run_id == run_id

    # Another order with the same shipment number sends a different request.
    second_order_id = new_flow_id("order")
    await _start_order(
        app,
        client,
        second_order_id,
        Order(
            shipments=[shipment, Shipment(f"{second_order_id}-2", "Austin", 3600)],
            reconcile_interval_seconds=3600,
        ),
    )
    rejected = ShipmentRecord(status=SHIPMENT_REJECTED)
    await _wait_for_shipment_record(client, second_order_id, shipment.shipment_number, rejected)
    # Reports to the second order, from the owning run or with no run ID, leave the rejected record as it is.
    for report_run_id in (run_id, ""):
        await _report_shipment_outcome(
            app,
            client,
            second_order_id,
            ShipmentReport(shipment.shipment_number, report_run_id, "delivered to Boise"),
        )
    assert (
        await _read_shipment_record(client, second_order_id, shipment.shipment_number)
        == rejected
    )
    await _terminate_flows(
        client,
        shipment.shipment_number,
        first_order_id,
        f"{second_order_id}-2",
        second_order_id,
    )


async def test_child_flows_business_identity_ignores_a_reopened_shipment_number(
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
        Order(shipments=[Shipment(number, "Seattle", 3600)], reconcile_interval_seconds=10),
    )
    claimed_run_id = await _wait_for_claimed_run(client, order_id, number)
    await client.stop_flow(number, StopFlowOptions(type=StopType.CANCEL))
    await wait_for_flow_status(client, number, FlowStatus.CANCELED)

    # Another caller reopens the number; its run reports to the same order and completes.
    await client.start_flow(
        app.shipment,
        number,
        ShipmentRequest(order_flow_id=order_id, shipment=Shipment(number, "Portland", 1)),
        StartFlowOptions(id_reuse_policy=IdReusePolicy.ALLOW_IF_NOT_RUNNING),
    )
    await wait_for_flow_status(client, number, FlowStatus.COMPLETED)

    # The order has not read the shipment yet, and it ignored the other run's report.
    order = await client.describe_flow(order_id)
    assert order.status == FlowStatus.RUNNING
    assert await _read_shipment_record(client, order_id, number) == ShipmentRecord(
        status=SHIPMENT_STARTED, run_id=claimed_run_id
    )

    result = await client.wait_for_flow(order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED, result.error_message
    assert await _read_shipment_record(client, order_id, number) == ShipmentRecord(
        status=SHIPMENT_UNAVAILABLE, run_id=claimed_run_id
    )


@pytest.mark.parametrize(
    ("build_order", "message"),
    [
        pytest.param(
            lambda number: Order(
                shipments=[Shipment(number, "Seattle", 1), Shipment(number, "Portland", 1)],
                reconcile_interval_seconds=2,
            ),
            "duplicate shipment number: ",
            id="duplicate shipment number",
        ),
        pytest.param(
            lambda number: Order(
                shipments=[Shipment(number, "Seattle", -1)], reconcile_interval_seconds=2
            ),
            "negative delivery delay for shipment: ",
            id="negative delivery delay",
        ),
        pytest.param(
            lambda number: Order(
                shipments=[Shipment(number, "Seattle", 1)], reconcile_interval_seconds=0
            ),
            "reconcile interval must be positive",
            id="zero reconcile interval",
        ),
    ],
)
async def test_child_flows_business_identity_fails_an_invalid_order(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    build_order: Callable[[str], Order],
    message: str,
) -> None:
    order_id = new_flow_id("order")
    number = f"{order_id}-1"
    await _start_order(app, client, order_id, build_order(number))
    result = await client.wait_for_flow(order_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.FAILED
    assert message in (result.error_message or "")
    await _require_no_shipment_record(client, order_id, number)
    with pytest.raises(FlowNotFoundError):
        await client.describe_flow(number)


# The explicit SYNC options override the Flow's ASYNC default, so the outcome stays readable once
# the order recorded it. This does not prove the commit order of the two Steps.
async def test_child_flows_business_identity_keeps_the_outcome_readable_when_sync_overrides_an_async_default(
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
        Order(shipments=[Shipment(number, "Austin", 3)], reconcile_interval_seconds=3600),
    )
    run_id = await _wait_for_claimed_run(client, order_id, number)
    await client.update_flow_config(number, FlowConfig(step_durability=StepDurability.ASYNC))
    await _wait_for_shipment_record(
        client,
        order_id,
        number,
        ShipmentRecord(status=SHIPMENT_STARTED, run_id=run_id, outcome="delivered to Austin"),
    )
    values = await client.get_attributes(number, SHIPMENT_OUTCOME)
    assert values.get(SHIPMENT_OUTCOME) == "delivered to Austin"
    await _terminate_flows(client, order_id)


async def test_child_flows_business_identity_merges_reports_that_arrive_before_the_claim(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    order_id = new_flow_id("order")
    claimed = Shipment(f"{order_id}-1", "Seattle", 3600)
    other = Shipment(f"{order_id}-2", "Boise", 3600)
    owned = Shipment(f"{order_id}-3", "Austin", 3600)
    # Withholding the Worker's Client keeps every StartShipment retrying, so the
    # reports below arrive deterministically before any claim.
    monkeypatch.setattr(app, "_client", None)
    try:
        await _start_order(
            app,
            client,
            order_id,
            Order(shipments=[claimed, other, owned], reconcile_interval_seconds=3600),
        )
        for shipment in (claimed, other, owned):
            await _wait_for_shipment_record(
                client, order_id, shipment.shipment_number, ShipmentRecord(status=SHIPMENT_STARTING)
            )

        # Start the first shipment with the order's own request, so the order's retry gets this run.
        claimed_run_id = await client.start_flow(
            app.shipment,
            claimed.shipment_number,
            ShipmentRequest(order_flow_id=order_id, shipment=claimed),
            StartFlowOptions(
                timeout=SHIPMENT_TIMEOUT,
                id_reuse_policy=IdReusePolicy.DISALLOW,
                request_id=shipment_start_request_id(order_id, claimed),
                ignore_already_started=True,
            ),
        )
        # Another request owns the third shipment number, so the order rejects it.
        await client.start_flow(
            app.shipment,
            owned.shipment_number,
            ShipmentRequest(order_flow_id="another-order", shipment=owned),
            StartFlowOptions(id_reuse_policy=IdReusePolicy.DISALLOW),
        )

        # A report under the wrong number arrives first; it must not hide the claimed run's report.
        for report in (
            ShipmentReport(other.shipment_number, claimed_run_id, "delivered to Boise"),
            ShipmentReport(claimed.shipment_number, claimed_run_id, "delivered to Seattle"),
            ShipmentReport(claimed.shipment_number, claimed_run_id, "delivered to Portland"),
            ShipmentReport(owned.shipment_number, "another-run", "delivered to Austin"),
        ):
            await _report_shipment_outcome(app, client, order_id, report)
        assert await _read_pending_shipment_reports(
            client, order_id, claimed.shipment_number
        ) == {
            claimed_run_id: ShipmentReport(
                claimed.shipment_number, claimed_run_id, "delivered to Seattle"
            )
        }
    finally:
        monkeypatch.undo()

    await _wait_for_shipment_record(
        client,
        order_id,
        claimed.shipment_number,
        ShipmentRecord(status=SHIPMENT_STARTED, run_id=claimed_run_id, outcome="delivered to Seattle"),
    )
    await _wait_for_shipment_record(
        client, order_id, owned.shipment_number, ShipmentRecord(status=SHIPMENT_REJECTED)
    )
    other_run_id = await _wait_for_claimed_run(client, order_id, other.shipment_number)
    assert await _read_shipment_record(client, order_id, other.shipment_number) == ShipmentRecord(
        status=SHIPMENT_STARTED, run_id=other_run_id
    )
    # The claim and the rejection leave no early report behind.
    for shipment in (claimed, other, owned):
        instance = PENDING_SHIPMENT_REPORTS.instance(shipment.shipment_number)
        values = await client.get_attributes(order_id, instance)
        assert instance not in values, f"pending reports of {shipment.shipment_number}"
    await _terminate_flows(
        client, claimed.shipment_number, other.shipment_number, owned.shipment_number, order_id
    )


async def _read_pending_shipment_reports(
    client: AsyncClient, order_id: str, shipment_number: str
) -> dict[str, ShipmentReport]:
    instance = PENDING_SHIPMENT_REPORTS.instance(shipment_number)
    values = await client.get_attributes(order_id, instance)
    assert instance in values, f"pending reports of {shipment_number}"
    pending: dict[str, ShipmentReport] = values[instance]
    return pending


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


async def _wait_for_claimed_run(client: AsyncClient, order_id: str, shipment_number: str) -> str:
    """Waits until the order claimed a run of the shipment and returns its run ID."""
    instance = SHIPMENT_RECORDS.instance(shipment_number)

    async def claimed_run_id() -> str:
        values = await client.get_attributes(order_id, instance)
        record = values.get(instance)
        return record.run_id if record is not None else ""

    run_id: str = await wait_until(f"claimed run of {shipment_number}", claimed_run_id)
    return run_id


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


async def _require_no_shipment_record(
    client: AsyncClient, order_id: str, shipment_number: str
) -> None:
    instance = SHIPMENT_RECORDS.instance(shipment_number)
    values = await client.get_attributes(order_id, instance)
    assert instance not in values, f"record of {shipment_number}"


async def _terminate_flows(client: AsyncClient, *flow_ids: str) -> None:
    for flow_id in flow_ids:
        await client.stop_flow(flow_id, StopFlowOptions(type=StopType.TERMINATE))
