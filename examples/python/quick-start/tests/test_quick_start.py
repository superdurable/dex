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

import json
import os
import shutil
import subprocess
import threading
import urllib.request
from http.server import ThreadingHTTPServer
import time
import uuid
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import datetime, timedelta
from pathlib import Path

import pytest
from dex import (
    Client,
    ClientOptions,
    DexServiceError,
    FlowDefinitionDocument,
    FlowDefinitionUploadOutcome,
    FlowStatus,
    Registry,
    StepExecutionId,
    TimerId,
    WaitForStepCompletionOptions,
    Worker,
    WorkerOptions,
)

from quick_start import order_flow as order_flow_module
from quick_start.config import (
    FLOW_DEFINITIONS_DIR,
    SERVER_ADDRESS,
    open_cache,
    worker_options,
)
from quick_start.order_flow import SHIP_DELAY_TIMER, OrderFlow
from quick_start.shop_api import ShopApi
from service import new_handler

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
        assert _calls_for(order_id, first_shop.charged) == [order_id]
        assert _calls_for(order_id, first_shop.shipped) == []

        with running_worker(OrderFlow(second_shop), tmp_path / "worker-2"):
            _skip_ship_delay(client, order_id)
            result = client.wait_for_flow(order_id, WAIT_TIMEOUT)

    assert result.status == FlowStatus.COMPLETED
    assert _calls_for(order_id, second_shop.charged) == []
    assert _calls_for(order_id, second_shop.shipped) == [order_id]


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
    """Skips the 60-second Timer once Ship waits on it."""
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


class OrderFlowForDesignCheck(OrderFlow):
    """OrderFlow under a Flow type that no other run uses."""

    def __init__(self, shop: ShopApi, flow_type: str) -> None:
        super().__init__(shop)
        self.flow_type = flow_type

    def get_flow_type(self) -> str:
        return self.flow_type


def test_worker_uploads_the_generated_design(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    dexcli = os.environ.get("DEXCLI_PATH") or shutil.which("dexcli")
    if dexcli is None:
        pytest.fail("set DEXCLI_PATH or put dexcli on PATH")
    source = Path(order_flow_module.__file__).resolve()
    monkeypatch.chdir(tmp_path)
    subprocess.run(
        [
            dexcli,
            "visualize",
            str(source),
            "--schema-version",
            "2.0",
            "--json",
            "--out",
            "build/flow-definitions/order",
        ],
        check=True,
        capture_output=True,
    )
    # A Flow type of its own keeps earlier uploads on a shared server out of the result.
    flow_type = f"OrderFlowDesignCheck{uuid.uuid4().hex}"
    design_path = FLOW_DEFINITIONS_DIR / "order.json"
    document = json.loads(design_path.read_text())
    document["flow"]["name"] = flow_type
    design_path.write_text(json.dumps(document))
    design = design_path.read_bytes()
    generated_at = datetime.fromisoformat(document["generatedAt"])

    client_flow = OrderFlow(ShopApi())
    with connected_client(client_flow, tmp_path / "client") as client:
        cache = open_cache(tmp_path / "worker")
        design_check_flow = OrderFlowForDesignCheck(ShopApi(), flow_type)
        worker = Worker(Registry((design_check_flow,)), cache, worker_options())
        thread = threading.Thread(target=worker.start, daemon=True)
        try:
            thread.start()
            worker.wait_pull_registered(timeout=30)
        finally:
            worker.close()
            thread.join(timeout=10)
            cache.close()
        results = client.upload_flow_definitions(
            [FlowDefinitionDocument(flow_type, design, generated_at)]
        )

    assert [result.outcome for result in results] == [FlowDefinitionUploadOutcome.UNCHANGED]


def test_shop_api_drops_a_repeated_key_across_processes(tmp_path: Path) -> None:
    keys_path = tmp_path / "keys"
    first_process = ShopApi(keys_path)
    second_process = ShopApi(keys_path)

    first_process.charge("order-1", "charge-order-1")
    second_process.charge("order-1", "charge-order-1")

    assert first_process.charged == ["order-1"]
    assert second_process.charged == []
    assert second_process.repeated_keys == ["charge-order-1"]


def test_service_starts_one_flow_for_a_repeated_request(tmp_path: Path) -> None:
    order_id = f"order-{uuid.uuid4().hex}"
    shop = ShopApi()
    client_flow = OrderFlow(ShopApi())
    with connected_client(client_flow, tmp_path / "client") as client:
        with running_worker(OrderFlow(shop), tmp_path / "worker"):
            server = ThreadingHTTPServer(("127.0.0.1", 0), new_handler(client, client_flow))
            server_thread = threading.Thread(target=server.serve_forever, daemon=True)
            server_thread.start()
            try:
                url = f"http://127.0.0.1:{server.server_address[1]}/orders"
                replies = [_post_order(url, order_id) for _ in range(2)]
            finally:
                server.shutdown()
                server.server_close()
            _skip_ship_delay(client, order_id)
            client.wait_for_flow(order_id, WAIT_TIMEOUT)

    assert replies == [{"flowId": order_id, "charged": True}] * 2
    assert _calls_for(order_id, shop.charged) == [order_id]


def _post_order(url: str, order_id: str) -> dict[str, object]:
    request = urllib.request.Request(
        url,
        data=json.dumps({"orderId": order_id}).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=60) as response:
        reply: dict[str, object] = json.loads(response.read())
        return reply


def _calls_for(order_id: str, calls: list[str]) -> list[str]:
    """Keeps this test's order; a Flow left open by an earlier run can also reach a Worker."""
    return [call for call in calls if call == order_id]
