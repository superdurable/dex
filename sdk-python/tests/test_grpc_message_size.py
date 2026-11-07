# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any, cast

import grpc
import grpc.aio
import pytest
from google.protobuf.any_pb2 import Any as AnyMessage
from google.rpc import status_pb2

from dex import (
    DEFAULT_GRPC_MAX_MESSAGE_BYTES,
    BlobCache,
    BlobCacheConfig,
    BlobMessageTooLargeError,
    ClientOptions,
    WorkerOptions,
    open_blob_cache,
)
from dex._async_value_hydrator import AsyncValueHydrator
from dex._message_size import (
    grpc_message_size_channel_options,
    is_grpc_message_size_error,
)
from dex._value_hydrator import ValueHydrator
from dex.dexpb import dex_pb2 as pb

RECEIVE_LIMIT_MESSAGE = "Received message larger than max (9 vs. 5)"
SERVER_SEND_LIMIT_MESSAGE = (
    "Error, encoded message length too large: found 37748736 bytes, "
    "the limit is: 33554432 bytes"
)
SIZE_FAILURES = [
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED, RECEIVE_LIMIT_MESSAGE, id="receive-limit"
    ),
    pytest.param(
        grpc.StatusCode.OUT_OF_RANGE, SERVER_SEND_LIMIT_MESSAGE, id="server-send-limit"
    ),
]

# Every message size failure that the pinned grpc-go, grpc-core, grpc-js, and tonic emit.
TRANSPORT_SIZE_FAILURES = [
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "grpc: received message larger than max (37748736 vs. 33554432)",
        id="grpc-go-receive",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "grpc: received message larger than max length allowed on current machine (37748736 vs. 33554432)",
        id="grpc-go-receive-machine",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "grpc: message after decompression larger than max (37748736 vs. 33554432)",
        id="grpc-go-decompressed",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "grpc: received message after decompression larger than max 33554432",
        id="grpc-go-decompressed-stream",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "grpc: trying to send message larger than max (37748736 vs. 33554432)",
        id="grpc-go-server-send",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "trying to send message larger than max (37748736 vs. 33554432)",
        id="grpc-go-client-send",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "grpc: message too large (4294967296 bytes)",
        id="grpc-go-marshal",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "CLIENT: Received message larger than max (37748736 vs. 33554432)",
        id="grpc-core-client-receive",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "CLIENT: Sent message larger than max (37748736 vs. 33554432)",
        id="grpc-core-client-send",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "SERVER: Received message larger than max (37748736 vs. 33554432)",
        id="grpc-core-server-receive",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "Received message larger than max (37748736 vs 33554432)",
        id="grpc-js-receive",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "Received message that decompresses to a size larger than 33554432",
        id="grpc-js-decompressed",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "Attempted to send message with a size larger than 33554432",
        id="grpc-js-client-send",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "Sent message larger than max (37748736 vs. 33554432)",
        id="grpc-js-server-send",
    ),
    pytest.param(
        grpc.StatusCode.OUT_OF_RANGE,
        "Error, encoded message length too large: found 37748736 bytes, the limit is: 33554432 bytes",
        id="tonic-encode",
    ),
    pytest.param(
        grpc.StatusCode.OUT_OF_RANGE,
        "Error, decoded message length too large: found 37748736 bytes, the limit is: 33554432 bytes",
        id="tonic-decode",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "Error decompressing: size limit, of 33554432 bytes, exceeded while decompressing message",
        id="tonic-decompress",
    ),
    pytest.param(
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        "Cannot return body with more than 4GB of data but got 4294967296 bytes",
        id="tonic-body-over-4-gib",
    ),
]


class FakeRpcError(grpc.RpcError):
    def __init__(
        self,
        code: grpc.StatusCode,
        detail: str,
        trailing_metadata: tuple[Any, ...] = (),
    ) -> None:
        super().__init__(detail)
        self._code = code
        self._detail = detail
        self._trailing_metadata = trailing_metadata

    def code(self) -> grpc.StatusCode:
        return self._code

    def details(self) -> str:
        return self._detail

    def trailing_metadata(self) -> tuple[Any, ...]:
        return self._trailing_metadata


def dex_status_metadata(code: grpc.StatusCode, detail: str) -> tuple[Any, ...]:
    service_error = AnyMessage()
    service_error.Pack(pb.ServiceErrorResponse(detail=detail))
    encoded_status = status_pb2.Status(
        code=code.value[0], message=detail, details=[service_error]
    )
    return (("grpc-status-details-bin", encoded_status.SerializeToString()),)


class LimitedBlobService:
    """Reject any LoadBlobs call that asks for more than max_blobs blobs."""

    def __init__(
        self,
        max_blobs: int,
        code: grpc.StatusCode = grpc.StatusCode.RESOURCE_EXHAUSTED,
        message: str = RECEIVE_LIMIT_MESSAGE,
    ) -> None:
        self.max_blobs = max_blobs
        self.code = code
        self.message = message
        self.requested_blob_ids: list[list[str]] = []

    @property
    def call_sizes(self) -> list[int]:
        return [len(blob_ids) for blob_ids in self.requested_blob_ids]

    def LoadBlobs(self, request: pb.LoadBlobsRequest) -> pb.LoadBlobsResponse:
        blob_ids = record_request(self.requested_blob_ids, request)
        if len(blob_ids) > self.max_blobs:
            raise FakeRpcError(self.code, self.message)
        response = pb.LoadBlobsResponse()
        for blob_id in blob_ids:
            response.values[blob_id].CopyFrom(pb.Value(string_value=f"value-{blob_id}"))
        return response


class AsyncLimitedBlobService(LimitedBlobService):
    async def LoadBlobs(  # type: ignore[override]
        self, request: pb.LoadBlobsRequest
    ) -> pb.LoadBlobsResponse:
        return super().LoadBlobs(request)


class FailingBlobService:
    """Fail every LoadBlobs call with the same error."""

    def __init__(self, error: grpc.RpcError) -> None:
        self.error = error
        self.requested_blob_ids: list[list[str]] = []

    def LoadBlobs(self, request: pb.LoadBlobsRequest) -> pb.LoadBlobsResponse:
        record_request(self.requested_blob_ids, request)
        raise self.error


def record_request(
    requested_blob_ids: list[list[str]], request: pb.LoadBlobsRequest
) -> list[str]:
    blob_ids = [
        entry.blob_value.internal_blob_id_for_string_value for entry in request.entries
    ]
    requested_blob_ids.append(blob_ids)
    return blob_ids


def blob_values(count: int) -> list[pb.Value]:
    return [
        pb.Value(internal_blob_id_for_string_value=f"blob-{index}")
        for index in range(count)
    ]


def open_cache(tmp_path: Path) -> BlobCache:
    return open_blob_cache(BlobCacheConfig(str(tmp_path), 1 << 20))


def test_default_receive_limit_matches_go_and_keeps_send_unlimited() -> None:
    assert DEFAULT_GRPC_MAX_MESSAGE_BYTES == 32 << 20
    assert grpc_message_size_channel_options(0) == (
        ("grpc.max_receive_message_length", 32 << 20),
    )


def test_configured_limit_applies_to_both_directions() -> None:
    assert grpc_message_size_channel_options(1024) == (
        ("grpc.max_receive_message_length", 1024),
        ("grpc.max_send_message_length", 1024),
    )


@pytest.mark.parametrize("options_type", [ClientOptions, WorkerOptions])
def test_negative_limit_is_rejected(options_type: Any) -> None:
    with pytest.raises(ValueError, match="must not be negative"):
        options_type(grpc_max_message_bytes=-1)


@pytest.mark.parametrize("code, message", TRANSPORT_SIZE_FAILURES)
def test_a_detail_free_size_diagnostic_is_a_size_error(
    code: grpc.StatusCode, message: str
) -> None:
    assert is_grpc_message_size_error(FakeRpcError(code, message))


@pytest.mark.parametrize(
    "error",
    [
        pytest.param(
            FakeRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED, "Bandwidth exhausted"),
            id="flow-control-resource-exhausted",
        ),
        pytest.param(
            FakeRpcError(
                grpc.StatusCode.RESOURCE_EXHAUSTED,
                "Frame size 20000 is larger than max frame size 16384",
            ),
            id="frame-size",
        ),
        pytest.param(
            FakeRpcError(grpc.StatusCode.OUT_OF_RANGE, "page token out of range"),
            id="other-out-of-range",
        ),
        pytest.param(
            FakeRpcError(
                grpc.StatusCode.OUT_OF_RANGE,
                SERVER_SEND_LIMIT_MESSAGE,
                dex_status_metadata(
                    grpc.StatusCode.OUT_OF_RANGE, SERVER_SEND_LIMIT_MESSAGE
                ),
            ),
            id="dex-details",
        ),
        pytest.param(
            FakeRpcError(grpc.StatusCode.UNAVAILABLE, "down"), id="unavailable"
        ),
        pytest.param(
            FakeRpcError(grpc.StatusCode.INTERNAL, RECEIVE_LIMIT_MESSAGE),
            id="size-text-with-other-code",
        ),
    ],
)
def test_other_failures_are_not_size_errors(error: FakeRpcError) -> None:
    assert not is_grpc_message_size_error(error)


def test_a_real_aio_error_is_classified_by_code_message_and_details() -> None:
    size_error = grpc.aio.AioRpcError(
        grpc.StatusCode.OUT_OF_RANGE,
        grpc.aio.Metadata(),
        grpc.aio.Metadata(),
        details=SERVER_SEND_LIMIT_MESSAGE,
    )
    assert is_grpc_message_size_error(size_error)
    dex_error = grpc.aio.AioRpcError(
        grpc.StatusCode.OUT_OF_RANGE,
        grpc.aio.Metadata(),
        grpc.aio.Metadata(
            *dex_status_metadata(
                grpc.StatusCode.OUT_OF_RANGE, SERVER_SEND_LIMIT_MESSAGE
            )
        ),
        details=SERVER_SEND_LIMIT_MESSAGE,
    )
    assert not is_grpc_message_size_error(dex_error)


@pytest.mark.parametrize("code, message", TRANSPORT_SIZE_FAILURES)
def test_oversized_batch_is_split_in_order_until_it_fits(
    tmp_path: Path, code: grpc.StatusCode, message: str
) -> None:
    service = LimitedBlobService(2, code, message)
    hydrator = ValueHydrator(service, open_cache(tmp_path))  # type: ignore[arg-type]
    hydrated = hydrator.hydrate_all("flow", blob_values(5))
    assert [value.string_value for value in hydrated] == [
        f"value-blob-{index}" for index in range(5)
    ]
    assert service.requested_blob_ids == [
        ["blob-0", "blob-1", "blob-2", "blob-3", "blob-4"],
        ["blob-0", "blob-1"],
        ["blob-2", "blob-3", "blob-4"],
        ["blob-2"],
        ["blob-3", "blob-4"],
    ]


@pytest.mark.parametrize("code, message", TRANSPORT_SIZE_FAILURES)
def test_async_oversized_batch_is_split_until_it_fits(
    tmp_path: Path, code: grpc.StatusCode, message: str
) -> None:
    service = AsyncLimitedBlobService(1, code, message)
    hydrator = AsyncValueHydrator(service, open_cache(tmp_path))  # type: ignore[arg-type]
    hydrated = asyncio.run(hydrator.hydrate_all("flow", blob_values(4)))
    assert [value.string_value for value in hydrated] == [
        f"value-blob-{index}" for index in range(4)
    ]
    assert len(service.call_sizes) == 2 * 4 - 1


@pytest.mark.parametrize("code, message", SIZE_FAILURES)
def test_one_blob_over_the_limit_names_the_blob_and_limit(
    tmp_path: Path, code: grpc.StatusCode, message: str
) -> None:
    service = LimitedBlobService(0, code, message)
    hydrator = ValueHydrator(service, open_cache(tmp_path))  # type: ignore[arg-type]
    with pytest.raises(BlobMessageTooLargeError) as raised:
        hydrator.hydrate_all("flow", blob_values(1))
    assert raised.value.blob_id == "blob-0"
    assert raised.value.flow_id == "flow"
    assert raised.value.code is grpc.StatusCode.RESOURCE_EXHAUSTED
    assert message in str(raised.value)
    assert cast(grpc.Call, raised.value.__cause__).code() is code
    assert service.call_sizes == [1]


@pytest.mark.parametrize("code, message", SIZE_FAILURES)
def test_async_one_blob_over_the_limit_names_the_blob(
    tmp_path: Path, code: grpc.StatusCode, message: str
) -> None:
    service = AsyncLimitedBlobService(0, code, message)
    hydrator = AsyncValueHydrator(service, open_cache(tmp_path))  # type: ignore[arg-type]
    with pytest.raises(BlobMessageTooLargeError) as raised:
        asyncio.run(hydrator.hydrate_all("flow", blob_values(1)))
    assert raised.value.blob_id == "blob-0"
    assert message in str(raised.value)


@pytest.mark.parametrize(
    "error",
    [
        pytest.param(
            FakeRpcError(grpc.StatusCode.UNAVAILABLE, "down"), id="unavailable"
        ),
        pytest.param(
            FakeRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED, "Bandwidth exhausted"),
            id="non-size-resource-exhausted",
        ),
        pytest.param(
            FakeRpcError(
                grpc.StatusCode.OUT_OF_RANGE,
                SERVER_SEND_LIMIT_MESSAGE,
                dex_status_metadata(
                    grpc.StatusCode.OUT_OF_RANGE, SERVER_SEND_LIMIT_MESSAGE
                ),
            ),
            id="dex-out-of-range",
        ),
    ],
)
def test_other_failures_are_not_split(tmp_path: Path, error: FakeRpcError) -> None:
    service = FailingBlobService(error)
    hydrator = ValueHydrator(service, open_cache(tmp_path))  # type: ignore[arg-type]
    with pytest.raises(grpc.RpcError) as raised:
        hydrator.hydrate_all("flow", blob_values(3))
    assert raised.value is error
    assert [len(blob_ids) for blob_ids in service.requested_blob_ids] == [3]
