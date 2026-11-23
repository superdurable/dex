# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A released connector's write through ConnectorToolSource: the fence checkpoint survives a retry.

dex-connectors-sdk 0.12 and later fences save their heartbeat as a plain dict of
markers. The released Gmail connector's sendMessage runs here against a local
Gmail stand-in: the first attempt sends and saves its marker as the tool Step's
text checkpoint, and an attempt that starts again from that checkpoint, as after
a Worker crash before the result was recorded, reads the message back by ID
instead of sending again.
"""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from typing import Any, cast

import httpx
from dex import AsyncContext
from dex_connectors.google_gmail import (
    PROVIDER,
    Config,
    Credentials,
    GmailProvider,
    new_connection,
)
from dex_connectors.sdk import ConnectionRef
from dex_connectors.sdk.credentials import SecretString, StaticCredentialProvider

from dex_agents import (
    AgentEventWriter,
    AgentToolCall,
    AgentTurnContext,
    DeferredToolExecution,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.connectors import ConnectorStepContext, ConnectorToolSource, connector_tool
from dex_agents.connectors._step_context import encode_heartbeat_value

TOOL_NAME = "gmail_send_message"


class _GmailStandIn:
    """Gmail's send and read-by-ID endpoints, counting every request."""

    def __init__(self) -> None:
        self.requests: list[httpx.Request] = []

    def __call__(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        if request.method == "POST" and request.url.path == "/gmail/v1/users/me/messages/send":
            return httpx.Response(200, json={"id": "sent-1", "threadId": "thread-1"})
        if request.method == "GET" and request.url.path == "/gmail/v1/users/me/messages/sent-1":
            return httpx.Response(200, json={"id": "sent-1", "threadId": "thread-1"})
        return httpx.Response(404, json={"error": {"code": 404, "message": "not found"}})

    def sends(self) -> list[httpx.Request]:
        return [request for request in self.requests if request.method == "POST"]


class _StepContext:
    """The members of AsyncContext that ConnectorStepContext reads."""

    def __init__(self, attempt: int) -> None:
        self.flow_id = "session-1"
        self.run_id = "run-1"
        self.flow_started_at = datetime(2026, 11, 23, 8, 0, 0, tzinfo=UTC)
        self.step_execution_id = "CallSingleAttemptTool-7"
        self.first_attempt_at = datetime(2026, 11, 23, 8, 0, 5, tzinfo=UTC)
        self.attempt = attempt
        self.deadline = datetime.now(UTC) + timedelta(minutes=1)


class _ToolStepCheckpoint:
    """The session's checkpoint behavior: each heartbeat repeats the latest text checkpoint."""

    def __init__(self) -> None:
        self.checkpoint: str | None = None

    async def record(self) -> None:
        return None

    async def save(self, checkpoint: str) -> None:
        self.checkpoint = checkpoint


def _source(gmail: _GmailStandIn) -> ConnectorToolSource:
    reference = ConnectionRef(PROVIDER, "test-gmail")
    credentials = StaticCredentialProvider(
        {reference: Credentials(SecretString("access-token"), "owner@example.org")}
    )
    provider = GmailProvider(
        Config(endpoint="http://localhost/gmail/v1"),
        credentials,
        http_client=httpx.AsyncClient(transport=httpx.MockTransport(gmail)),
    )
    connection = new_connection(provider, reference)
    return ConnectorToolSource(
        [connector_tool(connection.send_message(), connection.reference, requires_approval=False)]
    )


async def _run_attempt(
    source: ConnectorToolSource,
    arguments: str,
    reference: str | None,
    attempt: int,
    step: _ToolStepCheckpoint,
) -> Any:
    record = AgentToolCall(
        tool_call_id="call-1",
        turn_id="turn-1",
        tool_name=TOOL_NAME,
        arguments=arguments,
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=False,
        status=ToolCallStatus.APPROVED,
        tool_source_reference=reference,
    )
    return await source.execute_deferred_tool_call(
        DeferredToolExecution(
            flow_id="session-1",
            tool_call=record,
            idempotency_key="session-1/turn-1/call-1",
            attempt=attempt,
            events=cast(AgentEventWriter, None),
            heartbeat=step.record,
            heartbeat_checkpoint=step.checkpoint,
            save_heartbeat_checkpoint=step.save,
            context=cast(AsyncContext, _StepContext(attempt)),
        )
    )


def _turn() -> AgentTurnContext:
    async def heartbeat() -> None:
        return None

    return AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=(),
        framework_run_state=None,
        tool_calls={},
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=cast(AgentEventWriter, None),
        heartbeat=heartbeat,
    )


async def test_a_released_connector_send_keeps_its_fence_checkpoint_for_a_retry() -> None:
    gmail = _GmailStandIn()
    source = _source(gmail)
    arguments = json.dumps(
        {"to": ["ada@example.com"], "subject": "Weekly update", "textBody": "Hello"}
    )
    deferred = source.deferred_tool_call(
        _turn(), tool_call_id="call-1", tool_name=TOOL_NAME, arguments=arguments, listed_tool=None
    )
    step = _ToolStepCheckpoint()

    first = await _run_attempt(source, arguments, deferred.tool_source_reference, 1, step)

    assert json.loads(first.output)["messageId"] == "sent-1"
    assert len(gmail.sends()) == 1
    assert step.checkpoint is not None
    saved = json.loads(step.checkpoint)
    [marker] = saved["singleDispatchMarkers"].values()
    assert (marker["providerId"], marker["sendCount"]) == ("sent-1", 1)

    # The Worker dies before the result is recorded: the next attempt starts from the checkpoint.
    second = await _run_attempt(source, arguments, deferred.tool_source_reference, 2, step)

    assert json.loads(second.output)["messageId"] == "sent-1"
    assert len(gmail.sends()) == 1
    assert [request.method for request in gmail.requests] == ["POST", "GET"]


async def test_plain_json_collections_round_trip_as_heartbeat_checkpoints() -> None:
    saved: list[str] = []

    async def save(checkpoint: str) -> None:
        saved.append(checkpoint)

    async def heartbeat() -> None:
        return None

    context = ConnectorStepContext(
        cast(AsyncContext, _StepContext(1)),
        previous_checkpoint=None,
        heartbeat=heartbeat,
        save_checkpoint=save,
    )
    markers = {"singleDispatchMarkers": {"call-1": {"callId": "call-1", "sendCount": 1}}}
    await context.heartbeat(markers)
    await context.heartbeat(["a", 1])
    assert saved[0] == encode_heartbeat_value(markers)

    retry = ConnectorStepContext(
        cast(AsyncContext, _StepContext(2)),
        previous_checkpoint=saved[0],
        heartbeat=heartbeat,
        save_checkpoint=save,
    )
    assert retry.get_last_heartbeat_value(dict[str, Any]) == markers
    assert retry.get_last_heartbeat_value(dict) == markers
    assert ConnectorStepContext(
        cast(AsyncContext, _StepContext(2)),
        previous_checkpoint=saved[1],
        heartbeat=heartbeat,
        save_checkpoint=save,
    ).get_last_heartbeat_value(list) == ["a", 1]
