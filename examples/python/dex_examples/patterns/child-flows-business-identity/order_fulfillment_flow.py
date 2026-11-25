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

from dataclasses import dataclass, field
from datetime import timedelta
from typing import Callable

from dex import (
    AsyncClient,
    AsyncContext,
    AttributeMap,
    ChannelMap,
    Context,
    Flow,
    FlowAlreadyStartedError,
    FlowNotFoundError,
    FlowStatus,
    IdReusePolicy,
    PersistenceSchema,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.child_flows_business_identity.shipment_flow import (
    SHIPMENT_OUTCOME,
    Shipment,
    ShipmentFlow,
    ShipmentReport,
    ShipmentRequest,
)

# Each shipment's own Flow timeout. A closing order never stops its shipments.
SHIPMENT_TIMEOUT = timedelta(days=30)

# Shipment record statuses that are not a terminal Flow status.
SHIPMENT_REJECTED = "rejected"
SHIPMENT_UNAVAILABLE = "unavailable"


@dataclass(frozen=True)
class Order:
    """The input of OrderFulfillmentFlow. The order number is the Flow ID."""

    shipments: list[Shipment]
    # How long the order waits for a report before it reads the shipment.
    reconcile_interval_seconds: int = field(
        metadata={"dex_json_name": "reconcileIntervalSeconds"}
    )


@dataclass(frozen=True)
class TrackedShipment:
    """The input of the Steps that start and track one shipment."""

    shipment: Shipment
    reconcile_interval_seconds: int = field(
        metadata={"dex_json_name": "reconcileIntervalSeconds"}
    )


@dataclass(frozen=True)
class ShipmentRecord:
    """The order's record of one shipment.

    A report fills ``outcome``; a reconcile read fills ``status`` and, when the
    shipment stored one, ``outcome``.
    """

    status: str = field(default="", metadata={"dex_json_omit_default": True})
    outcome: str = field(default="", metadata={"dex_json_omit_default": True})


# One record per shipment, keyed by shipment number.
SHIPMENT_RECORDS = AttributeMap("shipmentRecords", ShipmentRecord)
# Each shipment's report, keyed by shipment number.
SHIPMENT_REPORTS = ChannelMap("shipmentReports", str)


# dex:group group-id:start group-label:"Start shipments"
# dex:explanation text:"Starts one branch per shipment in the order."
class StartShipments(Step[Order]):
    """Starts one StartShipment branch per shipment."""

    def execute(self, context: Context, order: Order) -> StepDecision:
        if not order.shipments:
            return graceful_complete()
        return go_to_many(
            *(
                StepMovement.of(
                    StartShipment,
                    TrackedShipment(
                        shipment=shipment,
                        reconcile_interval_seconds=order.reconcile_interval_seconds,
                    ),
                )
                for shipment in order.shipments
            )
        )


# dex:group group-id:start group-label:"Start shipments"
# dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number."
class StartShipment(Step[TrackedShipment]):
    """Starts ShipmentFlow with the shipment number as its Flow ID."""

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        shipment_flow: ShipmentFlow,
    ) -> None:
        self.client_provider = client_provider
        self.shipment_flow = shipment_flow

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, tracked: TrackedShipment
    ) -> StepDecision:
        number = tracked.shipment.shipment_number
        try:
            await self.client_provider().start_flow(
                self.shipment_flow,
                number,
                ShipmentRequest(order_flow_id=context.flow_id, shipment=tracked.shipment),
                StartFlowOptions(
                    timeout=SHIPMENT_TIMEOUT,
                    id_reuse_policy=IdReusePolicy.DISALLOW,
                    # Derived from this order and shipment, so a retried Execute repeats the same start.
                    request_id=f"start:{context.flow_id}:{number}",
                    ignore_already_started=True,
                ),
            )
        except FlowAlreadyStartedError:
            # Another request owns this shipment number; this order does not track it.
            SHIPMENT_RECORDS.set(context, number, ShipmentRecord(status=SHIPMENT_REJECTED))
            return graceful_complete()
        return go_to(AwaitShipment, tracked)


# dex:group group-id:track group-label:"Track shipment"
# dex:explanation text:"Records the shipment's report, or reads the shipment when no report arrives in time."
class AwaitShipment(Step[TrackedShipment]):
    """Waits for the shipment's report, or reconciles after the interval."""

    def __init__(self, client_provider: Callable[[], AsyncClient]) -> None:
        self.client_provider = client_provider

    def wait_for(self, context: Context, tracked: TrackedShipment) -> Wait:
        return Wait.any_of(
            SHIPMENT_REPORTS.for_one(tracked.shipment.shipment_number),
            Timer.by_duration(timedelta(seconds=tracked.reconcile_interval_seconds)),
        )

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, tracked: TrackedShipment
    ) -> StepDecision:
        number = tracked.shipment.shipment_number
        outcomes = SHIPMENT_REPORTS.results(context, number)
        if outcomes:
            record = ShipmentRecord(outcome=outcomes[0])
        else:
            record = await reconcile_shipment(self.client_provider(), number)
            if record.status == FlowStatus.RUNNING.value:
                return go_to(AwaitShipment, tracked)
        SHIPMENT_RECORDS.set(context, number, record)
        return graceful_complete()


async def reconcile_shipment(client: AsyncClient, number: str) -> ShipmentRecord:
    """Reads a shipment that has not reported: its status first, then its outcome.

    Returns a "running" record while the shipment runs and an "unavailable"
    record when the shipment does not exist. Any other failure raises, so the
    Step retries.
    """
    try:
        info = await client.describe_flow(number)
    except FlowNotFoundError:
        return ShipmentRecord(status=SHIPMENT_UNAVAILABLE)
    if info.status is FlowStatus.RUNNING:
        return ShipmentRecord(status=info.status.value)
    try:
        values = await client.get_attributes(number, SHIPMENT_OUTCOME)
    except FlowNotFoundError:
        return ShipmentRecord(status=info.status.value)
    # An unset outcome Attribute leaves the outcome empty: the shipment ended without one.
    return ShipmentRecord(
        status=info.status.value, outcome=values.get(SHIPMENT_OUTCOME, "")
    )


class OrderFulfillmentFlow(Flow[Order]):
    """Ships an order as one ShipmentFlow per shipment.

    Each shipment is a top-level Flow whose Flow ID is its shipment number.
    """

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        shipment_flow: ShipmentFlow,
    ) -> None:
        self.start_shipments = StartShipments()
        self.start_shipment = StartShipment(client_provider, shipment_flow)
        self.await_shipment = AwaitShipment(client_provider)

    def get_steps(self) -> StepList[Order]:
        return StepList.start_step(self.start_shipments).other_steps(
            self.start_shipment, self.await_shipment
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(SHIPMENT_RECORDS, SHIPMENT_REPORTS)

    @rpc
    def record_shipment_outcome(self, context: Context, report: ShipmentReport) -> None:
        """Receives a shipment's report and publishes its outcome."""
        SHIPMENT_REPORTS.publish(context, report.shipment_number, report.outcome)
