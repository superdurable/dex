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

"""Drives the customer refund routes of the shared example server."""

from __future__ import annotations

from typing import Any, Callable

import pytest
from dex_examples.app import ExampleApp
from dex_examples.http_app import create_app
from dex_examples.products.customer_refund.billing_service import FakeBillingService
from dex_examples.products.customer_refund.email_service import FakeEmailService
from quart.typing import TestClientProtocol as QuartTestClient
from tests.integ.conftest import WAIT_TIMEOUT

from dex import AsyncClient

pytestmark = pytest.mark.integ

ROUTE_PREFIX = "/products/customer-refund/manager-approval"


async def _get_json(
    http_client: QuartTestClient,
    route: str,
    query: dict[str, str],
    expected_status: int = 200,
) -> Any:
    response = await http_client.get(f"{ROUTE_PREFIX}/{route}", query_string=query)
    body = await response.get_json()
    assert response.status_code == expected_status, body
    return body


async def _start_refund(
    http_client: QuartTestClient, flow_id: str, scenario: str
) -> None:
    started = await _get_json(
        http_client, "start", {"workflowId": flow_id, "scenario": scenario}
    )
    assert started["flowID"] == flow_id
    assert started["runID"]


async def test_manager_approves_through_the_routes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "normal")
    waiting = await _get_json(http_client, "describe", {"flowId": flow_id})
    assert waiting["caseStatus"] == "awaiting-manager"
    assert waiting["status"] == "running"

    decided = await _get_json(
        http_client,
        "decide",
        {"flowId": flow_id, "decision": "approve", "reason": "Outage confirmed"},
    )
    assert decided == {"flowID": flow_id, "decision": "approve"}
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.single_output(str) == "refunded"

    closed = await _get_json(http_client, "describe", {"flowId": flow_id})
    assert closed["caseStatus"] == "closed-refunded"
    assert closed["status"] == "completed"
    ledger_path = app.config.customer_refund_ledger_path
    refunds = [
        refund
        for refund in FakeBillingService(ledger_path).list_refunds()
        if refund.idempotency_key == f"refund-{flow_id}"
    ]
    assert [refund.refund_id for refund in refunds] == [closed["refundId"]]
    assert f"email-{flow_id}" in FakeEmailService(ledger_path).list_sent_keys()


async def test_manager_rejects_through_the_routes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "normal")
    await _get_json(
        http_client,
        "decide",
        {"flowId": flow_id, "decision": "reject", "reason": "Credit offered instead"},
    )
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.single_output(str) == "denied"
    closed = await _get_json(http_client, "describe", {"flowId": flow_id})
    assert closed["caseStatus"] == "closed-denied"
    assert closed["refundId"] == ""


async def test_decide_refuses_a_blank_reason_and_keeps_waiting(
    app: ExampleApp,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "normal")
    refused = await _get_json(
        http_client,
        "decide",
        {"flowId": flow_id, "decision": "approve", "reason": "   "},
        expected_status=400,
    )
    assert refused == {"error": "a reason is required"}
    still_waiting = await _get_json(http_client, "describe", {"flowId": flow_id})
    assert still_waiting["caseStatus"] == "awaiting-manager"
    await _get_json(
        http_client,
        "decide",
        {"flowId": flow_id, "decision": "approve", "reason": "Outage confirmed"},
    )


async def test_decide_refuses_a_run_that_is_not_awaiting_the_manager(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "deny")
    await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    refused = await _get_json(
        http_client,
        "decide",
        {"flowId": flow_id, "decision": "approve", "reason": "Outage confirmed"},
        expected_status=409,
    )
    assert refused["error"]


async def test_start_mints_a_flow_id_and_refuses_an_unknown_scenario(
    app: ExampleApp,
    client: AsyncClient,
) -> None:
    http_client = create_app(app).test_client()
    started = await _get_json(http_client, "start", {"scenario": "small"})
    assert started["flowID"].startswith("customer-refund-")
    result = await client.wait_for_flow(started["flowID"], WAIT_TIMEOUT)
    assert result.single_output(str) == "refunded"

    refused = await _get_json(
        http_client, "start", {"scenario": "everything"}, expected_status=400
    )
    assert "scenario must be one of" in refused["error"]


async def test_decide_refuses_an_unknown_decision(
    app: ExampleApp,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    refused = await _get_json(
        http_client,
        "decide",
        {"flowId": new_flow_id("customer-refund"), "decision": "maybe", "reason": "x"},
        expected_status=400,
    )
    assert refused == {"error": "decision must be approve or reject"}
