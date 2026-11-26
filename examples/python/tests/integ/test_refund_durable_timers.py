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

"""Refund with durable Timers: one scenario per tutorial lesson, Timers fired with SkipTimer."""

from __future__ import annotations

import asyncio
from datetime import timedelta
from typing import Any, Callable

import pytest
from dex import (
    AsyncClient,
    AttributeMatch,
    Context,
    FlowStatus,
    RpcLockConflictError,
    Step,
    StepExecutionId,
    WaitForAttributeOptions,
    WorkerInvocationError,
)

from dex_examples.app import ExampleApp
from dex_examples.products.refund_durable_timers.bank_settlement_service import (
    SettlementState,
)
from dex_examples.products.refund_durable_timers.payment_provider import derive_refund_id
from dex_examples.products.refund_durable_timers.refund_timers_flow import (
    AWAITING_MANAGER,
    CASE_STATUS,
    CLOSING_INACTIVE,
    MAX_SETTLEMENT_HEARTBEAT_CHECKS,
    MAX_SETTLEMENT_POLLS,
    REFUND_ID,
    REMINDERS_SENT,
    CustomerMessage,
    DecisionReason,
    RefundSimulation,
    SettlementPolling,
    refund_start_options,
)
from dex_examples.products.refund_durable_timers.skip_timer import skip_refund_timer
from dex_examples.products.refund_durable_timers.start import new_refund_request
from tests.integ.conftest import LONG_WAIT_TIMEOUT, WAIT_TIMEOUT

pytestmark = pytest.mark.integ

REFUND_AMOUNT_CENTS = new_refund_request(SettlementPolling.TIMER, RefundSimulation()).amount_cents
DOCUMENTS = CustomerMessage("Attached: bank statement and invoice.", documents_complete=True)


async def start_refund(
    app: ExampleApp,
    client: AsyncClient,
    flow_id: str,
    polling: SettlementPolling = SettlementPolling.TIMER,
    simulation: RefundSimulation = RefundSimulation(),
) -> None:
    request = new_refund_request(polling, simulation)
    await client.start_flow(
        app.refund_durable_timers, flow_id, request, refund_start_options()
    )


async def wait_for_case_status(client: AsyncClient, flow_id: str, status: str) -> None:
    await client.wait_for_attribute_match(
        flow_id,
        CASE_STATUS,
        AttributeMatch.equal_to(status),
        WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
    )


async def send_documents_and_approve(app: ExampleApp, client: AsyncClient, flow_id: str) -> None:
    flow = app.refund_durable_timers
    await wait_for_case_status(client, flow_id, "awaiting-documents")
    await client.invoke_rpc(flow.send_customer_message, flow_id, DOCUMENTS)
    await wait_for_case_status(client, flow_id, AWAITING_MANAGER)
    await client.invoke_rpc(flow.approve_refund, flow_id, DecisionReason("Receipts match"))


async def completed_output(
    client: AsyncClient, flow_id: str, timeout: timedelta = WAIT_TIMEOUT
) -> str:
    result = await client.wait_for_flow(flow_id, timeout)
    assert result.status is FlowStatus.COMPLETED, result.error_message
    outputs = [completion.decode(str) for completion in result.completions]
    assert len(outputs) == 1, outputs
    return outputs[0]


async def test_lesson1_reminds_the_manager_until_the_decision(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow = app.refund_durable_timers
    flow_id = new_flow_id("refund-timers-reminder")
    await start_refund(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-documents")
    await client.invoke_rpc(flow.send_customer_message, flow_id, DOCUMENTS)

    first = await skip_refund_timer(client, flow_id, "manager-reminder")
    second = await skip_refund_timer(client, flow_id, "manager-reminder")
    assert (first.number, second.number) == (1, 2)
    await client.invoke_rpc(flow.approve_refund, flow_id, DecisionReason("Receipts match"))

    assert await completed_output(client, flow_id) == "refunded"
    values = await client.get_attributes(flow_id, REMINDERS_SENT, CASE_STATUS)
    assert values.get(REMINDERS_SENT) == 2
    assert values.get(CASE_STATUS) == "closed-refunded"
    assert len(app.refund_timers_notifications.list_sent(f"reminder-{flow_id}-")) == 2


async def test_lesson1_rejection_closes_the_case(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow = app.refund_durable_timers
    flow_id = new_flow_id("refund-timers-reject")
    await start_refund(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-documents")
    with pytest.raises(WorkerInvocationError, match="not waiting for a manager"):
        await client.invoke_rpc(flow.approve_refund, flow_id, DecisionReason("too early"))
    await client.invoke_rpc(flow.send_customer_message, flow_id, DOCUMENTS)
    await wait_for_case_status(client, flow_id, AWAITING_MANAGER)
    # Each run sends its own first reminder, even after another run sent one.
    await skip_refund_timer(client, flow_id, "manager-reminder")
    await client.invoke_rpc(flow.reject_refund, flow_id, DecisionReason("Outside policy"))

    assert await completed_output(client, flow_id) == "rejected"
    assert len(app.refund_timers_notifications.list_sent(f"reminder-{flow_id}-")) == 1


async def test_lesson2_each_message_restarts_the_idle_timer(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow = app.refund_durable_timers
    flow_id = new_flow_id("refund-timers-idle")
    await start_refund(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-documents")
    await client.invoke_rpc(
        flow.send_customer_message, flow_id, CustomerMessage("I will find the invoice.")
    )

    execution = await skip_refund_timer(client, flow_id, "customer-idle")
    # The message ended execution 1; execution 2 waited on a fresh 3-day Timer.
    assert execution == StepExecutionId("CollectSupportingDocuments", 2)
    assert await completed_output(client, flow_id) == "closed-inactive"
    closed = app.refund_timers_notifications.list_sent(f"closed-inactive-{flow_id}")
    assert len(closed) == 1


async def test_lesson3_backs_off_then_honours_retry_after(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund-timers-retry")
    simulation = RefundSimulation(transient_payment_failures=2, rate_limit_retry_after_seconds=12)
    await start_refund(app, client, flow_id, simulation=simulation)
    await send_documents_and_approve(app, client, flow_id)

    assert await completed_output(client, flow_id) == "refunded"
    attempts = app.refund_timers_payment.list_attempts(f"refund-{flow_id}")
    assert [item.outcome for item in attempts] == [
        "unavailable",
        "unavailable",
        "rate-limited",
        "refunded",
    ]
    gaps = [later.arrived_at - earlier.arrived_at for earlier, later in zip(attempts, attempts[1:])]
    # Backoff waits 2 then 4 seconds; Retry-After replaces the next 8-second backoff with 12.
    assert gaps[0] >= 1.5
    assert gaps[1] >= 3.5
    assert gaps[2] >= 11.5
    refund_id = (await client.get_attributes(flow_id, REFUND_ID)).get(REFUND_ID)
    assert refund_id and refund_id.startswith("rf_")


async def test_lesson4a_polls_with_a_durable_timer(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund-timers-poll-timer")
    simulation = RefundSimulation(settle_after_checks=3)
    await start_refund(app, client, flow_id, SettlementPolling.TIMER, simulation)
    await send_documents_and_approve(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-settlement")

    second = await skip_refund_timer(client, flow_id, "settlement-poll")
    third = await skip_refund_timer(client, flow_id, "settlement-poll")
    assert (second.number, third.number) == (2, 3)

    assert await completed_output(client, flow_id) == "refunded"
    refund_id = (await client.get_attributes(flow_id, REFUND_ID)).get(REFUND_ID)
    assert refund_id
    checks = app.refund_timers_bank.list_checks(refund_id)
    assert [check.check_number for check in checks] == [1, 2, 3]


async def test_lesson4a_escalates_when_the_bank_never_settles(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund-timers-poll-never")
    simulation = RefundSimulation(bank_never_settles=True)
    await start_refund(app, client, flow_id, SettlementPolling.TIMER, simulation)
    await send_documents_and_approve(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-settlement")

    for _ in range(MAX_SETTLEMENT_POLLS - 1):
        await skip_refund_timer(client, flow_id, "settlement-poll")

    assert await completed_output(client, flow_id) == "escalated-unsettled"
    escalations = app.refund_timers_notifications.list_sent(f"escalate-settlement-{flow_id}")
    assert len(escalations) == 1


async def test_lesson4b_polls_in_one_heartbeating_step(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund-timers-poll-heartbeat")
    simulation = RefundSimulation(settle_after_checks=2)
    await start_refund(app, client, flow_id, SettlementPolling.HEARTBEAT, simulation)
    await send_documents_and_approve(app, client, flow_id)

    assert await completed_output(client, flow_id) == "refunded"
    refund_id = (await client.get_attributes(flow_id, REFUND_ID)).get(REFUND_ID)
    assert refund_id
    checks = app.refund_timers_bank.list_checks(refund_id)
    assert [check.check_number for check in checks] == [1, 2]


async def test_lesson4b_escalates_after_the_heartbeat_check_limit(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund-timers-heartbeat-never")
    simulation = RefundSimulation(bank_never_settles=True, heartbeat_check_interval_seconds=1)
    await start_refund(app, client, flow_id, SettlementPolling.HEARTBEAT, simulation)
    await send_documents_and_approve(app, client, flow_id)

    assert await completed_output(client, flow_id, LONG_WAIT_TIMEOUT) == "escalated-unsettled"
    refund_id = derive_refund_id(f"refund-{flow_id}", REFUND_AMOUNT_CENTS)
    checks = app.refund_timers_bank.list_checks(refund_id)
    assert [check.check_number for check in checks] == list(
        range(1, MAX_SETTLEMENT_HEARTBEAT_CHECKS + 1)
    )


async def test_lesson4b_retry_continues_from_the_heartbeat_checkpoint(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    flow_id = new_flow_id("refund-timers-heartbeat-resume")
    refund_id = derive_refund_id(f"refund-{flow_id}", REFUND_AMOUNT_CENTS)
    bank = app.refund_timers_bank
    original_get_settlement_state = bank.get_settlement_state
    failed_checks: list[int] = []

    def fail_first_second_check(
        checked_refund_id: str, check_number: int, simulation: RefundSimulation
    ) -> SettlementState:
        # Attempt 1 heartbeats check 1, then fails on check 2.
        if checked_refund_id == refund_id and check_number == 2 and not failed_checks:
            failed_checks.append(check_number)
            raise ConnectionError("bank connection reset")
        return original_get_settlement_state(checked_refund_id, check_number, simulation)

    monkeypatch.setattr(bank, "get_settlement_state", fail_first_second_check)
    simulation = RefundSimulation(settle_after_checks=3, heartbeat_check_interval_seconds=1)
    await start_refund(app, client, flow_id, SettlementPolling.HEARTBEAT, simulation)
    await send_documents_and_approve(app, client, flow_id)

    assert await completed_output(client, flow_id) == "refunded"
    assert failed_checks == [2]
    # Attempt 2 starts at check 2, not check 1.
    assert [check.check_number for check in bank.list_checks(refund_id)] == [1, 2, 3]


class FirstExecutionGate:
    """Holds the first execution of one Step in one Flow until the test releases it."""

    def __init__(self, flow_id: str, step: Step[Any]) -> None:
        self.flow_id = flow_id
        self.entered = asyncio.Event()
        self.released = asyncio.Event()
        self._original_execute = step.execute

    def execute(self, context: Context, input: Any) -> Any:
        if context.flow_id != self.flow_id or self.entered.is_set():
            return self._original_execute(context, input)
        self.entered.set()
        return self._execute_after_release(context, input)

    async def _execute_after_release(self, context: Context, input: Any) -> Any:
        await asyncio.wait_for(self.released.wait(), WAIT_TIMEOUT.total_seconds())
        return self._original_execute(context, input)


async def test_rpc_cannot_slip_in_while_a_step_changes_the_stage(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    flow = app.refund_durable_timers
    flow_id = new_flow_id("refund-timers-stage-lock")
    gate = FirstExecutionGate(flow_id, flow.collect_supporting_documents)
    monkeypatch.setattr(flow.collect_supporting_documents, "execute", gate.execute)
    await start_refund(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-documents")
    await client.invoke_rpc(flow.send_customer_message, flow_id, DOCUMENTS)
    await asyncio.wait_for(gate.entered.wait(), WAIT_TIMEOUT.total_seconds())

    late_message = CustomerMessage("One more receipt.")
    try:
        # The Step holds the case-status lock while it moves the case to the manager.
        with pytest.raises(RpcLockConflictError):
            await client.invoke_rpc(flow.send_customer_message, flow_id, late_message)
    finally:
        gate.released.set()
    await wait_for_case_status(client, flow_id, AWAITING_MANAGER)
    with pytest.raises(WorkerInvocationError, match="not collecting documents"):
        await client.invoke_rpc(flow.send_customer_message, flow_id, late_message)

    await client.invoke_rpc(flow.approve_refund, flow_id, DecisionReason("Receipts match"))
    assert await completed_output(client, flow_id) == "refunded"


async def test_idle_close_leaves_the_documents_stage_before_the_handoff(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    flow = app.refund_durable_timers
    flow_id = new_flow_id("refund-timers-idle-handoff")
    gate = FirstExecutionGate(flow_id, flow.close_inactive_case)
    monkeypatch.setattr(flow.close_inactive_case, "execute", gate.execute)
    await start_refund(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-documents")
    await skip_refund_timer(client, flow_id, "customer-idle")
    await asyncio.wait_for(gate.entered.wait(), WAIT_TIMEOUT.total_seconds())

    try:
        # CollectSupportingDocuments committed the stage change before CloseInactiveCase ran.
        status = (await client.get_attributes(flow_id, CASE_STATUS)).get(CASE_STATUS)
        assert status == CLOSING_INACTIVE
        with pytest.raises((RpcLockConflictError, WorkerInvocationError)):
            await client.invoke_rpc(
                flow.send_customer_message, flow_id, CustomerMessage("Here it is.")
            )
    finally:
        gate.released.set()

    assert await completed_output(client, flow_id) == "closed-inactive"
    status = (await client.get_attributes(flow_id, CASE_STATUS)).get(CASE_STATUS)
    assert status == "closed-inactive"


async def test_closing_email_step_does_not_hold_the_status_lock(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    flow = app.refund_durable_timers
    flow_id = new_flow_id("refund-timers-unlocked-email")
    gate = FirstExecutionGate(flow_id, flow.send_closing_notification)
    monkeypatch.setattr(flow.send_closing_notification, "execute", gate.execute)
    await start_refund(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-documents")
    await skip_refund_timer(client, flow_id, "customer-idle")
    await asyncio.wait_for(gate.entered.wait(), WAIT_TIMEOUT.total_seconds())

    try:
        # The decision is committed; the RPC fails its stage check, not on the lock.
        await wait_for_case_status(client, flow_id, "closed-inactive")
        with pytest.raises(WorkerInvocationError, match="not collecting documents"):
            await client.invoke_rpc(
                flow.send_customer_message, flow_id, CustomerMessage("Here it is.")
            )
    finally:
        gate.released.set()

    assert await completed_output(client, flow_id) == "closed-inactive"
    closed = app.refund_timers_notifications.list_sent(f"closed-inactive-{flow_id}")
    assert len(closed) == 1


async def test_lesson5_timeout_handler_escalates_the_case(
    app: ExampleApp, client: AsyncClient, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund-timers-deadline")
    await start_refund(app, client, flow_id)
    await wait_for_case_status(client, flow_id, "awaiting-documents")

    await skip_refund_timer(client, flow_id, "refund-deadline")

    assert await completed_output(client, flow_id) == "escalated-deadline"
    status = (await client.get_attributes(flow_id, CASE_STATUS)).get(CASE_STATUS)
    assert status == "escalated-deadline"
    escalations = app.refund_timers_notifications.list_sent(f"escalate-deadline-{flow_id}")
    assert len(escalations) == 1
