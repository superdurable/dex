# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import asyncio
from concurrent.futures import ThreadPoolExecutor

import grpc
import pytest
from google.protobuf import timestamp_pb2

from dex import (
    AsyncClient,
    BlobCacheConfig,
    Client,
    ClientOptions,
    FlowUncompletedError,
    Registry,
)
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc


class MemoryBlobCache:
    config = BlobCacheConfig("memory", 1_024)

    def __init__(self) -> None:
        self.values: dict[str, bytes] = {}

    def get(self, blob_id: str) -> bytes | None:
        return self.values.get(blob_id)

    def put(self, blob_id: str, payload: bytes) -> bool:
        self.values[blob_id] = payload
        return True

    def delete(self, blob_id: str) -> None:
        self.values.pop(blob_id, None)

    def delete_all(self) -> None:
        self.values.clear()

    def close(self) -> None:
        return None


class WaitForFlowService(dex_pb2_grpc.FlowServiceServicer):
    def __init__(self) -> None:
        self.loaded_blob_ids: list[str] = []

    def WaitForFlow(  # noqa: N802
        self,
        request: pb.WaitForFlowRequest,
        context: grpc.ServicerContext,
    ) -> pb.WaitForFlowResponse:
        del context
        results = self._results()
        if request.flow_id == "empty":
            results = []
        elif request.flow_id == "single":
            results = results[:1]
        status = (
            pb.FLOW_STATUS_FAILED
            if request.flow_id == "failed"
            else pb.FLOW_STATUS_COMPLETED
        )
        return pb.WaitForFlowResponse(
            flow_status=status,
            results=results,
            error_type=pb.FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
            error_message="failed by test" if request.flow_id == "failed" else "",
        )

    def LoadBlobs(  # noqa: N802
        self,
        request: pb.LoadBlobsRequest,
        context: grpc.ServicerContext,
    ) -> pb.LoadBlobsResponse:
        del context
        values: dict[str, pb.Value] = {}
        for value in request.values:
            blob_id = value.WhichOneof("kind")
            if blob_id == "internal_blob_id_for_string_value":
                key = value.internal_blob_id_for_string_value
                values[key] = pb.Value(string_value="one")
            elif blob_id == "internal_blob_id_for_obj_value":
                key = value.internal_blob_id_for_obj_value
                values[key] = pb.Value(
                    obj_value=pb.EncodedObject(encoding="rawbytes", payload=b"done")
                )
            else:
                raise AssertionError("unexpected LoadBlobs value")
            self.loaded_blob_ids.append(key)
        return pb.LoadBlobsResponse(values=values)

    def GetFlowSummary(  # noqa: N802
        self,
        request: pb.GetFlowSummaryRequest,
        context: grpc.ServicerContext,
    ) -> pb.GetFlowSummaryResponse:
        del context
        return pb.GetFlowSummaryResponse(
            flow_execution_id=pb.FlowExecutionID(
                flow_id=request.flow_id,
                run_id="run-failed",
            ),
            flow_type="WaitForFlowTest",
            flow_status=pb.FLOW_STATUS_FAILED,
            start_time=timestamp_pb2.Timestamp(seconds=1),
        )

    @staticmethod
    def _results() -> list[pb.StepCompletionOutput]:
        return [
            pb.StepCompletionOutput(
                completed_step_type="First",
                completed_step_execution_id="First-1",
                completed_step_output=pb.Value(
                    internal_blob_id_for_string_value="first-blob"
                ),
            ),
            pb.StepCompletionOutput(
                completed_step_type="Second",
                completed_step_execution_id="Second-2",
                completed_step_output=pb.Value(
                    internal_blob_id_for_obj_value="second-blob"
                ),
            ),
        ]


def test_sync_wait_for_flow_returns_every_hydrated_completion() -> None:
    service = WaitForFlowService()
    server, address = _start_server(service)
    cache = MemoryBlobCache()
    client = Client(Registry(()), cache, ClientOptions(address))
    try:
        result = client.wait_for_flow("multi")
        assert [completion.step_type for completion in result.completions] == [
            "First",
            "Second",
        ]
        assert result.completions[0].step_execution_id == "First-1"
        assert result.completions[0].decode(str) == "one"
        assert result.completions[1].decode(bytes) == b"done"
        assert service.loaded_blob_ids == ["first-blob", "second-blob"]
        with pytest.raises(ValueError, match="found 2"):
            result.single_output(str)
        assert client.wait_for_flow("single").single_output(str) == "one"
        with pytest.raises(ValueError, match="found 0"):
            client.wait_for_flow("empty").single_output(str)
    finally:
        client.close()
        server.stop(grace=None).wait(timeout=5)


def test_async_wait_for_flow_and_failure_preserve_completions() -> None:
    asyncio.run(_test_async_wait_for_flow_and_failure_preserve_completions())


async def _test_async_wait_for_flow_and_failure_preserve_completions() -> None:
    service = WaitForFlowService()
    server, address = _start_server(service)
    client = AsyncClient(Registry(()), MemoryBlobCache(), ClientOptions(address))
    try:
        result = await client.wait_for_flow("multi")
        assert result.completions[0].decode(str) == "one"
        assert result.completions[1].decode(bytes) == b"done"
        with pytest.raises(FlowUncompletedError) as captured:
            await client.wait_for_flow("failed")
        failure = captured.value
        assert failure.run_id == "run-failed"
        assert [completion.step_execution_id for completion in failure.completions] == [
            "First-1",
            "Second-2",
        ]
        assert failure.completions[1].decode(bytes) == b"done"
    finally:
        await client.close()
        server.stop(grace=None).wait(timeout=5)


def _start_server(
    service: WaitForFlowService,
) -> tuple[grpc.Server, str]:
    server = grpc.server(ThreadPoolExecutor(max_workers=2))
    dex_pb2_grpc.add_FlowServiceServicer_to_server(  # type: ignore[no-untyped-call]
        service,
        server,
    )
    port = server.add_insecure_port("127.0.0.1:0")
    if port == 0:
        raise RuntimeError("cannot bind test FlowService")
    server.start()
    return server, f"127.0.0.1:{port}"
