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

from __future__ import annotations

from typing import Callable

import pytest
from dex import AsyncClient

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.ai_agent_email.ai_agent_flow import STATUS_WAITING
from dex_examples.resourcecontrol.controller_flow import SPOT_INSTANCE_IDS
from dex_examples.resourcecontrol.request import Request
from tests.integ.conftest import LONG_WAIT_TIMEOUT, WAIT_TIMEOUT, wait_until

pytestmark = pytest.mark.integ


async def test_basic_approve_completes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("basic")
    await client.start_flow(app.basic, flow_id, 5, start_options())
    appended = await client.invoke_rpc(app.basic.append_string, flow_id, "hello")
    assert "hello" in appended
    await client.invoke_rpc(app.basic.approve, flow_id)
    assert await client.wait_for_flow(flow_id, str, WAIT_TIMEOUT) == "approved"


async def test_resourcecontrol_enqueue(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    controller_id = new_flow_id(SPOT_INSTANCE_IDS[0])
    await client.start_flow(
        app.controller,
        controller_id,
        Request("bootstrap", "boot"),
        start_options(),
    )
    request = Request(new_flow_id("req"), "payload")
    assert await client.invoke_rpc(app.controller.enqueue, controller_id, request) is True

    async def controller_running() -> bool:
        info = await client.describe_flow(controller_id)
        return info.flow_id == controller_id

    await wait_until(
        "controller still running after enqueue",
        controller_running,
        LONG_WAIT_TIMEOUT,
    )


async def test_ai_agent_email_without_openai(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent")
    await client.start_flow(app.email_agent, flow_id, None, start_options())

    async def waiting() -> bool:
        details = await client.invoke_rpc(app.email_agent.describe, flow_id)
        return details.status == STATUS_WAITING

    await wait_until("email agent waiting", waiting, WAIT_TIMEOUT)
    assert await client.invoke_rpc(
        app.email_agent.send_request,
        flow_id,
        "Draft a short intro email",
    )
    details = await client.invoke_rpc(app.email_agent.describe, flow_id)
    assert details.current_request
