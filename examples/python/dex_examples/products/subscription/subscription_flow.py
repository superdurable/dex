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

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    force_complete,
    go_to,
    go_to_many,
    rpc,
)

from dex_examples.shared.my_dependency_service import MyDependencyService
from dex_examples.products.subscription import subscription_billing
from dex_examples.products.subscription.customer import Customer
from dex_examples.products.subscription.subscription import Subscription

SUBSCRIPTION_OVER_KEY = "subscription-over"


class ChargeCurrentBill(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        customer_details: Attribute[Customer],
        billing_period_number: Attribute[int],
    ) -> None:
        self.service = service
        self.customer_details = customer_details
        self.billing_period_number = billing_period_number

    def wait_for(self, context: Context, input: None) -> Wait:
        customer = self.customer_details.get(context)
        period_number = self.billing_period_number.get(context)
        if subscription_billing.is_subscription_over(customer, period_number):
            context.set_step_execution_local(SUBSCRIPTION_OVER_KEY, True)
            return Wait.skip_immediately()
        self.billing_period_number.set(context, period_number + 1)
        return Wait.until(
            Timer.by_duration(subscription_billing.billing_period(customer))
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        customer = self.customer_details.get(context)
        if context.get_step_execution_local(SUBSCRIPTION_OVER_KEY, bool):
            subscription_billing.send_subscription_over_email(customer, self.service)
            return force_complete("subscription ended")
        subscription_billing.charge_current_period(customer, self.service)
        return go_to(ChargeCurrentBill, None)


class Trial(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        customer_details: Attribute[Customer],
        billing_period_number: Attribute[int],
        charge_current_bill: ChargeCurrentBill,
    ) -> None:
        self.service = service
        self.customer_details = customer_details
        self.billing_period_number = billing_period_number
        self.charge_current_bill = charge_current_bill

    def wait_for(self, context: Context, input: None) -> Wait:
        customer = self.customer_details.get(context)
        subscription_billing.send_welcome_email(customer, self.service)
        return Wait.until(
            Timer.by_duration(subscription_billing.trial_period(customer))
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        self.billing_period_number.set(context, 0)
        return go_to(ChargeCurrentBill, None)


class Cancel(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        customer_details: Attribute[Customer],
        cancel_subscription: Channel[None],
    ) -> None:
        self.service = service
        self.customer_details = customer_details
        self.cancel_subscription = cancel_subscription

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(self.cancel_subscription.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        customer = self.customer_details.get(context)
        subscription_billing.send_canceled_email(customer, self.service)
        return force_complete("subscription canceled")


class UpdateChargeAmount(Step[None]):
    def __init__(
        self,
        customer_details: Attribute[Customer],
        update_charge_amount: Channel[int],
    ) -> None:
        self.customer_details = customer_details
        self.update_charge_amount = update_charge_amount

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(self.update_charge_amount.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        amounts = self.update_charge_amount.results(context)
        amount = subscription_billing.require_single_charge_amount(amounts)
        customer = self.customer_details.get(context)
        subscription_billing.apply_charge_amount(customer, amount)
        self.customer_details.set(context, customer)
        return go_to(UpdateChargeAmount, None)


class Initialize(Step[Customer]):
    def __init__(
        self,
        customer_details: Attribute[Customer],
        trial: Trial,
        cancel: Cancel,
        update_charge_amount: UpdateChargeAmount,
    ) -> None:
        self.customer_details = customer_details
        self.trial = trial
        self.cancel = cancel
        self.update_charge_amount = update_charge_amount

    def execute(self, context: Context, input: Customer) -> StepDecision:
        self.customer_details.set(context, input)
        return go_to_many(
            StepMovement.of(Trial, None),
            StepMovement.of(Cancel, None),
            StepMovement.of(UpdateChargeAmount, None),
        )


class SubscriptionFlow(Flow[Customer]):
    billing_period_number = Attribute("billing-period-number", int)
    customer_details = Attribute("customer", Customer)
    cancel_subscription = Channel[None]("cancel-subscription", type(None))
    update_charge_amount = Channel("update-charge-amount", int)

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.charge_current_bill = ChargeCurrentBill(
            service,
            self.customer_details,
            self.billing_period_number,
        )
        self.trial = Trial(
            service,
            self.customer_details,
            self.billing_period_number,
            self.charge_current_bill,
        )
        self.cancel = Cancel(
            service,
            self.customer_details,
            self.cancel_subscription,
        )
        self.update_charge_amount_step = UpdateChargeAmount(
            self.customer_details,
            self.update_charge_amount,
        )
        self.initialize = Initialize(
            self.customer_details,
            self.trial,
            self.cancel,
            self.update_charge_amount_step,
        )

    def get_steps(self) -> StepList[Customer]:
        return StepList.start_step(self.initialize).other_steps(
            self.trial,
            self.charge_current_bill,
            self.cancel,
            self.update_charge_amount_step,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.billing_period_number,
            self.customer_details,
            self.cancel_subscription,
            self.update_charge_amount,
        )

    @rpc
    def describe(self, context: Context) -> RPCResult[Subscription]:
        return RPCResult(self.customer_details.get(context).subscription)
