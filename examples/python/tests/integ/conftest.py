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

"""Shared harness for the example integration tests.

Every test in this package needs a running Dex server. Point
DEX_FLOW_SERVICE_ADDRESS at it (default 127.0.0.1:8801); when the server is
unreachable the whole package is skipped instead of failing.
"""

from __future__ import annotations

import os
import time
from datetime import timedelta
from typing import Any, Callable, Iterator
from uuid import uuid4

import pytest
from dex import Attribute, Client, DexException, ErrorSubStatus, FlowStatus

from dex_examples.app import ExampleApp
from dex_examples.config import ExamplesConfig

DEFAULT_SERVER_ADDRESS = "127.0.0.1:8801"

WAIT_TIMEOUT = timedelta(seconds=45)
# Flows that start child Flows inherit the child's random timer, up to a minute.
LONG_WAIT_TIMEOUT = timedelta(seconds=150)
SERVER_READY_TIMEOUT = timedelta(seconds=20)

POLL_INTERVAL_SECONDS = 0.5


def server_address() -> str:
    return os.environ.get("DEX_FLOW_SERVICE_ADDRESS", DEFAULT_SERVER_ADDRESS)


@pytest.fixture(scope="session")
def example_app(tmp_path_factory: pytest.TempPathFactory) -> Iterator[ExampleApp]:
    """Boots one ExampleApp with every Flow on an ephemeral Worker port."""
    config = ExamplesConfig(
        server_address=server_address(),
        # Port 0 lets the Worker's gRPC server pick a free port; the Client then
        # targets that exact Worker, so parallel sample apps cannot collide.
        worker_bind_address="127.0.0.1:0",
        worker_target=None,
        http_address="127.0.0.1:0",
        blob_cache_dir=tmp_path_factory.mktemp("dex-examples-blob-cache"),
    )
    app = ExampleApp(config)
    app.start_worker()
    if not server_is_ready(app.client):
        app.close()
        pytest.skip(
            "no Dex server at "
            f"{config.server_address}; set DEX_FLOW_SERVICE_ADDRESS to a running "
            "Dex server to run the example integration tests"
        )
    try:
        yield app
    finally:
        app.close()


@pytest.fixture(scope="session")
def app(example_app: ExampleApp) -> ExampleApp:
    return example_app


@pytest.fixture(scope="session")
def client(example_app: ExampleApp) -> Client:
    return example_app.client


@pytest.fixture()
def new_flow_id() -> Callable[[str], str]:
    """Returns a factory for Flow IDs that never collide across runs."""

    def make(prefix: str) -> str:
        return f"{prefix}-{uuid4().hex}"

    return make


def server_is_ready(client: Client) -> bool:
    deadline = time.monotonic() + SERVER_READY_TIMEOUT.total_seconds()
    while time.monotonic() < deadline:
        try:
            return client.health_check()
        except DexException:
            time.sleep(POLL_INTERVAL_SECONDS)
    return False


def wait_until(
    description: str,
    predicate: Callable[[], Any],
    timeout: timedelta = WAIT_TIMEOUT,
) -> Any:
    """Returns the first truthy value from predicate, or fails the test."""
    deadline = time.monotonic() + timeout.total_seconds()
    while True:
        value = predicate()
        if value:
            return value
        if time.monotonic() >= deadline:
            raise AssertionError(f"timed out waiting for {description}")
        time.sleep(POLL_INTERVAL_SECONDS)


def wait_for_attribute(
    client: Client,
    flow_id: str,
    attribute: Attribute[Any],
    timeout: timedelta = WAIT_TIMEOUT,
) -> Any:
    """Returns the Attribute value once the Flow has written something to it."""
    return wait_until(
        f"attribute {attribute.name} on {flow_id}",
        lambda: attribute_or_none(client, flow_id, attribute),
        timeout,
    )


def attribute_or_none(client: Client, flow_id: str, attribute: Attribute[Any]) -> Any:
    try:
        return client.get_attribute(flow_id, attribute)
    except DexException as error:
        if error.sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
            return None
        raise


def flow_status_or_none(client: Client, flow_id: str) -> FlowStatus | None:
    try:
        return client.describe_flow(flow_id).status
    except DexException as error:
        if error.sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
            return None
        raise


def wait_for_flow_status(
    client: Client,
    flow_id: str,
    status: FlowStatus,
    timeout: timedelta = WAIT_TIMEOUT,
) -> None:
    wait_until(
        f"Flow {flow_id} to reach {status.value}",
        lambda: flow_status_or_none(client, flow_id) is status,
        timeout,
    )
