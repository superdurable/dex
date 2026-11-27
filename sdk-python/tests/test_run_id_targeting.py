# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import asyncio
from typing import Any, cast

from google.protobuf import empty_pb2

from dex import (
    AsyncClient,
    Attribute,
    BlobCache,
    Client,
    Flow,
    PersistenceSchema,
    Registry,
)
from dex.dexpb import dex_pb2 as pb

RUN_ID = "0b5c6a4e-2f0d-4c84-9a52-7c1f0d2e8a31"
STATUS = Attribute("status", str)


class RunIdTargetingFlow(Flow[None]):
    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(STATUS)


class RunIdRecordingService:
    def __init__(self) -> None:
        self.requests: list[Any] = []

    def GetFlowSummary(self, request: pb.GetFlowSummaryRequest) -> Any:
        self.requests.append(request)
        return pb.GetFlowSummaryResponse(
            flow_run_id=pb.FlowRunID(flow_id=request.flow_id, run_id=request.run_id),
            flow_type="RunIdTargetingFlow",
            flow_status=pb.FLOW_STATUS_COMPLETED,
        )

    def GetAttributes(self, request: pb.GetAttributesRequest) -> Any:
        self.requests.append(request)
        return pb.GetAttributesResponse()

    def WaitForFlow(self, request: pb.WaitForFlowRequest) -> Any:
        self.requests.append(request)
        return pb.FlowResult(flow_status=pb.FLOW_STATUS_COMPLETED)

    def StopFlow(self, request: pb.StopFlowRequest) -> Any:
        self.requests.append(request)
        return empty_pb2.Empty()


class AsyncRunIdRecordingService:
    def __init__(self) -> None:
        self.recorder = RunIdRecordingService()

    @property
    def requests(self) -> list[Any]:
        return self.recorder.requests

    async def GetFlowSummary(self, request: pb.GetFlowSummaryRequest) -> Any:
        return self.recorder.GetFlowSummary(request)

    async def GetAttributes(self, request: pb.GetAttributesRequest) -> Any:
        return self.recorder.GetAttributes(request)

    async def WaitForFlow(self, request: pb.WaitForFlowRequest) -> Any:
        return self.recorder.WaitForFlow(request)

    async def StopFlow(self, request: pb.StopFlowRequest) -> Any:
        return self.recorder.StopFlow(request)


def _assert_run_ids(requests: list[Any]) -> None:
    sent = [(type(request).__name__, request.run_id) for request in requests]
    assert sent == [
        ("GetFlowSummaryRequest", ""),
        ("GetFlowSummaryRequest", RUN_ID),
        ("GetAttributesRequest", ""),
        ("GetAttributesRequest", RUN_ID),
        ("WaitForFlowRequest", ""),
        ("WaitForFlowRequest", RUN_ID),
        ("StopFlowRequest", ""),
        ("StopFlowRequest", RUN_ID),
    ]
    assert all(request.flow_id == "flow-1" for request in requests)


def test_client_sends_run_id_only_when_given() -> None:
    client = Client(Registry((RunIdTargetingFlow(),)), cast(BlobCache, object()))
    service = RunIdRecordingService()
    client._service = cast(Any, service)
    client._server_protocol.record_version(4)
    try:
        assert client.describe_flow("flow-1").run_id == ""
        assert client.describe_flow("flow-1", run_id=RUN_ID).run_id == RUN_ID
        client.get_attributes("flow-1", STATUS)
        client.get_attributes("flow-1", STATUS, run_id=RUN_ID)
        client.wait_for_flow("flow-1")
        client.wait_for_flow("flow-1", run_id=RUN_ID)
        client.stop_flow("flow-1")
        client.stop_flow("flow-1", run_id=RUN_ID)
    finally:
        client.close()
    _assert_run_ids(service.requests)


def test_async_client_sends_run_id_only_when_given() -> None:
    asyncio.run(_assert_async_client_sends_run_id_only_when_given())


async def _assert_async_client_sends_run_id_only_when_given() -> None:
    client = AsyncClient(Registry((RunIdTargetingFlow(),)), cast(BlobCache, object()))
    service = AsyncRunIdRecordingService()
    client._service = cast(Any, service)
    client._server_protocol.record_version(4)
    try:
        assert (await client.describe_flow("flow-1")).run_id == ""
        assert (await client.describe_flow("flow-1", run_id=RUN_ID)).run_id == RUN_ID
        await client.get_attributes("flow-1", STATUS)
        await client.get_attributes("flow-1", STATUS, run_id=RUN_ID)
        await client.wait_for_flow("flow-1")
        await client.wait_for_flow("flow-1", run_id=RUN_ID)
        await client.stop_flow("flow-1")
        await client.stop_flow("flow-1", run_id=RUN_ID)
    finally:
        await client.close()
    _assert_run_ids(service.requests)
