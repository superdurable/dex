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

import random

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    force_fail,
    go_to,
    graceful_complete,
)

from dex_examples.patterns.recovery.failure_recovery_workflow_input import (
    FailureRecoveryWorkflowInput,
)


class DatabaseConnection:
    def reduce_quantity(self, item_name: str, quantity: int) -> None:
        print(f"Reducing quantity: {quantity}")
        if quantity > random.randrange(10):
            raise RuntimeError("not enough items available")

    def increase_quantity(self, item_name: str, quantity: int) -> None:
        print(f"Increasing quantity: {quantity}")

    def get_item_price(self, item_name: str) -> float:
        return 3.14


class PaymentProcessor:
    def process_payment(self, price: float) -> None:
        raise RuntimeError("Payment could not be processed")

    def void_payment(self, price: float) -> None:
        print(f"Voiding payment for $ {price:.2f}")


class UpdateQuantityRecovery(Step[FailureRecoveryWorkflowInput]):
    def __init__(self, database: DatabaseConnection) -> None:
        self.database = database

    def execute(
        self,
        context: Context,
        input: FailureRecoveryWorkflowInput,
    ) -> StepDecision:
        self.database.increase_quantity(input.item_name, input.requested_quantity)
        return force_fail("Failed to process transaction")


class VoidPaymentRecovery(Step[int]):
    def __init__(
        self,
        update_quantity_recovery: UpdateQuantityRecovery,
        database: DatabaseConnection,
        payment_processor: PaymentProcessor,
        workflow_input: Attribute[FailureRecoveryWorkflowInput],
    ) -> None:
        self.update_quantity_recovery = update_quantity_recovery
        self.database = database
        self.payment_processor = payment_processor
        self.workflow_input = workflow_input

    def execute(self, context: Context, input: int) -> StepDecision:
        workflow = self.workflow_input.get(context)
        item_value = self.database.get_item_price(workflow.item_name)
        self.payment_processor.void_payment(workflow.requested_quantity * item_value)
        return go_to(UpdateQuantityRecovery, workflow)


class ChargeForItems(Step[int]):
    def __init__(
        self,
        void_payment_recovery: VoidPaymentRecovery,
        database: DatabaseConnection,
        payment_processor: PaymentProcessor,
        workflow_input: Attribute[FailureRecoveryWorkflowInput],
    ) -> None:
        self.void_payment_recovery = void_payment_recovery
        self.database = database
        self.payment_processor = payment_processor
        self.workflow_input = workflow_input

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=5)
        ).on_execute_failure_proceed_to(VoidPaymentRecovery)

    def execute(self, context: Context, input: int) -> StepDecision:
        workflow = self.workflow_input.get(context)
        item_value = self.database.get_item_price(workflow.item_name)
        self.payment_processor.process_payment(workflow.requested_quantity * item_value)
        return graceful_complete()


class UpdateItemQuantity(Step[FailureRecoveryWorkflowInput]):
    def __init__(
        self,
        charge_for_items: ChargeForItems,
        update_quantity_recovery: UpdateQuantityRecovery,
        database: DatabaseConnection,
        workflow_input: Attribute[FailureRecoveryWorkflowInput],
    ) -> None:
        self.charge_for_items = charge_for_items
        self.update_quantity_recovery = update_quantity_recovery
        self.database = database
        self.workflow_input = workflow_input

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=5)
        ).on_execute_failure_proceed_to(UpdateQuantityRecovery)

    def execute(
        self,
        context: Context,
        input: FailureRecoveryWorkflowInput,
    ) -> StepDecision:
        self.workflow_input.set(context, input)
        self.database.reduce_quantity(input.item_name, input.requested_quantity)
        return go_to(ChargeForItems, input.requested_quantity)


class FailureRecoveryFlow(Flow[FailureRecoveryWorkflowInput]):
    WORKFLOW_INPUT_KEY = "workflow-input-data-attribute-key"

    workflow_input = Attribute(WORKFLOW_INPUT_KEY, FailureRecoveryWorkflowInput)

    def __init__(self) -> None:
        database = DatabaseConnection()
        payment_processor = PaymentProcessor()

        self.update_quantity_recovery = UpdateQuantityRecovery(database)
        self.void_payment_recovery = VoidPaymentRecovery(
            self.update_quantity_recovery,
            database,
            payment_processor,
            self.workflow_input,
        )
        self.charge_for_items = ChargeForItems(
            self.void_payment_recovery,
            database,
            payment_processor,
            self.workflow_input,
        )
        self.update_item_quantity = UpdateItemQuantity(
            self.charge_for_items,
            self.update_quantity_recovery,
            database,
            self.workflow_input,
        )

    def get_steps(self) -> StepList[FailureRecoveryWorkflowInput]:
        return StepList.start_step(self.update_item_quantity).other_steps(
            self.charge_for_items,
            self.update_quantity_recovery,
            self.void_payment_recovery,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.workflow_input)
