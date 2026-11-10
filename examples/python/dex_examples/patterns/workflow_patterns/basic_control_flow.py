# Copyright (c) 2026 Super Durable, Inc.
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

"""Workflow Patterns WCP-1 to WCP-5: sequence, split, join, choice, and merge."""

from __future__ import annotations

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    dead_end,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

# WCP-1 Sequence: each Step starts after the previous one completes.


class ReceiveOrder(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return go_to(CheckInventory, [*input, "ReceiveOrder"])


class CheckInventory(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return go_to(ShipOrder, [*input, "CheckInventory"])


class ShipOrder(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete([*input, "ShipOrder"])


class SequenceFlow(Flow[list[str]]):
    def __init__(self) -> None:
        self.receive_order = ReceiveOrder()
        self.check_inventory = CheckInventory()
        self.ship_order = ShipOrder()

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(self.receive_order).other_steps(
            self.check_inventory, self.ship_order
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


# WCP-2 Parallel Split and WCP-3 Synchronization: the join waits for each branch.

FULFILLMENT_BRANCH_COMPLETED = Channel("fulfillmentBranchCompleted", str)
PAYMENT_APPROVED = Channel("paymentApproved", str)
FULFILLMENT_BRANCH_COUNT = 2


class SplitOrder(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(PackItems, None),
            StepMovement.of(ChargePayment, None),
            StepMovement.of(SynchronizeFulfillment, None),
        )


class PackItems(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        FULFILLMENT_BRANCH_COMPLETED.publish(context, "PackItems")
        return dead_end()


class ChargePayment(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(PAYMENT_APPROVED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        FULFILLMENT_BRANCH_COMPLETED.publish(context, "ChargePayment")
        return dead_end()


class SynchronizeFulfillment(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(FULFILLMENT_BRANCH_COMPLETED.for_n(FULFILLMENT_BRANCH_COUNT))

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(sorted(FULFILLMENT_BRANCH_COMPLETED.results(context)))


class ParallelSplitSynchronizationFlow(Flow[None]):
    def __init__(self) -> None:
        self.split_order = SplitOrder()
        self.pack_items = PackItems()
        self.charge_payment = ChargePayment()
        self.synchronize_fulfillment = SynchronizeFulfillment()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.split_order).other_steps(
            self.pack_items, self.charge_payment, self.synchronize_fulfillment
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(FULFILLMENT_BRANCH_COMPLETED, PAYMENT_APPROVED)

    @rpc
    def approve_payment(self, context: Context) -> None:
        PAYMENT_APPROVED.publish(context, "approved")


# WCP-4 Exclusive Choice and WCP-5 Simple Merge: one branch runs, then both merge.

MANUAL_REVIEW_THRESHOLD = 1000


class RouteClaim(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        if input >= MANUAL_REVIEW_THRESHOLD:
            return go_to(ManualReview, ["RouteClaim"])
        return go_to(AutoApprove, ["RouteClaim"])


class ManualReview(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return go_to(CloseClaim, [*input, "ManualReview"])


class AutoApprove(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return go_to(CloseClaim, [*input, "AutoApprove"])


class CloseClaim(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete([*input, "CloseClaim"])


class ExclusiveChoiceSimpleMergeFlow(Flow[int]):
    def __init__(self) -> None:
        self.route_claim = RouteClaim()
        self.manual_review = ManualReview()
        self.auto_approve = AutoApprove()
        self.close_claim = CloseClaim()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.route_claim).other_steps(
            self.manual_review, self.auto_approve, self.close_claim
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
