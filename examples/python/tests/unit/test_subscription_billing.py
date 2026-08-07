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

"""Pure-function tests for the subscription billing helpers; no Dex server."""

from __future__ import annotations

from dataclasses import dataclass, replace
from datetime import timedelta

import pytest

from dex_examples.my_dependency_service import MyDependencyService
from dex_examples.workflow.subscription import subscription_billing
from dex_examples.workflow.subscription.customer import Customer
from dex_examples.workflow.subscription.subscription import Subscription


@dataclass(frozen=True)
class RecordedEmail:
    recipient: str
    subject: str
    content: str


@dataclass(frozen=True)
class RecordedCharge:
    email: str
    customer_id: str
    amount: int


class RecordingService(MyDependencyService):
    def __init__(self) -> None:
        self.emails: list[RecordedEmail] = []
        self.charges: list[RecordedCharge] = []

    def send_email(self, recipient: str, subject: str, content: str) -> None:
        self.emails.append(RecordedEmail(recipient, subject, content))

    def charge_user(self, email: str, customer_id: str, amount: int) -> None:
        self.charges.append(RecordedCharge(email, customer_id, amount))


def make_customer() -> Customer:
    return Customer(
        "Quanzheng",
        "Long",
        "123",
        "qlong.seattle@gmail.com",
        Subscription(
            trial_period_seconds=2,
            billing_period_seconds=1,
            max_billing_periods=10,
            billing_period_charge=100,
        ),
    )


TEST_CUSTOMER = make_customer()


def test_send_welcome_email() -> None:
    service = RecordingService()

    subscription_billing.send_welcome_email(TEST_CUSTOMER, service)

    assert service.emails == [
        RecordedEmail(TEST_CUSTOMER.email, "welcome email", "hello content")
    ]
    assert subscription_billing.trial_period(TEST_CUSTOMER) == timedelta(seconds=2)


def test_is_subscription_over() -> None:
    assert not subscription_billing.is_subscription_over(TEST_CUSTOMER, 0)
    assert subscription_billing.is_subscription_over(
        TEST_CUSTOMER,
        TEST_CUSTOMER.subscription.max_billing_periods,
    )
    assert subscription_billing.billing_period(TEST_CUSTOMER) == timedelta(seconds=1)


def test_charge_current_period() -> None:
    service = RecordingService()

    subscription_billing.charge_current_period(TEST_CUSTOMER, service)

    assert service.charges == [
        RecordedCharge(TEST_CUSTOMER.email, TEST_CUSTOMER.id, 100)
    ]
    assert service.emails == []


def test_send_subscription_over_email() -> None:
    service = RecordingService()

    subscription_billing.send_subscription_over_email(TEST_CUSTOMER, service)

    assert service.charges == []
    assert service.emails == [
        RecordedEmail(TEST_CUSTOMER.email, "subscription over", "hello content")
    ]


def test_send_canceled_email() -> None:
    service = RecordingService()

    subscription_billing.send_canceled_email(TEST_CUSTOMER, service)

    assert service.emails == [
        RecordedEmail(TEST_CUSTOMER.email, "subscription canceled", "hello content")
    ]


def test_apply_charge_amount() -> None:
    customer = replace(
        TEST_CUSTOMER,
        subscription=replace(TEST_CUSTOMER.subscription),
    )

    subscription_billing.apply_charge_amount(customer, 200)

    assert customer.subscription.billing_period_charge == 200
    assert TEST_CUSTOMER.subscription.billing_period_charge == 100
    assert subscription_billing.require_single_charge_amount([200]) == 200


def test_require_single_charge_amount_rejects_unexpected_results() -> None:
    with pytest.raises(TypeError):
        subscription_billing.require_single_charge_amount(None)  # type: ignore[arg-type]
    with pytest.raises(ValueError):
        subscription_billing.require_single_charge_amount([])
    with pytest.raises(ValueError):
        subscription_billing.require_single_charge_amount([100, 200])
