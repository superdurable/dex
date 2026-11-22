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

"""Runs the customer refund example's scenarios against a real Dex server."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import threading
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from dataclasses import replace
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest
from dex import (
    AttributeMatch,
    Client,
    ClientOptions,
    FlowStatus,
    PayloadEncryptionOptions,
    Registry,
    StartFlowOptions,
    ValueMappingError,
    WaitForAttributeOptions,
    Worker,
    WorkerInvocationError,
)

from dex_examples.products.customer_refund.billing_service import FakeBillingService
from dex_examples.products.customer_refund.email_service import FakeEmailService
from dex_examples.products.customer_refund.refund_flow import (
    AWAITING_MANAGER,
    CASE_STATUS,
    CUSTOMER_MESSAGE,
    REFUND_ID,
    CustomerRefundFlow,
    DecisionReason,
    RefundRequest,
)
from dex_examples.products.customer_refund.refund_settings import (
    new_refund_flow,
    open_cache,
    payload_encryption,
)
from dex_examples.products.customer_refund.start import SCENARIOS
from dex_examples.products.customer_refund.worker import refund_worker_options
from tests.integ.conftest import WAIT_TIMEOUT, server_address
from tests.integ.test_payload_encryption import write_payload_key_file

pytestmark = pytest.mark.integ


class RefundTestbed:
    """One test's ledgers, cache folder, and Client, shared by its Workers."""

    def __init__(self, directory: Path, encryption: PayloadEncryptionOptions | None) -> None:
        self.directory = directory
        self.encryption = encryption
        # The shared ExampleApp Worker also serves this Flow type on the default task queue.
        self.task_queue = f"customer-refund-test-{uuid4().hex[:12]}"
        self.start_options = StartFlowOptions(task_queue=self.task_queue)
        self.billing = FakeBillingService(directory / "ledger.sqlite3")
        self.email = FakeEmailService(directory / "ledger.sqlite3")
        self.flow = self.new_flow("normal")
        self.cache = open_cache(directory / "client-cache")
        self.client = Client(
            Registry((self.flow,)),
            self.cache,
            ClientOptions(server_address=server_address(), payload_encryption=encryption),
        )

    def new_flow(self, billing_mode: str) -> CustomerRefundFlow:
        return new_refund_flow(billing_mode, self.billing.ledger_path)

    @contextmanager
    def running_worker(self, billing_mode: str = "normal") -> Iterator[None]:
        cache = open_cache(self.directory / f"worker-cache-{billing_mode}")
        options = refund_worker_options(server_address(), self.encryption)
        options = replace(options, pull=replace(options.pull, task_queue=self.task_queue))
        worker = Worker(Registry((self.new_flow(billing_mode),)), cache, options)
        thread = threading.Thread(target=worker.start, daemon=True)
        try:
            thread.start()
            worker.wait_pull_registered(timeout=30)
            yield
        finally:
            worker.close()
            thread.join(timeout=10)
            cache.close()

    def start_refund(self, flow_id: str, request: RefundRequest) -> None:
        self.client.start_flow(self.flow, flow_id, request, self.start_options)

    def wait_for_manager(self, flow_id: str) -> None:
        self.client.wait_for_attribute_match(
            flow_id,
            CASE_STATUS,
            AttributeMatch.equal_to(AWAITING_MANAGER),
            WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
        )

    def close(self) -> None:
        self.client.close()
        self.cache.close()


@pytest.fixture()
def testbed(tmp_path: Path) -> Iterator[RefundTestbed]:
    bed = RefundTestbed(tmp_path, None)
    try:
        yield bed
    finally:
        bed.close()


def test_manager_approves_the_northstar_refund(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        testbed.start_refund(flow_id,SCENARIOS["normal"])
        testbed.wait_for_manager(flow_id)
        testbed.client.invoke_rpc(
            testbed.flow.approve_refund, flow_id, DecisionReason("Outage confirmed")
        )
        result = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert result.status == FlowStatus.COMPLETED
    assert result.single_output(str) == "refunded"
    refunds = testbed.billing.list_refunds()
    assert [(refund.order_id, refund.amount_usd) for refund in refunds] == [("INV-1249", 1249.00)]
    assert testbed.client.get_attributes(flow_id, REFUND_ID)[REFUND_ID] == refunds[0].refund_id
    assert testbed.email.list_sent_keys() == [f"email-{flow_id}"]


def test_manager_rejects_the_refund(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        testbed.start_refund(flow_id,SCENARIOS["normal"])
        testbed.wait_for_manager(flow_id)
        testbed.client.invoke_rpc(
            testbed.flow.reject_refund, flow_id, DecisionReason("Credit offered instead")
        )
        result = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert result.single_output(str) == "denied"
    assert testbed.billing.list_refunds() == []
    message = testbed.client.get_attributes(flow_id, CUSTOMER_MESSAGE)[CUSTOMER_MESSAGE]
    assert "Credit offered instead" in message


def test_blank_reason_is_refused_and_a_later_decision_still_counts(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        testbed.start_refund(flow_id,SCENARIOS["normal"])
        testbed.wait_for_manager(flow_id)
        with pytest.raises(WorkerInvocationError, match="a reason is required"):
            testbed.client.invoke_rpc(testbed.flow.approve_refund, flow_id, DecisionReason("   "))
        assert testbed.client.get_attributes(flow_id, CASE_STATUS)[CASE_STATUS] == AWAITING_MANAGER
        testbed.client.invoke_rpc(
            testbed.flow.approve_refund, flow_id, DecisionReason("Outage confirmed")
        )
        result = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert result.single_output(str) == "refunded"
    assert len(testbed.billing.list_refunds()) == 1


@pytest.mark.parametrize(
    ("order_id", "expected_note"),
    [
        ("INV-0388", "older than our 30-day refund window"),
        ("INV-0455", "no service interruption"),
    ],
)
def test_policy_denial_names_the_failed_rule(
    testbed: RefundTestbed,
    new_flow_id: Callable[[str], str],
    order_id: str,
    expected_note: str,
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        request = replace(SCENARIOS["normal"], order_id=order_id)
        testbed.start_refund(flow_id,request)
        result = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert result.single_output(str) == "denied"
    assert testbed.client.get_attributes(flow_id, CASE_STATUS)[CASE_STATUS] == "closed-denied"
    message = testbed.client.get_attributes(flow_id, CUSTOMER_MESSAGE)[CUSTOMER_MESSAGE]
    assert expected_note in message
    assert testbed.billing.list_refunds() == []
    assert testbed.email.list_sent_keys() == [f"email-{flow_id}"]


def test_refund_succeeds_after_payment_timeouts(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        testbed.start_refund(flow_id,SCENARIOS["fail-then-succeed"])
        testbed.wait_for_manager(flow_id)
        testbed.client.invoke_rpc(
            testbed.flow.approve_refund, flow_id, DecisionReason("Outage confirmed")
        )
        result = testbed.client.wait_for_flow(flow_id, timedelta(seconds=90))

    assert result.status == FlowStatus.COMPLETED
    assert len(testbed.billing.list_refunds()) == 1
    completed = issue_refund_execute_completed_context(flow_id)
    assert completed["finalAttempt"] == 3
    assert completed["lastFailureInfo"]["attempt"] == 2
    assert "PaymentTimeout" in json.dumps(completed["lastFailureInfo"])


def test_time_travel_reuses_an_issued_refund(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        testbed.start_refund(flow_id,SCENARIOS["small"])
        testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        first_refunds = testbed.billing.list_refunds()
        first_attempt = current_run_attempt(flow_id)
        new_attempt = time_travel_with_dexcli(flow_id, "IssueRefund")
        again = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert again.status == FlowStatus.COMPLETED
    assert new_attempt > first_attempt
    issue_refund_execute_completed_context(flow_id, new_attempt)
    assert len(first_refunds) == 1
    assert testbed.billing.list_refunds() == first_refunds
    refund_id = testbed.client.get_attributes(flow_id, REFUND_ID)[REFUND_ID]
    assert refund_id == first_refunds[0].refund_id
    assert testbed.email.list_sent_keys() == [f"email-{flow_id}"]


def test_time_travel_issues_the_refund_a_missing_call_skipped(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker("call-missing"):
        testbed.start_refund(flow_id,SCENARIOS["normal"])
        testbed.wait_for_manager(flow_id)
        testbed.client.invoke_rpc(
            testbed.flow.approve_refund, flow_id, DecisionReason("Outage confirmed")
        )
        broken = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert broken.single_output(str) == "refunded"
    assert testbed.client.get_attributes(flow_id, REFUND_ID)[REFUND_ID] == ""
    assert testbed.billing.list_refunds() == []

    with testbed.running_worker("normal"):
        time_travel_with_dexcli(flow_id, "IssueRefund")
        fixed = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert fixed.status == FlowStatus.COMPLETED
    refunds = testbed.billing.list_refunds()
    assert [refund.idempotency_key for refund in refunds] == [f"refund-{flow_id}"]
    assert testbed.client.get_attributes(flow_id, REFUND_ID)[REFUND_ID] == refunds[0].refund_id
    assert testbed.email.list_sent_keys() == [f"email-{flow_id}"]


def test_encryption_keeps_case_status_readable(
    tmp_path: Path, new_flow_id: Callable[[str], str]
) -> None:
    encrypted = RefundTestbed(tmp_path, payload_encryption(write_payload_key_file(tmp_path)))
    plain = RefundTestbed(tmp_path / "without-key", None)
    flow_id = new_flow_id("refund")
    try:
        with encrypted.running_worker():
            encrypted.start_refund(flow_id, SCENARIOS["small"])
            encrypted.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        message = encrypted.client.get_attributes(flow_id, CUSTOMER_MESSAGE)[CUSTOMER_MESSAGE]
        assert "refunded 49.00 USD" in message

        assert plain.client.get_attributes(flow_id, CASE_STATUS)[CASE_STATUS] == "closed-refunded"
        with pytest.raises(ValueMappingError, match="configure a PayloadCodec"):
            plain.client.get_attributes(flow_id, CUSTOMER_MESSAGE)
    finally:
        plain.close()
        encrypted.close()


def dexcli_path() -> str:
    dexcli = os.environ.get("DEXCLI_PATH") or shutil.which("dexcli")
    if dexcli is None:
        pytest.fail("set DEXCLI_PATH or put dexcli on PATH")
    return dexcli


def read_history_events(flow_id: str) -> list[dict[str, Any]]:
    """Reads every history event of the Flow's current run with dexcli."""
    history = subprocess.run(
        [dexcli_path(), "flow", "history", flow_id, "--server", server_address(), "--all"],
        check=True,
        capture_output=True,
        text=True,
    )
    events: list[dict[str, Any]] = json.loads(history.stdout)["events"]
    return events


def issue_refund_execute_completed_context(
    flow_id: str, run_attempt: int | None = None
) -> dict[str, Any]:
    """Reads the context of the one IssueRefund StepExecuteCompleted event, in one run attempt."""
    contexts = [
        event["payload"]["context"]
        for event in read_history_events(flow_id)
        if event["type"] == "StepExecuteCompleted"
        and event["payload"]["context"]["stepType"] == "IssueRefund"
        and run_attempt in (None, event["runAttempt"])
    ]
    assert len(contexts) == 1
    context: dict[str, Any] = contexts[0]
    return context


def current_run_attempt(flow_id: str) -> int:
    """Returns the run attempt of the Flow's latest history event."""
    run_attempt: int = read_history_events(flow_id)[-1]["runAttempt"]
    return run_attempt


def time_travel_with_dexcli(flow_id: str, step_type: str) -> int:
    """Runs the README's time travel command and returns the new run attempt."""
    completed = subprocess.run(
        [
            dexcli_path(),
            "flow",
            "time-travel",
            flow_id,
            "--server",
            server_address(),
            "--type",
            "step-type",
            "--target",
            step_type,
            "--reason",
            "billing call restored",
            "--yes",
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    run_attempt: int = json.loads(completed.stdout)["runAttempt"]
    return run_attempt
