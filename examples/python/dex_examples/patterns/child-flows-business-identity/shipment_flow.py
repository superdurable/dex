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
from typing import TYPE_CHECKING, Callable

from dex import (
    AsyncClient,
    AsyncContext,
    Attribute,
    Context,
    Flow,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
)

if TYPE_CHECKING:
    from dex_examples.patterns.child_flows_business_identity.order_fulfillment_flow import (
        OrderFulfillmentFlow,
    )


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
    """What a shipment sends to its order when it ends."""

    shipment_number: str = field(metadata={"dex_json_name": "shipmentNumber"})
    outcome: str


@dataclass(frozen=True)
class PendingReport:
    """The report a shipment still has to send to its order."""

    order_flow_id: str = field(metadata={"dex_json_name": "orderFlowId"})
    report: ShipmentReport


# The shipment's committed outcome. The order reads it when no report arrives.
SHIPMENT_OUTCOME = Attribute("shipmentOutcome", str)


# dex:group group-id:deliver group-label:"Deliver"
# dex:explanation text:"Waits for the carrier, then commits the delivered outcome."
class DeliverShipment(Step[ShipmentRequest]):
    """Waits for the simulated carrier, then stores the outcome."""

    def wait_for(self, context: Context, request: ShipmentRequest) -> Wait:
        return Wait.until(
            Timer.by_duration(timedelta(seconds=request.shipment.delivery_delay_seconds))
        )

    # Keeps the default SYNC durability, so the outcome is stored before the report Step starts.
    def execute(self, context: Context, request: ShipmentRequest) -> StepDecision:
        outcome = "delivered to " + request.shipment.destination
        SHIPMENT_OUTCOME.set(context, outcome)
        return go_to(
            ReportShipmentOutcome,
            PendingReport(
                order_flow_id=request.order_flow_id,
                report=ShipmentReport(
                    shipment_number=request.shipment.shipment_number, outcome=outcome
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

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, pending: PendingReport
    ) -> StepDecision:
        # This SDK pin has no RPC request ID. The order consumes one report
        # per shipment, so a repeat changes nothing.
        try:
            await self.client_provider().invoke_rpc(
                self.order_flow_provider().record_shipment_outcome,
                pending.order_flow_id,
                pending.report,
            )
        except (FlowNotFoundError, FlowNotActiveOrNotFoundError):
            # A closed order ends the attempts; it reads the shipment's outcome instead.
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
