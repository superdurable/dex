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

"""Shared harness for the agent harness example tests.

Every test needs a running Dex server. Point DEX_FLOW_SERVICE_ADDRESS at it
(default 127.0.0.1:8801). Without a server the tests fail, unless
DEX_EXAMPLES_ALLOW_SKIP=1 asks them to skip.
"""

from __future__ import annotations

import asyncio
import os
from collections.abc import AsyncIterator, Awaitable, Callable
from datetime import timedelta
from typing import Any

import pytest
import pytest_asyncio
from dex import DexServiceError
from dex_connectors.slack import slackmock

from agent_harness_examples.app import AgentHarnessExamplesApp
from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.slack_connector_tools import SLACK_CONNECTION_NAME

WAIT_TIMEOUT = timedelta(seconds=60)
SERVER_READY_TIMEOUT = timedelta(seconds=20)
POLL_INTERVAL_SECONDS = 0.25


@pytest.fixture(scope="session")
def slack_mock() -> slackmock.Mock:
    """The Slack connector's mock; it records every call and never reaches Slack."""
    return slackmock.Mock(connection_name=SLACK_CONNECTION_NAME)


@pytest_asyncio.fixture(scope="session")
async def app(
    tmp_path_factory: pytest.TempPathFactory, slack_mock: slackmock.Mock
) -> AsyncIterator[AgentHarnessExamplesApp]:
    """Boots one app whose Worker serves every order support agent."""
    config = AgentHarnessExamplesConfig(
        server_address=os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
        http_address="127.0.0.1:0",
        blob_cache_dir=tmp_path_factory.mktemp("dex-agent-harness-blob-cache"),
        # The Claude Agent SDK test points the CLI at a local stub with a dummy key.
        has_anthropic_api_key=True,
    )
    app_state = AgentHarnessExamplesApp(config, slack_mock.connection())
    try:
        if not await _is_server_ready(app_state):
            message = f"no Dex server at {config.server_address}; set DEX_FLOW_SERVICE_ADDRESS"
            if os.environ.get("DEX_EXAMPLES_ALLOW_SKIP") == "1":
                pytest.skip(message)
            pytest.fail(message)
        await app_state.start_worker()
        yield app_state
    finally:
        await app_state.close()


async def _is_server_ready(app_state: AgentHarnessExamplesApp) -> bool:
    loop = asyncio.get_running_loop()
    deadline = loop.time() + SERVER_READY_TIMEOUT.total_seconds()
    while loop.time() < deadline:
        try:
            return await app_state.client.health_check()
        except DexServiceError:
            await asyncio.sleep(POLL_INTERVAL_SECONDS)
    return False


async def wait_until(
    description: str,
    predicate: Callable[[], Awaitable[Any]],
    timeout: timedelta = WAIT_TIMEOUT,
) -> Any:
    """Returns the first truthy value from predicate, or fails the test."""
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout.total_seconds()
    while True:
        value = await predicate()
        if value:
            return value
        if loop.time() >= deadline:
            raise AssertionError(f"timed out waiting for {description}")
        await asyncio.sleep(POLL_INTERVAL_SECONDS)
