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

from concurrent.futures import ThreadPoolExecutor
from typing import Callable

import pytest
from dex_examples.products.customer_refund.billing_service import FakeBillingService
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
from tests.refund_rpc_gate import RefundRpcGate

from dex import (
    Attribute,
    AttributeMatch,
    Client,
    FlowStatus,
    IdReusePolicy,
    StartFlowOptions,
    StepExecutionId,
    WaitForAttributeOptions,
    WaitForStepCompletionOptions,
)

pytestmark = pytest.mark.integ

REFUND_ROUTE = "/products/customer-refund/manager-approval"
APPROVAL = {"decision": "approve", "reason": "Outage confirmed"}


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


@pytest.mark.parametrize(
    ("decision", "outcome"), [("approve", "refunded"), ("reject", "denied")]
)
def test_customer_refund_routes_decide_and_describe(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
    decision: str,
    outcome: str,
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("sync-customer-refund")
    started = http_client.get(
        f"{REFUND_ROUTE}/start",
        query_string={"workflowId": "", "flowId": flow_id, "scenario": ""},
    )
    assert started.status_code == 200, started.get_json()
    assert started.get_json().keys() == {"flowID", "runID"}
    assert started.get_json()["flowID"] == flow_id
    assert started.get_json()["runID"]
    waiting = http_client.get(f"{REFUND_ROUTE}/describe", query_string={"workflowId": flow_id})
    assert waiting.status_code == 200
    assert waiting.get_json() == {
        "flowID": flow_id,
        "status": "running",
        "caseStatus": "awaiting-manager",
        "refundId": None,
    }

    blank = http_client.get(
        f"{REFUND_ROUTE}/decide",
        query_string={"flowId": flow_id, "decision": "approve", "reason": " "},
    )
    assert blank.status_code == 400
    assert blank.get_json() == {"error": "a reason is required"}
    decided = http_client.get(
        f"{REFUND_ROUTE}/decide",
        query_string={"flowId": flow_id, "decision": decision, "reason": "Outage confirmed"},
    )
    assert decided.status_code == 200, decided.get_json()
    assert decided.get_json() == {"flowID": flow_id, "decision": decision}
    assert client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str) == outcome

    refunds = [
        refund
        for refund in FakeBillingService(app.config.customer_refund_ledger_path).list_refunds()
        if refund.idempotency_key == f"refund-{flow_id}"
    ]
    assert len(refunds) == (1 if decision == "approve" else 0)
    closed = http_client.get(f"{REFUND_ROUTE}/describe", query_string={"flowId": flow_id})
    assert closed.status_code == 200
    assert closed.get_json() == {
        "flowID": flow_id,
        "status": "completed",
        "caseStatus": f"closed-{outcome}",
        "refundId": refunds[0].refund_id if refunds else None,
    }
    late = http_client.get(
        f"{REFUND_ROUTE}/decide",
        query_string={"flowId": flow_id, "decision": "reject", "reason": "Too late"},
    )
    assert late.status_code == 404
    assert late.get_json() == {"error": "this refund is closed or missing"}


def test_customer_refund_decide_rejects_a_running_run_after_approval(
    app: SyncExampleApp, client: Client, new_flow_id: Callable[[str], str]
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("sync-customer-refund")
    started = http_client.get(
        f"{REFUND_ROUTE}/start", query_string={"flowId": flow_id, "scenario": "fail-then-succeed"}
    )
    assert started.status_code == 200, started.get_json()
    approval_query = {"flowId": flow_id, **APPROVAL}
    approved = http_client.get(f"{REFUND_ROUTE}/decide", query_string=approval_query)
    assert approved.status_code == 200, approved.get_json()
    running = http_client.get(f"{REFUND_ROUTE}/describe", query_string={"flowId": flow_id})
    assert running.status_code == 200
    assert running.get_json()["status"] == "running"
    assert running.get_json()["caseStatus"] == "approved"
    refused = http_client.get(f"{REFUND_ROUTE}/decide", query_string=approval_query)
    assert refused.status_code == 409
    assert refused.get_json() == {"error": "this refund is not waiting for a manager"}
    client.wait_for_flow(flow_id, WAIT_TIMEOUT)


def test_customer_refund_decide_reports_rpc_lock_conflict(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    flask_app = create_app(app)
    http_client = flask_app.test_client()
    flow_id = new_flow_id("sync-customer-refund")
    started = http_client.get(f"{REFUND_ROUTE}/start", query_string={"flowId": flow_id})
    assert started.status_code == 200, started.get_json()
    gate = RefundRpcGate(flow_id)
    monkeypatch.setattr(
        Attribute, "get", lambda attribute, context: gate.hold_lock(attribute, context)
    )
    approval_query = {"flowId": flow_id, **APPROVAL}
    with ThreadPoolExecutor(max_workers=2) as requests:
        winner = requests.submit(
            flask_app.test_client().get, f"{REFUND_ROUTE}/decide", query_string=approval_query
        )
        try:
            assert gate.lock_acquired.wait(5)
            loser = requests.submit(
                http_client.get, f"{REFUND_ROUTE}/decide", query_string=approval_query
            )
            refused = loser.result(timeout=5)
            assert refused.status_code == 409
            assert refused.get_json() == {
                "error": "another decision is changing this refund; try again"
            }
        finally:
            gate.release_lock.set()
        approved = winner.result(timeout=5)
        assert approved.status_code == 200
        assert approved.get_json() == {"flowID": flow_id, "decision": "approve"}
    client.wait_for_flow(flow_id, WAIT_TIMEOUT)


def test_customer_refund_duplicate_start_returns_a_json_conflict(
    app: SyncExampleApp, new_flow_id: Callable[[str], str]
) -> None:
    http_client = create_app(app).test_client()
    query = {"flowId": new_flow_id("sync-customer-refund")}
    started = http_client.get(f"{REFUND_ROUTE}/start", query_string=query)
    assert started.status_code == 200, started.get_json()
    duplicate = http_client.get(f"{REFUND_ROUTE}/start", query_string=query)
    assert duplicate.status_code == 409
    assert duplicate.get_json().keys() == {"error"}
    assert duplicate.get_json()["error"]


@pytest.mark.parametrize(("route", "query"), [("describe", {}), ("decide", APPROVAL)])
def test_customer_refund_lookup_validates_the_flow_id_and_type(
    app: SyncExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
    route: str,
    query: dict[str, str],
) -> None:
    http_client = create_app(app).test_client()
    path = f"{REFUND_ROUTE}/{route}"
    missing = http_client.get(path, query_string=query)
    assert missing.status_code == 400
    assert missing.get_json() == {"error": "workflowId is required"}
    unknown = http_client.get(path, query_string={"flowId": new_flow_id("unknown"), **query})
    assert unknown.status_code == 404
    assert unknown.get_json().keys() == {"error"}
    assert unknown.get_json()["error"]

    other_flow_id = new_flow_id("sync-channel")
    client.start_flow(app.channel, other_flow_id, 5, start_options())
    wrong_type = http_client.get(path, query_string={"flowId": other_flow_id, **query})
    assert wrong_type.status_code == 404
    assert wrong_type.get_json() == {"error": "no refund Flow with this ID"}


def test_customer_refund_decide_refuses_an_unknown_decision(app: SyncExampleApp) -> None:
    refused = create_app(app).test_client().get(
        f"{REFUND_ROUTE}/decide",
        query_string={"flowId": "unknown-refund", "decision": "maybe", "reason": "x"},
    )
    assert refused.status_code == 400
    assert refused.get_json() == {"error": "decision must be approve or reject"}


@pytest.mark.parametrize(("scenario", "outcome"), [("small", "refunded"), ("deny", "denied")])
def test_customer_refund_start_succeeds_when_the_run_closes_before_the_policy_wait(
    app: SyncExampleApp,
    client: Client,
    monkeypatch: pytest.MonkeyPatch,
    scenario: str,
    outcome: str,
) -> None:
    http_client = create_app(app).test_client()
    wait_for_step = client.wait_for_step_completion

    def wait_for_policy_after_close(
        flow_id: str, step: StepExecutionId, options: WaitForStepCompletionOptions
    ) -> None:
        client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        wait_for_step(flow_id, step, options)

    monkeypatch.setattr(client, "wait_for_step_completion", wait_for_policy_after_close)
    started = http_client.get(
        f"{REFUND_ROUTE}/start", query_string={"flowId": "", "scenario": scenario}
    )
    assert started.status_code == 200, started.get_json()
    assert started.get_json().keys() == {"flowID", "runID"}
    flow_id = started.get_json()["flowID"]
    assert flow_id.startswith("customer-refund-")
    assert started.get_json()["runID"]
    refunds = [
        refund
        for refund in FakeBillingService(app.config.customer_refund_ledger_path).list_refunds()
        if refund.idempotency_key == f"refund-{flow_id}"
    ]
    assert len(refunds) == (1 if scenario == "small" else 0)
    if refunds:
        assert refunds[0].refund_id.startswith("rf_")
    closed = http_client.get(f"{REFUND_ROUTE}/describe", query_string={"flowId": flow_id})
    assert closed.status_code == 200
    assert closed.get_json() == {
        "flowID": flow_id,
        "status": "completed",
        "caseStatus": f"closed-{outcome}",
        "refundId": refunds[0].refund_id if refunds else None,
    }


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
