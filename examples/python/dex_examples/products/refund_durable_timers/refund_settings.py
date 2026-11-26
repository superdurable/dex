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

"""Settings shared by the refund Worker and the presenter commands."""

from __future__ import annotations

import os
import secrets
from pathlib import Path

from dex import (
    AsyncClient,
    BlobCache,
    BlobCacheConfig,
    ClientOptions,
    Registry,
    open_blob_cache,
)

from dex_examples.products.refund_durable_timers.bank_settlement_service import (
    FakeBankSettlementService,
)
from dex_examples.products.refund_durable_timers.notification_service import (
    FakeNotificationService,
)
from dex_examples.products.refund_durable_timers.payment_provider import FakePaymentProvider
from dex_examples.products.refund_durable_timers.refund_timers_flow import (
    RefundDurableTimersFlow,
)

SERVER_ADDRESS = os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801")
FLOW_DEFINITIONS_DIR = Path("build/refund-durable-timers")


def new_refund_timers_flow() -> RefundDurableTimersFlow:
    """Returns the Flow with fresh in-process fakes."""
    return RefundDurableTimersFlow(
        FakePaymentProvider(), FakeBankSettlementService(), FakeNotificationService()
    )


def new_refund_registry(flow: RefundDurableTimersFlow) -> Registry:
    """Returns a Registry for the Flow; its settlement heartbeat Step is async."""
    return Registry((flow,), allow_async_handlers=True)


def new_worker_id() -> str:
    """Returns a Worker ID without the host name; it changes on every start."""
    return f"refund-timers-worker-{secrets.token_hex(3)}"


def open_cache(directory: Path) -> BlobCache:
    """Opens a local cache for large Flow values, up to 1 GiB."""
    directory.mkdir(parents=True, exist_ok=True)
    return open_blob_cache(BlobCacheConfig(str(directory), 1 << 30))


def new_presenter_client(flow: RefundDurableTimersFlow, cache: BlobCache) -> AsyncClient:
    """Returns a Client for the presenter commands; the caller closes it."""
    return AsyncClient(
        new_refund_registry(flow), cache, ClientOptions(server_address=SERVER_ADDRESS)
    )
