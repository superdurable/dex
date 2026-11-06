# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import os
import threading
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import timedelta
from tempfile import TemporaryDirectory

import pytest

from dex import (
    Attribute,
    BlobCache,
    BlobCacheConfig,
    BlobMessageTooLargeError,
    Client,
    ClientOptions,
    Context,
    Flow,
    FlowStatus,
    PersistenceSchema,
    Registry,
    RPCResult,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    Worker,
    WorkerOptions,
    graceful_complete,
    open_blob_cache,
    rpc,
)

from .shared import unique_id

SERVER_ADDRESS = os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")
WAIT_TIMEOUT = timedelta(seconds=30)
VALUE_BYTES = 2 << 20
LARGE_VALUES = tuple(Attribute(f"grpc-size-value-{index}", str) for index in range(4))
FIVE_MIB_TEXT = "r" * (5 << 20)


class SumValueLengths(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(sum(len(value.get(context)) for value in LARGE_VALUES))


class GrpcMessageSizeFlow(Flow[None]):
    def __init__(self) -> None:
        self.sum_value_lengths = SumValueLengths()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.sum_value_lengths)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(*LARGE_VALUES)


class LargeRpcResultFlow(Flow[None]):
    def get_steps(self) -> StepList[None]:
        return StepList.without_start_step()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()

    @rpc
    def read_large_text(self, context: Context) -> RPCResult[str]:
        return RPCResult(FIVE_MIB_TEXT)


@contextmanager
def fresh_blob_cache() -> Iterator[BlobCache]:
    with TemporaryDirectory(prefix="dex-python-message-size-") as directory:
        cache = open_blob_cache(BlobCacheConfig(directory, 64 << 20))
        try:
            yield cache
        finally:
            cache.close()


@contextmanager
def running_worker(flow: Flow[None], max_message_bytes: int = 0) -> Iterator[Registry]:
    registry = Registry((flow,))
    with fresh_blob_cache() as cache:
        worker = Worker(
            registry,
            cache,
            WorkerOptions(
                server_address=SERVER_ADDRESS,
                grpc_max_message_bytes=max_message_bytes,
            ),
        )
        thread = threading.Thread(target=worker.start, name="dex-message-size-worker")
        thread.start()
        worker.wait_pull_registered(timeout=30)
        try:
            yield registry
        finally:
            worker.close()
            thread.join(timeout=10)


def start_options_with_large_values() -> StartFlowOptions:
    options = StartFlowOptions()
    for index, value in enumerate(LARGE_VALUES):
        options = options.with_attribute(value, str(index) * VALUE_BYTES)
    return options


def test_default_client_receives_a_response_over_four_mib() -> None:
    flow = LargeRpcResultFlow()
    with running_worker(flow) as registry, fresh_blob_cache() as cache:
        with Client(registry, cache, ClientOptions(SERVER_ADDRESS)) as client:
            flow_id = unique_id("grpc-size-rpc")
            client.start_flow(flow, flow_id, None)
            assert client.invoke_rpc(flow.read_large_text, flow_id) == FIVE_MIB_TEXT
            client.stop_flow(flow_id)


def test_worker_with_an_empty_cache_loads_blobs_over_several_calls() -> None:
    flow = GrpcMessageSizeFlow()
    limit = 3 << 20
    with running_worker(flow, limit) as registry, fresh_blob_cache() as cache:
        with Client(registry, cache, ClientOptions(SERVER_ADDRESS)) as client:
            flow_id = unique_id("grpc-size-worker")
            client.start_flow(flow, flow_id, None, start_options_with_large_values())
            result = client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED


def test_client_limit_smaller_than_one_blob_names_the_blob() -> None:
    flow = GrpcMessageSizeFlow()
    with running_worker(flow) as registry, fresh_blob_cache() as cache:
        with Client(registry, cache, ClientOptions(SERVER_ADDRESS)) as writer:
            flow_id = unique_id("grpc-size-too-large")
            writer.start_flow(flow, flow_id, None, start_options_with_large_values())
            writer.wait_for_flow(flow_id, WAIT_TIMEOUT)
    with fresh_blob_cache() as cache:
        options = ClientOptions(SERVER_ADDRESS, grpc_max_message_bytes=1 << 20)
        with Client(Registry((GrpcMessageSizeFlow(),)), cache, options) as client:
            with pytest.raises(BlobMessageTooLargeError) as raised:
                client.get_attributes(flow_id, LARGE_VALUES[0])
    assert raised.value.flow_id == flow_id
    assert raised.value.blob_id
    assert "1048576" in str(raised.value)
