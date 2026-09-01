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
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.resource_control.controller_flow import SPOT_INSTANCE_IDS
from dex_examples.patterns.resource_control.request import Request
from dex_examples.products.ai_agent_email.ai_agent_flow import STATUS_WAITING
from tests.integ.conftest import LONG_WAIT_TIMEOUT, WAIT_TIMEOUT, wait_until

from dex import AsyncClient

pytestmark = pytest.mark.integ


async def test_channel_approve_completes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("channel")
    await client.start_flow(app.channel, flow_id, 5, start_options())
    await client.invoke_rpc(app.channel.approve, flow_id)
    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "approved"


async def test_stream_resumes_after_step_and_client_writes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("stream")
    await client.start_flow(app.stream, flow_id, "invoice", start_options())

    step_message = await client.read_stream(
        flow_id,
        app.stream.progress,
        timeout=WAIT_TIMEOUT,
    )
    assert step_message.value == "Rendering preview for invoice"
    assert step_message.source.startswith("#")

    second_step_message = await client.read_stream(
        flow_id,
        app.stream.progress,
        step_message.resume_token,
        WAIT_TIMEOUT,
    )
    assert second_step_message.value == "Preview ready for invoice"
    assert second_step_message.source == step_message.source

    await client.write_stream(
        flow_id,
        app.stream.progress,
        "browser/complete",
        "Preview displayed",
    )
    client_message = await client.read_stream(
        flow_id,
        app.stream.progress,
        second_step_message.resume_token,
        WAIT_TIMEOUT,
    )
    assert client_message.value == "Preview displayed"
    assert client_message.source == "browser/complete"


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
    assert (
        await client.invoke_rpc(app.controller.enqueue, controller_id, request) is True
    )

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
    thinking = await client.read_stream(
        flow_id,
        app.email_agent.thinking,
        timeout=WAIT_TIMEOUT,
    )
    assert thinking.value == "Analyzing the request. Preparing a local email draft. "
    details = await client.invoke_rpc(app.email_agent.describe, flow_id)
    assert details.current_request
