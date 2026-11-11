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

"""Runs the Quick Start against a real Dex server.

Point DEX_FLOW_SERVICE_ADDRESS at the server (default 127.0.0.1:8801). Without
a server the test fails, unless DEX_EXAMPLES_ALLOW_SKIP=1 asks it to skip.
"""

from __future__ import annotations

import os
import threading
import time
import uuid
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import timedelta
from pathlib import Path

import pytest
from dex import (
    Client,
    ClientOptions,
    DexServiceError,
    FlowStatus,
    Registry,
    StepExecutionId,
    TimerId,
    WaitForStepCompletionOptions,
    Worker,
    WorkerOptions,
)

from quick_start.config import SERVER_ADDRESS, open_cache
from quick_start.order_flow import SHIP_DELAY_TIMER, OrderFlow
from quick_start.shop_api import ShopApi

WAIT_TIMEOUT = timedelta(seconds=60)


@contextmanager
def running_worker(order_flow: OrderFlow, cache_dir: Path) -> Iterator[None]:
    cache = open_cache(cache_dir)
    worker = Worker(
        Registry((order_flow,)), cache, WorkerOptions(server_address=SERVER_ADDRESS)
    )
    thread = threading.Thread(target=worker.start, daemon=True)
    try:
        thread.start()
        worker.wait_pull_registered(timeout=30)
        yield
    finally:
        worker.close()
        thread.join(timeout=10)
        cache.close()


@contextmanager
def connected_client(order_flow: OrderFlow, cache_dir: Path) -> Iterator[Client]:
    cache = open_cache(cache_dir)
    client = Client(
        Registry((order_flow,)), cache, ClientOptions(server_address=SERVER_ADDRESS)
    )
    try:
        if not _is_server_ready(client):
            message = f"no Dex server at {SERVER_ADDRESS}; set DEX_FLOW_SERVICE_ADDRESS"
            if os.environ.get("DEX_EXAMPLES_ALLOW_SKIP") == "1":
                pytest.skip(message)
            pytest.fail(message)
        yield client
    finally:
        client.close()
        cache.close()


def test_restarted_worker_ships_without_charging_again(tmp_path: Path) -> None:
    order_id = f"order-{uuid.uuid4().hex}"
    first_shop = ShopApi()
    second_shop = ShopApi()
    client_flow = OrderFlow(ShopApi())
    with connected_client(client_flow, tmp_path / "client") as client:
        with running_worker(OrderFlow(first_shop), tmp_path / "worker-1"):
            client.start_flow(client_flow, order_id, order_id)
            client.wait_for_step_completion(
                order_id,
                StepExecutionId("Charge"),
                WaitForStepCompletionOptions(request_timeout=WAIT_TIMEOUT),
            )
        assert first_shop.charged == [order_id]
        assert first_shop.shipped == []

        with running_worker(OrderFlow(second_shop), tmp_path / "worker-2"):
            _skip_ship_delay(client, order_id)
            result = client.wait_for_flow(order_id, WAIT_TIMEOUT)

    assert result.status == FlowStatus.COMPLETED
    assert second_shop.charged == []
    assert second_shop.shipped == [order_id]


def _is_server_ready(client: Client) -> bool:
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        try:
            if client.health_check():
                return True
        except DexServiceError:
            pass
        time.sleep(0.25)
    return False


def _skip_ship_delay(client: Client, order_id: str) -> None:
    """Skips the 30-second Timer once Ship waits on it."""
    deadline = time.monotonic() + WAIT_TIMEOUT.total_seconds()
    while True:
        try:
            client.skip_timer(
                order_id,
                StepExecutionId("Ship"),
                TimerId.by_condition_id(SHIP_DELAY_TIMER),
            )
            return
        except DexServiceError:
            if time.monotonic() >= deadline:
                raise
            time.sleep(0.25)
