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

import asyncio
import os
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import timedelta
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Any

import grpc
import pytest

from dex import (
    AsyncClient,
    Attribute,
    AttributeMapInstance,
    AttributeValues,
    BlobCache,
    BlobCacheConfig,
    Client,
    ClientOptions,
    FlowDefinitionError,
    FlowNotFoundError,
    FlowStatus,
    Registry,
    open_blob_cache,
)
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc as pb_grpc

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .get_attributes_flow import (
    CLOSED_RUN_LARGE_STRING_VALUE,
    CLOSED_RUN_LARGE_VALUE,
    COUNT,
    EMPTY_LABEL,
    INITIAL_COUNT,
    INITIAL_LABEL,
    INITIAL_NOTE,
    LABEL,
    LARGE_STRING_VALUE,
    LARGE_VALUE,
    NOTE,
    QUANTITIES,
    SECRET,
    SECRET_VALUE,
    SET_QUANTITY_INSTANCE,
    UNSET_COUNT,
    UNSET_QUANTITY_INSTANCE,
    ZERO_COUNT,
    GetAttributesFlow,
    get_attributes_start_options,
)
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)
UNREGISTERED = Attribute("get-attributes-unregistered", int)
MISMATCHED_COUNT = Attribute(COUNT.name, str)
SERVER_ADDRESS = os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")


def test_get_attributes_reads_values_and_rejects_invalid_reads() -> None:
    flow = GetAttributesFlow()
    with DexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("get-attributes")
        client.start_flow(flow, flow_id, None, get_attributes_start_options())
        _assert_large_values_are_blob_references(flow_id)

        values = client.get_attributes(flow_id, *_open_run_reads())
        _assert_open_run_values(values)

        with pytest.raises(ValueError, match="duplicate Attribute read") as duplicate:
            client.get_attributes(flow_id, COUNT, COUNT)
        assert flow_id in str(duplicate.value)
        with pytest.raises(ValueError, match="at least one Attribute read") as empty:
            client.get_attributes(flow_id)
        assert flow_id in str(empty.value)
        with pytest.raises(FlowDefinitionError) as unregistered:
            client.get_attributes(flow_id, UNREGISTERED)
        _assert_names(unregistered.value, flow_id, UNREGISTERED.name)
        with pytest.raises(FlowDefinitionError) as mismatched:
            client.get_attributes(flow_id, MISMATCHED_COUNT)
        _assert_names(mismatched.value, flow_id, COUNT.name)

        missing_flow_id = unique_id("get-attributes-missing")
        with pytest.raises(FlowNotFoundError) as missing:
            client.get_attributes(missing_flow_id, COUNT)
        _assert_names(missing.value, missing_flow_id, COUNT.name)

        deleted_flow_id = unique_id("get-attributes-deleted-secret")
        client.start_flow(flow, deleted_flow_id, None, get_attributes_start_options())
        client.invoke_rpc(flow.delete_secret, deleted_flow_id)
        _assert_secret_deleted(client.get_attributes(deleted_flow_id, SECRET, LABEL))
        client.stop_flow(deleted_flow_id)

        client.stop_flow(flow_id)
        result = client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.status is FlowStatus.CANCELED
        with _fresh_blob_cache() as (cache, cache_directory):
            with Client(
                Registry((GetAttributesFlow(),)),
                cache,
                ClientOptions(SERVER_ADDRESS),
            ) as fresh_client:
                closed_values = fresh_client.get_attributes(
                    flow_id, SECRET, CLOSED_RUN_LARGE_VALUE, UNSET_COUNT
                )
            _assert_closed_run_values(closed_values, cache_directory)
        _assert_cache_excludes_secret(Path(environment._cache_directory.name))


def test_async_get_attributes_reads_values_and_rejects_invalid_reads() -> None:
    asyncio.run(_async_get_attributes_reads_values_and_rejects_invalid_reads())


async def _async_get_attributes_reads_values_and_rejects_invalid_reads() -> None:
    flow = GetAttributesFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("async-get-attributes")
        await client.start_flow(flow, flow_id, None, get_attributes_start_options())
        _assert_large_values_are_blob_references(flow_id)

        values = await client.get_attributes(flow_id, *_open_run_reads())
        _assert_open_run_values(values)

        with pytest.raises(ValueError, match="duplicate Attribute read") as duplicate:
            await client.get_attributes(flow_id, COUNT, COUNT)
        assert flow_id in str(duplicate.value)
        with pytest.raises(ValueError, match="at least one Attribute read") as empty:
            await client.get_attributes(flow_id)
        assert flow_id in str(empty.value)
        with pytest.raises(FlowDefinitionError) as unregistered:
            await client.get_attributes(flow_id, UNREGISTERED)
        _assert_names(unregistered.value, flow_id, UNREGISTERED.name)
        with pytest.raises(FlowDefinitionError) as mismatched:
            await client.get_attributes(flow_id, MISMATCHED_COUNT)
        _assert_names(mismatched.value, flow_id, COUNT.name)

        missing_flow_id = unique_id("async-get-attributes-missing")
        with pytest.raises(FlowNotFoundError) as missing:
            await client.get_attributes(missing_flow_id, COUNT)
        _assert_names(missing.value, missing_flow_id, COUNT.name)

        deleted_flow_id = unique_id("async-get-attributes-deleted-secret")
        await client.start_flow(
            flow, deleted_flow_id, None, get_attributes_start_options()
        )
        await client.invoke_rpc(flow.delete_secret, deleted_flow_id)
        _assert_secret_deleted(
            await client.get_attributes(deleted_flow_id, SECRET, LABEL)
        )
        await client.stop_flow(deleted_flow_id)

        await client.stop_flow(flow_id)
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.status is FlowStatus.CANCELED
        with _fresh_blob_cache() as (cache, cache_directory):
            async with AsyncClient(
                Registry((GetAttributesFlow(),)),
                cache,
                ClientOptions(SERVER_ADDRESS),
            ) as fresh_client:
                closed_values = await fresh_client.get_attributes(
                    flow_id, SECRET, CLOSED_RUN_LARGE_VALUE, UNSET_COUNT
                )
            _assert_closed_run_values(closed_values, cache_directory)
        _assert_cache_excludes_secret(Path(environment._cache_directory.name))


def _open_run_reads() -> tuple[Attribute[Any] | AttributeMapInstance[Any], ...]:
    return (
        LABEL,
        COUNT,
        NOTE,
        EMPTY_LABEL,
        ZERO_COUNT,
        UNSET_COUNT,
        QUANTITIES.instance(SET_QUANTITY_INSTANCE),
        QUANTITIES.instance(UNSET_QUANTITY_INSTANCE),
        SECRET,
        LARGE_VALUE,
    )


def _assert_open_run_values(values: AttributeValues) -> None:
    assert values[LABEL] == INITIAL_LABEL
    assert values[COUNT] == INITIAL_COUNT
    assert values[NOTE] == INITIAL_NOTE
    assert values[EMPTY_LABEL] == ""
    assert EMPTY_LABEL in values
    assert values[ZERO_COUNT] == 0
    assert values.get(ZERO_COUNT, -1) == 0
    assert UNSET_COUNT not in values
    assert values.get(UNSET_COUNT) is None
    assert values.get(UNSET_COUNT, -1) == -1
    with pytest.raises(KeyError):
        values[UNSET_COUNT]
    assert values[QUANTITIES.instance(SET_QUANTITY_INSTANCE)] == 0
    assert QUANTITIES.instance(SET_QUANTITY_INSTANCE) in values
    assert QUANTITIES.instance(UNSET_QUANTITY_INSTANCE) not in values
    assert values.get(QUANTITIES.instance(UNSET_QUANTITY_INSTANCE), -1) == -1
    assert values[SECRET] == SECRET_VALUE
    assert values[LARGE_VALUE] == LARGE_STRING_VALUE
    assert CLOSED_RUN_LARGE_VALUE not in values
    assert SECRET_VALUE not in repr(values)


def _assert_secret_deleted(values: AttributeValues) -> None:
    assert SECRET not in values
    assert values.get(SECRET) is None
    assert values[LABEL] == INITIAL_LABEL


def _assert_closed_run_values(values: AttributeValues, cache_directory: Path) -> None:
    assert values[SECRET] == SECRET_VALUE
    assert values[CLOSED_RUN_LARGE_VALUE] == CLOSED_RUN_LARGE_STRING_VALUE
    assert UNSET_COUNT not in values
    cached_payloads = [
        path.read_bytes() for path in cache_directory.rglob("*") if path.is_file()
    ]
    assert any(
        CLOSED_RUN_LARGE_STRING_VALUE.encode("utf-8") in payload
        for payload in cached_payloads
    ), "the closed-run large value was not loaded through LoadBlobs"
    _assert_cache_excludes_secret(cache_directory)


def _assert_large_values_are_blob_references(flow_id: str) -> None:
    with grpc.insecure_channel(SERVER_ADDRESS) as channel:
        service = pb_grpc.FlowServiceStub(channel)  # type: ignore[no-untyped-call]
        response = service.GetAttributes(
            pb.GetAttributesRequest(
                flow_id=flow_id,
                keys=[LARGE_VALUE.name, CLOSED_RUN_LARGE_VALUE.name, SECRET.name],
            )
        )
    kinds = {entry.key: entry.value.WhichOneof("kind") for entry in response.attributes}
    assert kinds == {
        LARGE_VALUE.name: "internal_blob_id_for_string_value",
        CLOSED_RUN_LARGE_VALUE.name: "internal_blob_id_for_string_value",
        SECRET.name: "string_value",
    }


def _assert_names(error: BaseException, flow_id: str, attribute_name: str) -> None:
    message = str(error)
    assert flow_id in message
    assert attribute_name in message


def _assert_cache_excludes_secret(cache_directory: Path) -> None:
    for path in cache_directory.rglob("*"):
        if path.is_file():
            assert SECRET_VALUE.encode("utf-8") not in path.read_bytes(), path


@contextmanager
def _fresh_blob_cache() -> Iterator[tuple[BlobCache, Path]]:
    with TemporaryDirectory(prefix="dex-python-get-attributes-cache-") as directory:
        cache = open_blob_cache(BlobCacheConfig(directory, 64 * 1024 * 1024))
        try:
            yield cache, Path(directory)
        finally:
            cache.close()
