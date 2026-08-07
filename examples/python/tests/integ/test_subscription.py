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

from typing import Callable

import pytest
from dex import Client

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.workflow.subscription.customer import Customer
from dex_examples.workflow.subscription.subscription import Subscription
from tests.integ.conftest import WAIT_TIMEOUT, wait_until

pytestmark = pytest.mark.integ

# A long trial keeps the Flow parked in Trial so cancel and charge updates win.
LONG_TRIAL_SECONDS = 600


def customer(trial_seconds: int, max_billing_periods: int) -> Customer:
    return Customer(
        "Quanzheng",
        "Long",
        "qlong",
        "qlong@example.com",
        Subscription(
            trial_period_seconds=trial_seconds,
            billing_period_seconds=1,
            max_billing_periods=max_billing_periods,
            billing_period_charge=100,
        ),
    )


def test_subscription_bills_every_period_then_ends(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("subscription")
    client.start_flow(app.subscription, flow_id, customer(2, 2), start_options())

    assert client.wait_for_flow(flow_id, str, WAIT_TIMEOUT) == "subscription ended"


def test_subscription_describe_returns_the_stored_plan(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("subscription")
    client.start_flow(
        app.subscription,
        flow_id,
        customer(LONG_TRIAL_SECONDS, 10),
        start_options(),
    )

    subscription = wait_until(
        "Initialize to store the customer",
        lambda: client.invoke_rpc(app.subscription.describe, flow_id),
    )
    assert subscription.trial_period_seconds == LONG_TRIAL_SECONDS
    assert subscription.billing_period_charge == 100

    client.publish(flow_id, app.subscription.cancel_subscription, None)
    assert client.wait_for_flow(flow_id, str, WAIT_TIMEOUT) == "subscription canceled"


def test_subscription_update_charge_amount(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("subscription")
    client.start_flow(
        app.subscription,
        flow_id,
        customer(LONG_TRIAL_SECONDS, 10),
        start_options(),
    )

    wait_until(
        "Initialize to store the customer",
        lambda: client.invoke_rpc(app.subscription.describe, flow_id),
    )
    client.publish(flow_id, app.subscription.update_charge_amount, 250)

    wait_until(
        "the new charge amount to be applied",
        lambda: client.invoke_rpc(
            app.subscription.describe,
            flow_id,
        ).billing_period_charge
        == 250,
    )

    client.publish(flow_id, app.subscription.cancel_subscription, None)
    assert client.wait_for_flow(flow_id, str, WAIT_TIMEOUT) == "subscription canceled"
