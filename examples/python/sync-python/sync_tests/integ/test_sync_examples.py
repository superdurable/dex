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
from dex_examples.products.customer_refund.start import SCENARIOS
from dex_examples.products.engagement.engagement_flow import EngagementFlow
from dex_examples.products.engagement.engagement_input import EngagementInput
from dex_examples.products.engagement.status import Status
from dex_examples.products.money_transfer.transfer_request import TransferRequest
from dex_examples.products.subscription.customer import Customer
from dex_examples.products.subscription.subscription import Subscription
from dex_examples.products.subscription.subscription_flow import SubscriptionFlow
from sync_examples.app import SyncExampleApp
from sync_examples.config import DEFAULT_TIMEOUT, start_options
from sync_examples.http_app import create_app
from sync_examples.wait import (
    WAIT_TIMEOUT,
    flow_status_or_none,
    wait_until,
)

from dex import (
    AttributeMatch,
    Client,
    FlowStatus,
    IdReusePolicy,
    StartFlowOptions,
    WaitForAttributeOptions,
)

pytestmark = pytest.mark.integ


def test_channel_approve_completes(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("sync-channel")
    client.start_flow(app.channel, flow_id, 5, start_options())
    client.invoke_rpc(app.channel.publish_approval_message, flow_id)
    assert client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str) == "approved"


def test_money_transfer_completes_the_saga(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("sync-money")
    request = TransferRequest("from-account", "to-account", 100, "test-notes")
    client.start_flow(app.money_transfer, flow_id, request, start_options())
    output = client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str)
    assert "transfer is done" in output
    assert "from from-account to to-account for amount 100" in output


def test_customer_refund_routes_approve_and_describe(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    route_prefix = "/products/customer-refund/manager-approval"
    flow_id = new_flow_id("sync-customer-refund")
    started = http_client.get(
        f"{route_prefix}/start", query_string={"workflowId": flow_id, "scenario": "normal"}
    )
    assert started.status_code == 200, started.get_json()
    assert started.get_json()["flowID"] == flow_id
    waiting = http_client.get(f"{route_prefix}/describe", query_string={"workflowId": flow_id})
    assert waiting.get_json()["caseStatus"] == "awaiting-manager"

    blank = http_client.get(
        f"{route_prefix}/decide",
        query_string={"flowId": flow_id, "decision": "approve", "reason": " "},
    )
    assert blank.status_code == 400
    assert blank.get_json() == {"error": "a reason is required"}
    approved = http_client.get(
        f"{route_prefix}/decide",
        query_string={"flowId": flow_id, "decision": "approve", "reason": "Outage confirmed"},
    )
    assert approved.status_code == 200, approved.get_json()
    assert client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str) == "refunded"

    closed = http_client.get(f"{route_prefix}/describe", query_string={"flowId": flow_id})
    assert closed.get_json()["caseStatus"] == "closed-refunded"
    assert closed.get_json()["refundId"].startswith("rf_")
    late = http_client.get(
        f"{route_prefix}/decide",
        query_string={"flowId": flow_id, "decision": "reject", "reason": "Too late"},
    )
    assert late.status_code == 409


def test_customer_refund_policy_denial_completes(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("sync-customer-refund")
    client.start_flow(app.customer_refund, flow_id, SCENARIOS["deny"], start_options())
    assert client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str) == "denied"


def test_engagement_accept_completes(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("sync-engagement")
    client.start_flow(
        app.engagement,
        flow_id,
        EngagementInput("test-employer-id", "test-job-seeker-id", "test-notes"),
        start_options(),
    )
    client.wait_for_attribute_match(
        flow_id,
        EngagementFlow.employer_id,
        AttributeMatch.equal_to("test-employer-id"),
        WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
    )
    description = client.invoke_rpc(app.engagement.describe, flow_id)
    assert description.employer_id == "test-employer-id"
    assert description.current_status == Status.INITIATED
    assert client.invoke_rpc(app.engagement.accept, flow_id, "sounds good") is (
        Status.ACCEPTED
    )
    assert client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str) == "done"


def test_subscription_describe_and_cancel(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("sync-subscription")
    customer = Customer(
        "Quanzheng",
        "Long",
        "qlong",
        "qlong@example.com",
        Subscription(
            trial_period_seconds=600,
            billing_period_seconds=1,
            max_billing_periods=10,
            billing_period_charge=100,
        ),
    )
    client.start_flow(app.subscription, flow_id, customer, start_options())
    client.wait_for_attribute_match(
        flow_id,
        SubscriptionFlow.billing_period_number,
        AttributeMatch.greater_than_or_equal(0),
        WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
    )
    subscription = client.invoke_rpc(app.subscription.describe, flow_id)
    assert subscription.billing_period_charge == 100
    client.invoke_rpc(app.subscription.cancel_subscription_rpc, flow_id)
    assert (
        client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str)
        == "subscription canceled"
    )


def test_parent_child_starts_a_child(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("sync-parent-child")
    run_id = client.start_flow(
        app.parent_flow_v2,
        flow_id,
        2,
        StartFlowOptions(
            timeout=DEFAULT_TIMEOUT,
            id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
        ),
    )
    assert run_id
    child_id = f"{flow_id}-child-0"
    wait_until(
        "parent-child started a child flow",
        lambda: flow_status_or_none(client, child_id),
        WAIT_TIMEOUT,
    )


def test_interruptible_start_and_cancel(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("sync-interruptible")
    client.start_flow(app.interruptible, flow_id, None, start_options())
    wait_until(
        "interruptible flow running",
        lambda: flow_status_or_none(client, flow_id) is FlowStatus.RUNNING,
    )
    client.invoke_rpc(app.interruptible.interrupt, flow_id)
    wait_until(
        "interruptible flow completed",
        lambda: flow_status_or_none(client, flow_id) is FlowStatus.COMPLETED,
        WAIT_TIMEOUT,
    )
