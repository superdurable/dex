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

import dataclasses
import hashlib
import json
from dataclasses import dataclass, field
from datetime import timedelta
from typing import Callable

from dex import (
    AsyncClient,
    AsyncContext,
    Attribute,
    AttributeMap,
    Context,
    Flow,
    FlowAlreadyStartedError,
    FlowInfo,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    FlowStatus,
    IdReusePolicy,
    PersistenceSchema,
    StartFlowOptions,
    Step,
    StepDecision,
    StepDurability,
    StepList,
    StepMovement,
    StepOptions,
    Timer,
    Wait,
    force_fail,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

# The registered Flow type of ShipmentFlow.
SHIPMENT_FLOW_TYPE = "ShipmentFlow"

# Each shipment's own Flow timeout. A closing order never stops its shipments.
SHIPMENT_TIMEOUT = timedelta(days=30)

# Shipment record statuses that are not a terminal Flow status.
# A shipment of the order that has no claimed run yet.
SHIPMENT_STARTING = "starting"
# A shipment whose run this order started and claimed.
SHIPMENT_STARTED = "started"
# A shipment number that another request owns.
SHIPMENT_REJECTED = "rejected"
# A claimed run that can no longer be read.
SHIPMENT_UNAVAILABLE = "unavailable"

DUPLICATE_SHIPMENT_PREFIX = "duplicate shipment number: "


class ShipmentNotClaimedError(Exception):
    """Asks a shipment to retry its report after the order claims its run."""

    def __init__(self) -> None:
        super().__init__("childflows: the order has not claimed this shipment run yet")


@dataclass(frozen=True)
class Shipment:
    """One parcel of an order, identified by its shipment number."""

    shipment_number: str = field(metadata={"dex_json_name": "shipmentNumber"})
    destination: str
    # Simulates how long the carrier takes to deliver.
    delivery_delay_seconds: int = field(metadata={"dex_json_name": "deliveryDelaySeconds"})


@dataclass(frozen=True)
class ShipmentRequest:
    """The input of ShipmentFlow."""

    order_flow_id: str = field(metadata={"dex_json_name": "orderFlowId"})
    shipment: Shipment


@dataclass(frozen=True)
class ShipmentReport:
    """What a shipment run sends to its order when it ends."""

    shipment_number: str = field(metadata={"dex_json_name": "shipmentNumber"})
    shipment_run_id: str = field(metadata={"dex_json_name": "shipmentRunId"})
    outcome: str


@dataclass(frozen=True)
class PendingReport:
    """The report a shipment still has to send to its order."""

    order_flow_id: str = field(metadata={"dex_json_name": "orderFlowId"})
    report: ShipmentReport


@dataclass(frozen=True)
class Order:
    """The input of OrderFulfillmentFlow. The order number is the Flow ID."""

    shipments: list[Shipment]
    # How often the order reads a shipment that has not ended.
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
    # The run this order started and tracks; empty before the start.
    shipment_run_id: str = field(
        default="",
        metadata={"dex_json_name": "shipmentRunId", "dex_json_omit_default": True},
    )


@dataclass(frozen=True)
class ShipmentRecord:
    """The order's record of one shipment. ``run_id`` is the claimed run.

    A report or a reconcile read fills ``outcome``; a reconcile read fills the
    terminal ``status``.
    """

    status: str = field(default="", metadata={"dex_json_omit_default": True})
    run_id: str = field(
        default="", metadata={"dex_json_name": "runId", "dex_json_omit_default": True}
    )
    outcome: str = field(default="", metadata={"dex_json_omit_default": True})


# The shipment's committed outcome. The order reads it when no report arrives.
SHIPMENT_OUTCOME = Attribute("shipmentOutcome", str)

# One record per shipment, keyed by shipment number.
SHIPMENT_RECORDS = AttributeMap("shipmentRecords", ShipmentRecord)
# Serializes every write to the shipment records; it stores no value.
SHIPMENT_RECORDS_LOCK = Attribute("shipmentRecordsLock", bool)


# dex:group group-id:start group-label:"Start shipments"
# dex:explanation text:"Registers the order's shipment numbers, then starts one branch per shipment."
class StartShipments(Step[Order]):
    """Registers every shipment, then starts one StartShipment branch per shipment."""

    def get_step_options(self) -> StepOptions:
        """Takes the records lock, so registration never races a report."""
        return StepOptions(execute_lock_attributes=(SHIPMENT_RECORDS_LOCK.lock(),))

    def execute(self, context: Context, order: Order) -> StepDecision:
        if not order.shipments:
            return graceful_complete()
        numbers: set[str] = set()
        for shipment in order.shipments:
            if shipment.shipment_number in numbers:
                return force_fail(DUPLICATE_SHIPMENT_PREFIX + shipment.shipment_number)
            numbers.add(shipment.shipment_number)
        movements: list[StepMovement] = []
        for shipment in order.shipments:
            SHIPMENT_RECORDS.set(
                context, shipment.shipment_number, ShipmentRecord(status=SHIPMENT_STARTING)
            )
            movements.append(
                StepMovement.of(
                    StartShipment,
                    TrackedShipment(
                        shipment=shipment,
                        reconcile_interval_seconds=order.reconcile_interval_seconds,
                    ),
                )
            )
        return go_to_many(*movements)


def shipment_start_request_id(order_flow_id: str, shipment: Shipment) -> str:
    """Derives the start request ID from the complete start request.

    A retried Execute sends the same ID, so it repeats the same start. Any
    other request for the same shipment number sends another ID, so Dex
    rejects it as already started.

    Returns:
        ``start:`` followed by the hex SHA-256 digest of the encoded request.
    """
    request = json.dumps(
        [
            SHIPMENT_FLOW_TYPE,
            shipment.shipment_number,
            order_flow_id,
            shipment.destination,
            shipment.delivery_delay_seconds,
            int(SHIPMENT_TIMEOUT.total_seconds()),
            "disallow",
        ],
        separators=(",", ":"),
    )
    return "start:" + hashlib.sha256(request.encode("utf-8")).hexdigest()


# dex:group group-id:start group-label:"Start shipments"
# dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number and claims its run."
class StartShipment(Step[TrackedShipment]):
    """Starts ShipmentFlow with the shipment number as its Flow ID and claims its run."""

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        shipment_flow: ShipmentFlow,
    ) -> None:
        self.client_provider = client_provider
        self.shipment_flow = shipment_flow

    def get_step_options(self) -> StepOptions:
        """Takes the records lock, so the claim and the rejection never race a report."""
        return StepOptions(execute_lock_attributes=(SHIPMENT_RECORDS_LOCK.lock(),))

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, tracked: TrackedShipment
    ) -> StepDecision:
        number = tracked.shipment.shipment_number
        try:
            run_id = await self.client_provider().start_flow(
                self.shipment_flow,
                number,
                ShipmentRequest(order_flow_id=context.flow_id, shipment=tracked.shipment),
                StartFlowOptions(
                    timeout=SHIPMENT_TIMEOUT,
                    id_reuse_policy=IdReusePolicy.DISALLOW,
                    request_id=shipment_start_request_id(context.flow_id, tracked.shipment),
                    ignore_already_started=True,
                ),
            )
        except FlowAlreadyStartedError:
            # Another request owns this shipment number; this order does not track it.
            SHIPMENT_RECORDS.set(context, number, ShipmentRecord(status=SHIPMENT_REJECTED))
            return graceful_complete()
        # Claim the run: reports and reads of any other run of this number are ignored.
        SHIPMENT_RECORDS.set(
            context, number, ShipmentRecord(status=SHIPMENT_STARTED, run_id=run_id)
        )
        return go_to(AwaitShipment, dataclasses.replace(tracked, shipment_run_id=run_id))


# dex:group group-id:track group-label:"Track shipment"
# dex:explanation text:"Reads the claimed shipment run at each interval and records how it ended."
class AwaitShipment(Step[TrackedShipment]):
    """Reads the claimed shipment run at each interval until it ends, then records it."""

    def __init__(self, client_provider: Callable[[], AsyncClient]) -> None:
        self.client_provider = client_provider

    def get_step_options(self) -> StepOptions:
        """Takes the same lock as the report RPC and loads the records."""
        return StepOptions(
            execute_lock_attributes=(SHIPMENT_RECORDS_LOCK.lock(),),
            execute_load_attribute_maps=(SHIPMENT_RECORDS,),
        )

    def wait_for(self, context: Context, tracked: TrackedShipment) -> Wait:
        return Wait.until(
            Timer.by_duration(timedelta(seconds=tracked.reconcile_interval_seconds))
        )

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, tracked: TrackedShipment
    ) -> StepDecision:
        number = tracked.shipment.shipment_number
        reading = await reconcile_shipment(
            self.client_provider(), number, tracked.shipment_run_id
        )
        if reading.status == FlowStatus.RUNNING.value:
            return go_to(AwaitShipment, tracked)
        # Merge with the record, which may already hold the reported outcome.
        record, _ = load_shipment_record(context, number)
        SHIPMENT_RECORDS.set(
            context,
            number,
            ShipmentRecord(
                status=reading.status,
                run_id=tracked.shipment_run_id,
                outcome=record.outcome or reading.outcome,
            ),
        )
        return graceful_complete()


def load_shipment_record(context: Context, number: str) -> tuple[ShipmentRecord, bool]:
    """Returns the loaded record of a shipment and whether it exists.

    Returns an empty record and False when the shipment has no record.
    """
    # A missing instance reads as None.
    record: ShipmentRecord | None = SHIPMENT_RECORDS.get(context, number)
    if record is None:
        return ShipmentRecord(), False
    return record, True


async def reconcile_shipment(client: AsyncClient, number: str, run_id: str) -> ShipmentRecord:
    """Reads the claimed run of a shipment: its status first, then its outcome.

    Returns a "running" record while that run runs, and an "unavailable" record
    when that run no longer exists or another run replaced it. Keeps an outcome
    only when the run is still the claimed one after the read. Any other failure
    raises, so the Step retries.
    """
    info, found = await describe_claimed_run(client, number, run_id)
    if info is None or not found:
        return ShipmentRecord(status=SHIPMENT_UNAVAILABLE)
    if info.status is FlowStatus.RUNNING:
        return ShipmentRecord(status=info.status.value)
    try:
        values = await client.get_attributes(number, SHIPMENT_OUTCOME)
    except FlowNotFoundError:
        return ShipmentRecord(status=info.status.value)
    # The Attribute read targets the latest run, so confirm it is still the claimed one.
    _, still_claimed = await describe_claimed_run(client, number, run_id)
    if not still_claimed:
        return ShipmentRecord(status=info.status.value)
    # An unset outcome Attribute leaves the outcome empty: the shipment ended without one.
    return ShipmentRecord(
        status=info.status.value, outcome=values.get(SHIPMENT_OUTCOME, "")
    )


async def describe_claimed_run(
    client: AsyncClient, number: str, run_id: str
) -> tuple[FlowInfo | None, bool]:
    """Describes the latest run of a shipment.

    Returns None and False when the shipment does not exist, and the run's
    info with False when its latest run is not ``run_id``. Any other failure
    raises.
    """
    try:
        info = await client.describe_flow(number)
    except FlowNotFoundError:
        return None, False
    return info, info.run_id == run_id


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
        return PersistenceSchema.of(SHIPMENT_RECORDS, SHIPMENT_RECORDS_LOCK)

    @rpc(
        lock_attributes=(SHIPMENT_RECORDS_LOCK.lock(),),
        load_attribute_maps=(SHIPMENT_RECORDS,),
    )
    def record_shipment_outcome(self, context: Context, report: ShipmentReport) -> None:
        """Fills the outcome of a shipment's record.

        It accepts only the run this order claimed. The first report wins, and
        any other report changes nothing.

        Raises:
            ShipmentNotClaimedError: The order is still starting that shipment.
        """
        record, found = load_shipment_record(context, report.shipment_number)
        if found and record.status == SHIPMENT_STARTING:
            raise ShipmentNotClaimedError()
        # Ignore an unknown or rejected shipment, another run, and a repeated report.
        if (
            not found
            or not record.run_id
            or record.run_id != report.shipment_run_id
            or record.outcome
        ):
            return
        SHIPMENT_RECORDS.set(
            context,
            report.shipment_number,
            dataclasses.replace(record, outcome=report.outcome),
        )


# dex:group group-id:deliver group-label:"Deliver"
# dex:explanation text:"Waits for the carrier, then commits the delivered outcome."
class DeliverShipment(Step[ShipmentRequest]):
    """Waits for the simulated carrier, then stores the outcome."""

    def get_step_options(self) -> StepOptions:
        """Stores the outcome before the report Step starts, even when the Flow defaults to ASYNC."""
        return StepOptions(execute_durability=StepDurability.SYNC)

    def wait_for(self, context: Context, request: ShipmentRequest) -> Wait:
        return Wait.until(
            Timer.by_duration(timedelta(seconds=request.shipment.delivery_delay_seconds))
        )

    def execute(self, context: Context, request: ShipmentRequest) -> StepDecision:
        outcome = "delivered to " + request.shipment.destination
        SHIPMENT_OUTCOME.set(context, outcome)
        return go_to(
            ReportShipmentOutcome,
            PendingReport(
                order_flow_id=request.order_flow_id,
                report=ShipmentReport(
                    shipment_number=request.shipment.shipment_number,
                    shipment_run_id=context.run_id,
                    outcome=outcome,
                ),
            ),
        )


# dex:group group-id:report group-label:"Report"
# dex:explanation text:"Reports the committed outcome to the order that started this shipment."
class ReportShipmentOutcome(Step[PendingReport]):
    """Sends the committed outcome to the order's RPC."""

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        order_flow_provider: Callable[[], OrderFulfillmentFlow],
    ) -> None:
        self.client_provider = client_provider
        self.order_flow_provider = order_flow_provider

    def get_step_options(self) -> StepOptions:
        """Keeps the report SYNC, like the Step that stores the outcome."""
        return StepOptions(execute_durability=StepDurability.SYNC)

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, pending: PendingReport
    ) -> StepDecision:
        # This SDK pin has no RPC request ID. The locked RPC keeps the first
        # outcome, so a repeat changes nothing.
        try:
            await self.client_provider().invoke_rpc(
                self.order_flow_provider().record_shipment_outcome,
                pending.order_flow_id,
                pending.report,
            )
        except (FlowNotFoundError, FlowNotActiveOrNotFoundError):
            # A closed order ends the attempts; the outcome stays readable through the Client.
            pass
        return graceful_complete(pending.report.outcome)


class ShipmentFlow(Flow[ShipmentRequest]):
    """Delivers one shipment.

    Its Flow ID is the shipment number, so people and systems can look it up,
    retry it, or stop it by that number.
    """

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        order_flow_provider: Callable[[], OrderFulfillmentFlow],
    ) -> None:
        self.deliver_shipment = DeliverShipment()
        self.report_shipment_outcome = ReportShipmentOutcome(
            client_provider, order_flow_provider
        )

    def get_steps(self) -> StepList[ShipmentRequest]:
        return StepList.start_step(self.deliver_shipment).other_steps(
            self.report_shipment_outcome
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(SHIPMENT_OUTCOME)
