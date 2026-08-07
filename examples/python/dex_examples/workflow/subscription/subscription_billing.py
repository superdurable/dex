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
from typing import Sequence

from dex_examples.my_dependency_service import MyDependencyService
from dex_examples.workflow.subscription.customer import Customer


def send_welcome_email(customer: Customer, service: MyDependencyService) -> None:
    service.send_email(customer.email, "welcome email", "hello content")


def trial_period(customer: Customer) -> timedelta:
    return timedelta(seconds=customer.subscription.trial_period_seconds)


def is_subscription_over(customer: Customer, period_number: int) -> bool:
    return period_number >= customer.subscription.max_billing_periods


def billing_period(customer: Customer) -> timedelta:
    return timedelta(seconds=customer.subscription.billing_period_seconds)


def send_subscription_over_email(
    customer: Customer,
    service: MyDependencyService,
) -> None:
    service.send_email(customer.email, "subscription over", "hello content")


def charge_current_period(customer: Customer, service: MyDependencyService) -> None:
    service.charge_user(
        customer.email,
        customer.id,
        customer.subscription.billing_period_charge,
    )


def send_canceled_email(customer: Customer, service: MyDependencyService) -> None:
    service.send_email(customer.email, "subscription canceled", "hello content")


def require_single_charge_amount(amounts: Sequence[int]) -> int:
    if len(amounts) != 1:
        raise ValueError(f"expected one charge amount, got {len(amounts)}")
    return amounts[0]


def apply_charge_amount(customer: Customer, amount: int) -> None:
    customer.subscription.billing_period_charge = amount
