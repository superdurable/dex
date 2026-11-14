# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import threading
import time
from datetime import timedelta
from typing import cast

import grpc
import pytest
from google.protobuf import empty_pb2

from dex import (
    AsyncClient,
    BlobCache,
    Client,
    ClientOptions,
    DexServiceError,
    Registry,
    RequestTimeoutError,
    StepExecutionId,
    WaitForStepCompletionOptions,
)
from dex._server_protocol import negotiate_server_protocol
from dex.dexpb import dex_pb2 as pb
from test_hard_delete_attributes import ProtocolService, SecretFlow, _start_flow_server


class SlowNegotiationService(ProtocolService):
    def __init__(self, server_info_delay_seconds: float) -> None:
        super().__init__(current_protocol_version=4)
        self.server_info_delay_seconds = server_info_delay_seconds
        self.release_server_info = threading.Event()
        self.server_info_time_remaining: list[float | None] = []
        self.wait_requests: list[pb.WaitForStepCompletionRequest] = []
        self.wait_time_remaining: list[float | None] = []

    def GetServerInfo(  # noqa: N802
        self,
        request: empty_pb2.Empty,
        context: grpc.ServicerContext,
    ) -> pb.ServerInfo:
        self.server_info_time_remaining.append(context.time_remaining())
        self.release_server_info.wait(self.server_info_delay_seconds)
        return super().GetServerInfo(request, context)

    def WaitForStepCompletion(  # noqa: N802
        self,
        request: pb.WaitForStepCompletionRequest,
        context: grpc.ServicerContext,
    ) -> pb.WaitForStepCompletionResponse:
        self.wait_requests.append(request)
        self.wait_time_remaining.append(context.time_remaining())
        return pb.WaitForStepCompletionResponse()


def wait_options(seconds: int) -> WaitForStepCompletionOptions:
    return WaitForStepCompletionOptions(request_timeout=timedelta(seconds=seconds))


async def wait_for_step(client: Client | AsyncClient, seconds: int) -> None:
    step = StepExecutionId("Step")
    if isinstance(client, AsyncClient):
        await client.wait_for_step_completion("flow", step, wait_options(seconds))
    else:
        await asyncio.to_thread(
            client.wait_for_step_completion, "flow", step, wait_options(seconds)
        )


async def health_check(client: Client | AsyncClient) -> None:
    if isinstance(client, AsyncClient):
        await client.health_check()
    else:
        await asyncio.to_thread(client.health_check)


def create_client(is_async: bool, port: int) -> Client | AsyncClient:
    client_type = AsyncClient if is_async else Client
    return client_type(
        Registry([SecretFlow()]),
        cast(BlobCache, object()),
        ClientOptions(server_address=f"127.0.0.1:{port}"),
    )


async def close_client(client: Client | AsyncClient) -> None:
    if isinstance(client, AsyncClient):
        await client.close()
    else:
        client.close()


@pytest.mark.parametrize("is_async", [False, True])
def test_stalled_negotiation_respects_request_timeout(is_async: bool) -> None:
    asyncio.run(_stalled_negotiation_respects_request_timeout(is_async))


async def _stalled_negotiation_respects_request_timeout(is_async: bool) -> None:
    service = SlowNegotiationService(server_info_delay_seconds=5)
    server, port = _start_flow_server(service)
    client = create_client(is_async, port)
    try:
        started_at = time.monotonic()
        with pytest.raises(RequestTimeoutError):
            await wait_for_step(client, 1)
        assert time.monotonic() - started_at < 2
        assert service.wait_requests == []
        server_info_time_remaining = service.server_info_time_remaining[0]
        assert server_info_time_remaining is not None
        assert server_info_time_remaining <= 1.1
    finally:
        service.release_server_info.set()
        await close_client(client)
        server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize("is_async", [False, True])
def test_wait_request_uses_budget_left_after_negotiation(is_async: bool) -> None:
    asyncio.run(_wait_request_uses_budget_left_after_negotiation(is_async))


async def _wait_request_uses_budget_left_after_negotiation(is_async: bool) -> None:
    service = SlowNegotiationService(server_info_delay_seconds=1.2)
    server, port = _start_flow_server(service)
    client = create_client(is_async, port)
    try:
        await wait_for_step(client, 3)
        assert [
            request.request_timeout_seconds for request in service.wait_requests
        ] == [2]
        wait_time_remaining = service.wait_time_remaining[0]
        assert wait_time_remaining is not None
        assert wait_time_remaining <= 1.9
    finally:
        await close_client(client)
        server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize("is_async", [False, True])
def test_waiting_for_concurrent_negotiation_respects_request_timeout(
    is_async: bool,
) -> None:
    asyncio.run(_waiting_for_concurrent_negotiation_respects_request_timeout(is_async))


async def _waiting_for_concurrent_negotiation_respects_request_timeout(
    is_async: bool,
) -> None:
    service = SlowNegotiationService(server_info_delay_seconds=5)
    server, port = _start_flow_server(service)
    client = create_client(is_async, port)
    unbounded_call = asyncio.create_task(health_check(client))
    try:
        async with asyncio.timeout(2):
            while not service.server_info_time_remaining:
                await asyncio.sleep(0.01)
        started_at = time.monotonic()
        with pytest.raises(RequestTimeoutError):
            await wait_for_step(client, 1)
        assert time.monotonic() - started_at < 2
        assert len(service.server_info_time_remaining) == 1
        assert not unbounded_call.done()
    finally:
        service.release_server_info.set()
        # ProtocolService does not implement HealthCheck.
        with pytest.raises(DexServiceError):
            await asyncio.wait_for(unbounded_call, 5)
        await close_client(client)
        server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize("is_async", [False, True])
def test_unreachable_server_raises_service_error(is_async: bool) -> None:
    asyncio.run(_unreachable_server_raises_service_error(is_async))


async def _unreachable_server_raises_service_error(is_async: bool) -> None:
    client = create_client(is_async, 1)
    try:
        for _ in range(2):
            with pytest.raises(DexServiceError) as failure:
                await health_check(client)
            assert failure.value.code is grpc.StatusCode.UNAVAILABLE
            assert "incompatible" not in str(failure.value)
    finally:
        await close_client(client)


@pytest.mark.parametrize(
    "server_info,expected_message",
    [
        (
            pb.ServerInfo(
                server_version="old",
                minimum_supported_protocol_version=1,
                current_protocol_version=3,
            ),
            "This SDK requires a Dex Server with protocol version 4 or later "
            "(server-zd newer than v0.1.16). The Server reports protocol versions "
            "1-3 (server version old). Upgrade the Server before upgrading the SDK.",
        ),
        (
            pb.ServerInfo(
                server_version="future",
                minimum_supported_protocol_version=6,
                current_protocol_version=7,
            ),
            'Python SDK version "test" supports Dex Server protocol versions 4-5, '
            "but the Server requires a newer protocol. The Server reports protocol "
            "versions 6-7 (server version future). Upgrade the SDK.",
        ),
        (
            pb.ServerInfo(),
            'Python SDK version "test" protocol [4,5] cannot use Server version '
            '"unknown" protocol [0,0]: Server protocol interval is invalid',
        ),
        (
            pb.ServerInfo(
                server_version="broken",
                minimum_supported_protocol_version=5,
                current_protocol_version=4,
            ),
            'Python SDK version "test" protocol [4,5] cannot use Server version '
            '"broken" protocol [5,4]: Server protocol interval is invalid',
        ),
    ],
)
def test_incompatibility_message_names_the_component_to_upgrade(
    server_info: pb.ServerInfo, expected_message: str
) -> None:
    with pytest.raises(RuntimeError) as failure:
        negotiate_server_protocol(server_info, "test")
    assert str(failure.value) == expected_message
    assert "incompatible" not in str(failure.value)
