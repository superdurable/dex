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

from dex import (
    Attribute,
    AttributeIndex,
    Channel,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    RPCResult,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.shared.my_dependency_service import MyDependencyService
from dex_examples.products.order_processing.order_request import OrderRequest

order_status = Attribute(
    "order-status",
    str,
    AttributeIndex(IndexType.KEYWORD),
)
seller_ok = Channel[str]("seller-ok", str)


class ChargeStep(Step[OrderRequest]):
    def __init__(self, service: MyDependencyService, ship: ShipStep) -> None:
        self.service = service
        self.ship = ship

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                # total_duration=timedelta(hours=1),
                total_duration=timedelta(seconds=3),
            )
        )

    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        self.service.charge_user(input.email, input.customer_id, input.amount)
        order_status.set(context, "charged")
        return go_to(self.ship, input)


class ShipStep(Step[OrderRequest]):
    def __init__(self, service: MyDependencyService, refund: RefundStep) -> None:
        self.service = service
        self.refund = refund

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                # total_duration=timedelta(hours=1),
                total_duration=timedelta(seconds=3),
            )
        ).on_execute_failure_proceed_to(
            self.refund,
            StepOptions(
                execute_retry=RetryPolicy(
                    # total_duration=timedelta(hours=1),
                    total_duration=timedelta(seconds=3),
                )
            ),
        )

    def wait_for(self, context: Context, input: OrderRequest) -> Wait:
        del context, input
        return Wait.any_of(
            seller_ok.for_one(),
            Timer.by_duration(timedelta(hours=24)),
        )

    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        if context.has_timer_fired():
            self.service.send_email(
                input.email,
                "Reminder: approve shipment",
                "Please approve or provide a tracking number.",
            )
            return go_to(self, input)
        self.service.ship_item(input.order_id, input.test_fail_at_shipping)
        order_status.set(context, "shipped")
        return graceful_complete(f"shipped:{input.order_id}")


class RefundStep(Step[OrderRequest]):
    def __init__(self, service: MyDependencyService) -> None:
        self.service = service

    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        self.service.update_external_system(f"refund {input.order_id}")
        order_status.set(context, "refunded")
        return graceful_complete(f"refunded:{input.order_id}")


class OrderProcessingFlow(Flow[OrderRequest]):
    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.refund = RefundStep(service)
        self.ship = ShipStep(service, self.refund)
        self.charge = ChargeStep(service, self.ship)

    def get_steps(self) -> StepList[OrderRequest]:
        return StepList.start_step(self.charge).other_steps(self.ship, self.refund)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(order_status, seller_ok)

    @rpc
    def approve(self, context: Context, _note: str) -> RPCResult[str]:
        seller_ok.publish(context, "approved")
        return RPCResult("ok")

    @rpc
    def describe(self, context: Context) -> RPCResult[str]:
        return RPCResult(order_status.get(context))
