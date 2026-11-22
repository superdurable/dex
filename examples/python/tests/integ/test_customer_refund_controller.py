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

import asyncio
from typing import Any, Callable

import grpc
import pytest
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.http_app import create_app
from dex_examples.products.customer_refund.billing_service import FakeBillingService
from dex_examples.products.customer_refund.email_service import FakeEmailService
from dex_examples.products.customer_refund.refund_flow import refund_run_status
from quart.typing import TestClientProtocol as QuartTestClient
from tests.integ.conftest import WAIT_TIMEOUT
from tests.refund_rpc_gate import AsyncRefundRpcGate

from dex import (
    AsyncClient,
    ErrorSubStatus,
    FlowClosingError,
    FlowStatus,
    StepExecutionId,
    WaitForStepCompletionOptions,
)
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex.dexpb.dex_pb2 import InvokeWorkerRPCRequest, InvokeWorkerRPCResponse

pytestmark = pytest.mark.integ

ROUTE_PREFIX = "/products/customer-refund/manager-approval"
APPROVAL = {"decision": "approve", "reason": "Outage confirmed"}


@pytest.mark.parametrize(
    ("status", "expected"),
    [
        (FlowStatus.RUNNING, "running"),
        (FlowStatus.COMPLETED, "completed"),
        (FlowStatus.FAILED, "failed"),
        (FlowStatus.CANCELED, "canceled"),
        (FlowStatus.TERMINATED, "terminated"),
        (FlowStatus.SERVER_SIDE_TIMEOUT_INTERNAL_ONLY, "timeout"),
        (None, "unknown"),
    ],
)
def test_refund_run_status(status: FlowStatus | None, expected: str) -> None:
    assert refund_run_status(status) == expected


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
        http_client, "start", {"workflowId": "", "flowId": flow_id, "scenario": scenario}
    )
    assert started.keys() == {"flowID", "runID"}
    assert started["flowID"] == flow_id
    assert started["runID"]


async def test_manager_approves_through_the_routes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "")
    waiting = await _get_json(http_client, "describe", {"workflowId": "", "flowId": flow_id})
    assert waiting == {
        "flowID": flow_id,
        "status": "running",
        "caseStatus": "awaiting-manager",
        "refundId": None,
    }

    decided = await _get_json(
        http_client,
        "decide",
        {"flowId": flow_id, "decision": "approve", "reason": "Outage confirmed"},
    )
    assert decided == {"flowID": flow_id, "decision": "approve"}
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.single_output(str) == "refunded"

    closed = await _get_json(http_client, "describe", {"flowId": flow_id})
    ledger_path = app.config.customer_refund_ledger_path
    refunds = [
        refund
        for refund in FakeBillingService(ledger_path).list_refunds()
        if refund.idempotency_key == f"refund-{flow_id}"
    ]
    assert len(refunds) == 1
    assert closed == {
        "flowID": flow_id,
        "status": "completed",
        "caseStatus": "closed-refunded",
        "refundId": refunds[0].refund_id,
    }
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
    assert closed == {
        "flowID": flow_id,
        "status": "completed",
        "caseStatus": "closed-denied",
        "refundId": None,
    }


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
    await _start_refund(http_client, flow_id, "fail-then-succeed")
    await _get_json(http_client, "decide", {"flowId": flow_id, **APPROVAL})
    running = await _get_json(http_client, "describe", {"flowId": flow_id})
    assert running["status"] == "running"
    assert running["caseStatus"] == "approved"
    refused = await _get_json(
        http_client, "decide", {"flowId": flow_id, **APPROVAL}, expected_status=409
    )
    assert refused == {"error": "this refund is not waiting for a manager"}
    await client.wait_for_flow(flow_id, WAIT_TIMEOUT)


async def test_decide_refuses_a_closed_run(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "deny")
    await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    refused = await _get_json(
        http_client, "decide", {"flowId": flow_id, **APPROVAL}, expected_status=404
    )
    assert refused == {"error": "this refund is closed or missing"}


async def test_decide_reports_rpc_lock_conflict(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "normal")
    gate = AsyncRefundRpcGate(flow_id)

    async def hold_approval_lock(
        dispatcher: AsyncWorkerDispatcher,
        request: InvokeWorkerRPCRequest,
        is_active: Callable[[], bool] | None = None,
    ) -> InvokeWorkerRPCResponse:
        return await gate.hold_lock(dispatcher, request, is_active)

    monkeypatch.setattr(AsyncWorkerDispatcher, "invoke_rpc", hold_approval_lock)
    winner = asyncio.create_task(
        _get_json(http_client, "decide", {"flowId": flow_id, **APPROVAL})
    )
    try:
        await asyncio.wait_for(gate.lock_acquired.wait(), timeout=5)
        refused = await asyncio.wait_for(
            _get_json(
                http_client, "decide", {"flowId": flow_id, **APPROVAL}, expected_status=409
            ),
            timeout=5,
        )
        assert refused == {"error": "another decision is changing this refund; try again"}
    finally:
        gate.release_lock.set()
    assert await winner == {"flowID": flow_id, "decision": "approve"}
    await client.wait_for_flow(flow_id, WAIT_TIMEOUT)


@pytest.mark.parametrize(
    ("scenario", "outcome", "simulate_closing"),
    [("small", "refunded", False), ("deny", "denied", False), ("deny", "denied", True)],
)
async def test_start_returns_a_flow_id_when_the_run_closes_before_the_policy_wait(
    app: ExampleApp,
    client: AsyncClient,
    monkeypatch: pytest.MonkeyPatch,
    scenario: str,
    outcome: str,
    simulate_closing: bool,
) -> None:
    http_client = create_app(app).test_client()
    wait_for_step = client.wait_for_step_completion

    async def wait_for_policy_after_close(
        flow_id: str, step: StepExecutionId, options: WaitForStepCompletionOptions
    ) -> None:
        await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        if simulate_closing:
            raise FlowClosingError(
                grpc.StatusCode.FAILED_PRECONDITION,
                ErrorSubStatus.FLOW_CLOSING,
                "Flow is closing",
                "wait_for_step_completion",
                flow_id,
            )
        await wait_for_step(flow_id, step, options)

    monkeypatch.setattr(client, "wait_for_step_completion", wait_for_policy_after_close)
    started = await _get_json(http_client, "start", {"flowId": "", "scenario": scenario})
    assert started.keys() == {"flowID", "runID"}
    flow_id = started["flowID"]
    assert flow_id.startswith("customer-refund-")
    assert started["runID"]
    refunds = [
        refund
        for refund in FakeBillingService(app.config.customer_refund_ledger_path).list_refunds()
        if refund.idempotency_key == f"refund-{flow_id}"
    ]
    assert len(refunds) == (1 if scenario == "small" else 0)
    if refunds:
        assert refunds[0].refund_id.startswith("rf_")
    closed = await _get_json(http_client, "describe", {"flowId": flow_id})
    assert closed == {
        "flowID": flow_id,
        "status": "completed",
        "caseStatus": f"closed-{outcome}",
        "refundId": refunds[0].refund_id if refunds else None,
    }


async def test_start_refuses_an_unknown_scenario(app: ExampleApp) -> None:
    refused = await _get_json(
        create_app(app).test_client(), "start", {"scenario": "everything"}, expected_status=400
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


async def test_duplicate_start_returns_a_json_conflict(
    app: ExampleApp, new_flow_id: Callable[[str], str]
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("customer-refund")
    await _start_refund(http_client, flow_id, "normal")
    refused = await _get_json(
        http_client, "start", {"workflowId": flow_id}, expected_status=409
    )
    assert refused.keys() == {"error"}
    assert refused["error"]


@pytest.mark.parametrize(("route", "query"), [("describe", {}), ("decide", APPROVAL)])
async def test_refund_lookup_validates_the_flow_id_and_type(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    route: str,
    query: dict[str, str],
) -> None:
    http_client = create_app(app).test_client()
    missing = await _get_json(http_client, route, query, expected_status=400)
    assert missing == {"error": "workflowId is required"}
    unknown = await _get_json(
        http_client, route, {"flowId": new_flow_id("unknown"), **query}, expected_status=404
    )
    assert unknown.keys() == {"error"}
    assert unknown["error"]

    other_flow_id = new_flow_id("channel")
    await client.start_flow(app.channel, other_flow_id, 5, start_options())
    wrong_type = await _get_json(
        http_client, route, {"flowId": other_flow_id, **query}, expected_status=404
    )
    assert wrong_type == {"error": "no refund Flow with this ID"}
