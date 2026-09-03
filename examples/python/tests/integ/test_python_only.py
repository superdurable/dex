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
from dex_examples.primitives.channel.channel_flow import MoveMessage
from dex_examples.products.ai_agent.ai_agent_flow import STATUS_WAITING
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    HistoryRequest,
    UserMessage,
)
from tests.integ.conftest import LONG_WAIT_TIMEOUT, WAIT_TIMEOUT, wait_until

from dex import AsyncClient, ChannelMessageNotFoundError

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


async def test_channel_pending_messages_can_be_deleted_and_moved(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("channel-queue")
    await client.start_flow(app.channel, flow_id, 30, start_options())
    await client.publish(flow_id, app.channel.queued, "delete me")
    await client.publish(flow_id, app.channel.queued, "move me")

    pending = await client.get_channel_messages(flow_id, app.channel.queued)
    assert [message.value for message in pending] == ["delete me", "move me"]

    await client.delete_channel_message(
        flow_id,
        app.channel.queued,
        pending[0].message_id,
    )
    move_message = MoveMessage(pending[1].message_id)
    await client.invoke_rpc(app.channel.move, flow_id, move_message)

    assert not await client.get_channel_messages(flow_id, app.channel.queued)
    moved = await client.get_channel_messages(flow_id, app.channel.moved)
    assert [message.value for message in moved] == ["move me"]

    with pytest.raises(ChannelMessageNotFoundError):
        await client.invoke_rpc(app.channel.move, flow_id, move_message)
    moved_after_failure = await client.get_channel_messages(flow_id, app.channel.moved)
    assert [message.value for message in moved_after_failure] == ["move me"]

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
    assert step_message.value == "Rendering preview for invoicePreview ready for invoice"
    assert step_message.source.startswith("#")

    await client.write_stream(
        flow_id,
        app.stream.progress,
        "browser/complete",
        "Preview displayed",
    )
    client_message = await client.read_stream(
        flow_id,
        app.stream.progress,
        step_message.resume_token,
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


async def test_ai_agent_conversation_and_durable_wait(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent")
    await client.start_flow(app.ai_agent, flow_id, AgentConfig(), start_options())

    async def waiting() -> bool:
        details = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return details.status == STATUS_WAITING

    await wait_until("AI Agent waiting", waiting, WAIT_TIMEOUT)
    assert await client.invoke_rpc(
        app.ai_agent.send_message,
        flow_id,
        UserMessage("Hello durable agent"),
    )
    assistant_text = await client.read_stream(
        flow_id,
        app.ai_agent.assistant_text,
        timeout=WAIT_TIMEOUT,
    )
    assert assistant_text.value == "Local demo response: Hello durable agent"
    activity = await client.read_stream(
        flow_id,
        app.ai_agent.agent_activity,
        timeout=WAIT_TIMEOUT,
    )
    assert activity.value.kind == "model_started"

    async def has_reply() -> bool:
        history = await client.invoke_rpc(
            app.ai_agent.history,
            flow_id,
            HistoryRequest(limit=10),
        )
        return any(message.message.role == "assistant" for message in history.messages)

    await wait_until("AI Agent reply", has_reply, WAIT_TIMEOUT)
    assert await client.invoke_rpc(
        app.ai_agent.send_message,
        flow_id,
        UserMessage("/wait 1 integration timer"),
    )

    async def timer_completed() -> bool:
        history = await client.invoke_rpc(
            app.ai_agent.history,
            flow_id,
            HistoryRequest(limit=20),
        )
        return any(
            message.message.role == "tool"
            and '"status": "completed"' in message.message.content
            for message in history.messages
        )

    await wait_until("durable timer completion", timer_completed, WAIT_TIMEOUT)
