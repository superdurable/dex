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
from typing import Any

import grpc
import pytest

from dex import (
    DEFAULT_GRPC_MAX_MESSAGE_BYTES,
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


class FakeRpcError(grpc.RpcError):
    def __init__(self, code: grpc.StatusCode, detail: str) -> None:
        super().__init__(detail)
        self._code = code
        self._detail = detail

    def code(self) -> grpc.StatusCode:
        return self._code

    def details(self) -> str:
        return self._detail

    def trailing_metadata(self) -> tuple[Any, ...]:
        return ()


class LimitedBlobService:
    """Reject any LoadBlobs call that asks for more than max_blobs blobs."""

    def __init__(self, max_blobs: int) -> None:
        self.max_blobs = max_blobs
        self.call_sizes: list[int] = []

    def LoadBlobs(self, request: pb.LoadBlobsRequest) -> pb.LoadBlobsResponse:
        self.call_sizes.append(len(request.entries))
        if len(request.entries) > self.max_blobs:
            raise FakeRpcError(
                grpc.StatusCode.RESOURCE_EXHAUSTED,
                "Received message larger than max (9 vs. 5)",
            )
        response = pb.LoadBlobsResponse()
        for entry in request.entries:
            blob_id = entry.blob_value.internal_blob_id_for_string_value
            response.values[blob_id].CopyFrom(pb.Value(string_value=f"value-{blob_id}"))
        return response


class AsyncLimitedBlobService(LimitedBlobService):
    async def LoadBlobs(  # type: ignore[override]
        self, request: pb.LoadBlobsRequest
    ) -> pb.LoadBlobsResponse:
        return super().LoadBlobs(request)


def blob_values(count: int) -> list[pb.Value]:
    return [
        pb.Value(internal_blob_id_for_string_value=f"blob-{index}")
        for index in range(count)
    ]


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


def test_only_a_detail_free_resource_exhausted_is_a_size_error() -> None:
    assert is_grpc_message_size_error(
        FakeRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED, "too large")
    )
    assert not is_grpc_message_size_error(
        FakeRpcError(grpc.StatusCode.UNAVAILABLE, "down")
    )


def test_oversized_batch_is_split_until_it_fits(tmp_path: Path) -> None:
    service = LimitedBlobService(max_blobs=2)
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path), 1 << 20))
    hydrated = ValueHydrator(service, cache).hydrate_all("flow", blob_values(5))  # type: ignore[arg-type]
    assert [value.string_value for value in hydrated] == [
        f"value-blob-{index}" for index in range(5)
    ]
    assert service.call_sizes[0] == 5
    assert max(service.call_sizes[1:]) > 1
    assert len(service.call_sizes) <= 2 * 5 - 1


def test_async_oversized_batch_is_split_until_it_fits(tmp_path: Path) -> None:
    service = AsyncLimitedBlobService(max_blobs=1)
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path), 1 << 20))
    hydrator = AsyncValueHydrator(service, cache)  # type: ignore[arg-type]
    hydrated = asyncio.run(hydrator.hydrate_all("flow", blob_values(4)))
    assert len(hydrated) == 4
    assert len(service.call_sizes) == 2 * 4 - 1


def test_one_blob_over_the_limit_names_the_blob_and_limit(tmp_path: Path) -> None:
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path), 1 << 20))
    hydrator = ValueHydrator(LimitedBlobService(max_blobs=0), cache)  # type: ignore[arg-type]
    with pytest.raises(BlobMessageTooLargeError) as raised:
        hydrator.hydrate_all("flow", blob_values(1))
    assert raised.value.blob_id == "blob-0"
    assert raised.value.flow_id == "flow"
    assert raised.value.code is grpc.StatusCode.RESOURCE_EXHAUSTED
    assert "larger than max" in str(raised.value)


def test_other_failures_are_not_split(tmp_path: Path) -> None:
    class UnavailableService(LimitedBlobService):
        def LoadBlobs(self, request: pb.LoadBlobsRequest) -> pb.LoadBlobsResponse:
            self.call_sizes.append(len(request.entries))
            raise FakeRpcError(grpc.StatusCode.UNAVAILABLE, "down")

    service = UnavailableService(max_blobs=1)
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path), 1 << 20))
    with pytest.raises(grpc.RpcError):
        ValueHydrator(service, cache).hydrate_all("flow", blob_values(3))  # type: ignore[arg-type]
    assert service.call_sizes == [3]
