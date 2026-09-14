# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
from typing import Any, cast

import grpc
import pytest
from google.protobuf import any_pb2
from google.rpc import status_pb2

from dex import (
    AsyncClient,
    Attribute,
    AttributeMatch,
    BlobCacheConfig,
    Client,
    ClientOptions,
    Registry,
    StepExecutionId,
    WaitForAttributeOptions,
    WaitForStepCompletionOptions,
)
from dex.dexpb import dex_pb2 as pb


class MemoryBlobCache:
    config = BlobCacheConfig("memory", 1_024)

    def get(self, blob_id: str) -> bytes | None:
        return None

    def put(self, blob_id: str, payload: bytes) -> bool:
        return True

    def delete(self, blob_id: str) -> None:
        return None

    def delete_all(self) -> None:
        return None

    def close(self) -> None:
        return None


class SyncWaitService:
    def __init__(self) -> None:
        self.requests: list[pb.WaitForAttributeRequest] = []

    def WaitForAttribute(  # noqa: N802
        self, request: pb.WaitForAttributeRequest
    ) -> pb.WaitForAttributeResponse:
        self.requests.append(request)
        if len(self.requests) == 1:
            raise _long_poll_timeout()
        return pb.WaitForAttributeResponse(matched_value=pb.Value(int_value=7))


class AsyncWaitService:
    def __init__(self) -> None:
        self.requests: list[pb.WaitForAttributeRequest] = []

    async def WaitForAttribute(  # noqa: N802
        self, request: pb.WaitForAttributeRequest
    ) -> pb.WaitForAttributeResponse:
        self.requests.append(request)
        if len(self.requests) == 1:
            raise _long_poll_timeout()
        return pb.WaitForAttributeResponse(matched_value=pb.Value(int_value=7))


def test_sync_attribute_wait_reattaches_and_returns_matched_value() -> None:
    service = SyncWaitService()
    client = Client(Registry(()), MemoryBlobCache(), ClientOptions("unused:1"))
    client._service = cast(Any, service)
    try:
        options = WaitForAttributeOptions(request_id="wait-revision")
        matched = client.wait_for_attribute_match(
            "flow-1",
            Attribute("revision", int),
            AttributeMatch.greater_than(5),
            options,
        )
        assert matched == 7
        assert [request.request_id for request in service.requests] == [
            options.request_id,
            options.request_id,
        ]
    finally:
        client.close()


def test_async_attribute_wait_reattaches_and_returns_matched_value() -> None:
    asyncio.run(_test_async_attribute_wait_reattaches_and_returns_matched_value())


async def _test_async_attribute_wait_reattaches_and_returns_matched_value() -> None:
    service = AsyncWaitService()
    client = AsyncClient(Registry(()), MemoryBlobCache(), ClientOptions("unused:1"))
    client._service = cast(Any, service)
    try:
        options = WaitForAttributeOptions(request_id="wait-revision")
        matched = await client.wait_for_attribute_match(
            "flow-1",
            Attribute("revision", int),
            AttributeMatch.greater_than(5),
            options,
        )
        assert matched == 7
        assert [request.request_id for request in service.requests] == [
            options.request_id,
            options.request_id,
        ]
    finally:
        await client.close()


def test_wait_request_id_is_required() -> None:
    client = Client(Registry(()), MemoryBlobCache(), ClientOptions("unused:1"))
    try:
        with pytest.raises(ValueError, match="request ID is required"):
            client.wait_for_step_completion(
                "flow-1",
                StepExecutionId("Step", 1),
                WaitForStepCompletionOptions(),
            )
    finally:
        client.close()


class FakeRpcError(grpc.RpcError):
    def code(self) -> grpc.StatusCode:
        return grpc.StatusCode.DEADLINE_EXCEEDED

    def details(self) -> str:
        return "long poll timed out"

    def trailing_metadata(self) -> tuple[Any, ...]:
        response = pb.ServiceErrorResponse(
            detail="long poll timed out",
            sub_status=pb.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
        )
        packed = any_pb2.Any()
        packed.Pack(response)
        status = status_pb2.Status(
            code=grpc.StatusCode.DEADLINE_EXCEEDED.value[0],
            message="long poll timed out",
            details=[packed],
        )
        return (("grpc-status-details-bin", status.SerializeToString()),)


def _long_poll_timeout() -> FakeRpcError:
    return FakeRpcError()
