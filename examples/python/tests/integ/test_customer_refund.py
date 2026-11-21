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

import os
import shutil
import subprocess
import threading
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from datetime import timedelta
from pathlib import Path

import pytest
from dex import (
    AttributeMatch,
    Client,
    ClientOptions,
    FlowStatus,
    PayloadEncryptionOptions,
    Registry,
    ValueMappingError,
    WaitForAttributeOptions,
    Worker,
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
        self.billing = FakeBillingService(directory / "billing-refunds.json")
        self.email = FakeEmailService(directory / "sent-emails.json")
        self.flow = self.new_flow("normal")
        self.cache = open_cache(directory / "client-cache")
        self.client = Client(
            Registry((self.flow,)),
            self.cache,
            ClientOptions(server_address=server_address(), payload_encryption=encryption),
        )

    def new_flow(self, billing_mode: str) -> CustomerRefundFlow:
        return new_refund_flow(billing_mode, self.billing.ledger_path, self.email.ledger_path)

    @contextmanager
    def running_worker(self, billing_mode: str = "normal") -> Iterator[None]:
        cache = open_cache(self.directory / f"worker-cache-{billing_mode}")
        options = refund_worker_options(server_address(), self.encryption)
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
        testbed.client.start_flow(testbed.flow, flow_id, SCENARIOS["normal"])
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
        testbed.client.start_flow(testbed.flow, flow_id, SCENARIOS["normal"])
        testbed.wait_for_manager(flow_id)
        testbed.client.invoke_rpc(
            testbed.flow.reject_refund, flow_id, DecisionReason("Credit offered instead")
        )
        result = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert result.single_output(str) == "denied"
    assert testbed.billing.list_refunds() == []
    message = testbed.client.get_attributes(flow_id, CUSTOMER_MESSAGE)[CUSTOMER_MESSAGE]
    assert "Credit offered instead" in message

def test_policy_denies_an_old_charge(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        testbed.client.start_flow(testbed.flow, flow_id, SCENARIOS["deny"])
        result = testbed.client.wait_for_flow(flow_id, WAIT_TIMEOUT)

    assert result.single_output(str) == "denied"
    assert testbed.client.get_attributes(flow_id, CASE_STATUS)[CASE_STATUS] == "closed-denied"
    assert testbed.billing.list_refunds() == []
    assert testbed.email.list_sent_keys() == [f"email-{flow_id}"]

def test_refund_succeeds_after_payment_timeouts(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker():
        testbed.client.start_flow(testbed.flow, flow_id, SCENARIOS["fail-then-succeed"])
        testbed.wait_for_manager(flow_id)
        testbed.client.invoke_rpc(
            testbed.flow.approve_refund, flow_id, DecisionReason("Outage confirmed")
        )
        result = testbed.client.wait_for_flow(flow_id, timedelta(seconds=90))

    assert result.status == FlowStatus.COMPLETED
    assert len(testbed.billing.list_refunds()) == 1

def test_time_travel_issues_the_refund_a_missing_call_skipped(
    testbed: RefundTestbed, new_flow_id: Callable[[str], str]
) -> None:
    flow_id = new_flow_id("refund")
    with testbed.running_worker("call-missing"):
        testbed.client.start_flow(testbed.flow, flow_id, SCENARIOS["normal"])
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
            encrypted.client.start_flow(encrypted.flow, flow_id, SCENARIOS["small"])
            encrypted.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        message = encrypted.client.get_attributes(flow_id, CUSTOMER_MESSAGE)[CUSTOMER_MESSAGE]
        assert "refunded 49.00 USD" in message

        assert plain.client.get_attributes(flow_id, CASE_STATUS)[CASE_STATUS] == "closed-refunded"
        with pytest.raises(ValueMappingError, match="configure a PayloadCodec"):
            plain.client.get_attributes(flow_id, CUSTOMER_MESSAGE)
    finally:
        plain.close()
        encrypted.close()

def time_travel_with_dexcli(flow_id: str, step_type: str) -> None:
    """Runs the README's time travel command."""
    dexcli = os.environ.get("DEXCLI_PATH") or shutil.which("dexcli")
    if dexcli is None:
        pytest.fail("set DEXCLI_PATH or put dexcli on PATH")
    subprocess.run(
        [
            dexcli, "flow", "time-travel", flow_id,
            "--server", server_address(),
            "--type", "step-type", "--target", step_type,
            "--reason", "billing call restored", "--yes",
        ],
        check=True,
        capture_output=True,
    )
