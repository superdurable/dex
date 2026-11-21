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

"""Settings shared by the refund Worker, the start and decide commands, and the tests."""

from __future__ import annotations

import os
import secrets
from pathlib import Path

from dex import (
    AesGcmPayloadCodec,
    BlobCache,
    BlobCacheConfig,
    FilePayloadEncryptionKeyProvider,
    PayloadEncryptionOptions,
    open_blob_cache,
)

from dex_examples.products.customer_refund.billing_service import (
    CallMissingBillingService,
    FakeBillingService,
)
from dex_examples.products.customer_refund.email_service import FakeEmailService
from dex_examples.products.customer_refund.ledger_database import DEFAULT_LEDGER_DATABASE
from dex_examples.products.customer_refund.message_model import MockMessageModel
from dex_examples.products.customer_refund.refund_flow import CustomerRefundFlow

SERVER_ADDRESS = os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801")
PAYLOAD_KEY_FILE = os.environ.get("REFUND_PAYLOAD_KEY_FILE", "")
BILLING_MODE = os.environ.get("REFUND_BILLING_MODE", "normal")
FLOW_DEFINITIONS_DIR = Path("build/customer-refund")


def payload_encryption(key_file: str = PAYLOAD_KEY_FILE) -> PayloadEncryptionOptions | None:
    """Returns AES-GCM payload encryption with the key file, or None without one."""
    if not key_file:
        return None
    key_provider = FilePayloadEncryptionKeyProvider(key_file)
    return PayloadEncryptionOptions(AesGcmPayloadCodec(key_provider))


def new_refund_flow(
    billing_mode: str = BILLING_MODE,
    ledger_path: Path = DEFAULT_LEDGER_DATABASE,
) -> CustomerRefundFlow:
    """Returns the Flow; billing mode "call-missing" leaves out the billing call."""
    if billing_mode not in ("normal", "call-missing"):
        raise ValueError(
            f"REFUND_BILLING_MODE must be normal or call-missing, not {billing_mode!r}"
        )
    if billing_mode == "call-missing":
        billing: FakeBillingService = CallMissingBillingService(ledger_path)
    else:
        billing = FakeBillingService(ledger_path)
    return CustomerRefundFlow(billing, FakeEmailService(ledger_path), MockMessageModel())


def new_worker_id() -> str:
    """Returns a Worker ID without the host name; it changes on every start."""
    return f"refund-worker-{secrets.token_hex(3)}"


def open_cache(directory: Path) -> BlobCache:
    """Opens a local cache for large Flow values, up to 1 GiB."""
    directory.mkdir(parents=True, exist_ok=True)
    return open_blob_cache(BlobCacheConfig(str(directory), 1 << 30))
