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

SELLER_REMINDER_TIMER = "seller-reminder"
SHIP_RETRY = RetryPolicy(
    initial_interval=timedelta(seconds=1),
    maximum_attempts=2,
)
CHARGE_RETRY = RetryPolicy(maximum_attempts=3)
REFUND_RETRY = RetryPolicy(maximum_attempts=3)


class ChargeStep(Step[OrderRequest]):
    def __init__(self, flow: "OrderProcessingFlow") -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=CHARGE_RETRY)

    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        self.flow.service.charge_user(input.email, input.customer_id, input.amount)
        self.flow.order_status.set(context, "charged")
        return go_to(self.flow.ship, input)


class ShipStep(Step[OrderRequest]):
    def __init__(self, flow: "OrderProcessingFlow") -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=SHIP_RETRY).on_execute_failure_proceed_to(
            self.flow.refund,
            StepOptions(execute_retry=REFUND_RETRY),
        )

    def wait_for(self, context: Context, input: OrderRequest) -> Wait:
        del context, input
        return Wait.any_of(
            self.flow.seller_ok.for_one(),
            Timer.by_duration(timedelta(hours=24), condition_id=SELLER_REMINDER_TIMER),
        )

    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        if context.has_timer_fired():
            self.flow.service.send_email(
                input.email,
                "Reminder: approve shipment",
                "Please approve or provide a tracking number.",
            )
            return go_to(self, input)
        if input.fail_ship:
            raise RuntimeError(f"ship failed for order {input.order_id}")
        self.flow.service.update_external_system(f"ship {input.order_id}")
        self.flow.order_status.set(context, "shipped")
        return graceful_complete(f"shipped:{input.order_id}")


class RefundStep(Step[OrderRequest]):
    def __init__(self, flow: "OrderProcessingFlow") -> None:
        self.flow = flow

    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        self.flow.service.update_external_system(f"refund {input.order_id}")
        self.flow.order_status.set(context, "refunded")
        return graceful_complete(f"refunded:{input.order_id}")


class OrderProcessingFlow(Flow[OrderRequest]):
    order_status = Attribute(
        "order-status",
        str,
        AttributeIndex(IndexType.KEYWORD),
    )
    seller_ok = Channel[str]("seller-ok", str)

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.charge = ChargeStep(self)
        self.ship = ShipStep(self)
        self.refund = RefundStep(self)

    def get_steps(self) -> StepList[OrderRequest]:
        return StepList.start_step(self.charge).other_steps(self.ship, self.refund)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.order_status, self.seller_ok)

    @rpc
    def approve(self, context: Context, _note: str) -> RPCResult[str]:
        self.seller_ok.publish(context, "approved")
        return RPCResult("ok")

    @rpc
    def describe(self, context: Context) -> RPCResult[str]:
        return RPCResult(self.order_status.get(context))
