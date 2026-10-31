# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import asyncio
from pathlib import Path
from typing import Any, cast

import grpc
import pytest
from google.protobuf import any_pb2, struct_pb2
from google.rpc import status_pb2

from dex import (
    AsyncClient,
    Attribute,
    AttributeMap,
    AttributeMapInstance,
    AttributeValues,
    BlobCache,
    BlobCacheConfig,
    Client,
    Flow,
    FlowDefinitionError,
    FlowNotFoundError,
    PersistenceSchema,
    Registry,
    ValueMappingError,
    open_blob_cache,
)
from dex.dexpb import dex_pb2 as pb

STATUS = Attribute("status", str)
COUNT = Attribute("count", int)
NOTES = AttributeMap("notes", str)
SECRET = Attribute("secret", str, hard_delete_on_null=True)


class GetAttributesContractFlow(Flow[None]):
    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(STATUS, COUNT, NOTES, SECRET)


class GetAttributesService:
    def __init__(self, response: pb.GetAttributesResponse) -> None:
        self.response = response
        self.requests: list[pb.GetAttributesRequest] = []

    def GetAttributes(self, request: pb.GetAttributesRequest) -> Any:
        self.requests.append(request)
        return self.response


class AsyncGetAttributesService(GetAttributesService):
    async def GetAttributes(self, request: pb.GetAttributesRequest) -> Any:
        self.requests.append(request)
        return self.response


def _client(response: pb.GetAttributesResponse) -> tuple[Client, GetAttributesService]:
    client = Client(Registry((GetAttributesContractFlow(),)), cast(BlobCache, object()))
    service = GetAttributesService(response)
    client._service = cast(Any, service)
    return client, service


def _response(*entries: tuple[str, pb.Value | None]) -> pb.GetAttributesResponse:
    response = pb.GetAttributesResponse()
    for key, value in entries:
        entry = response.attributes.add(key=key)
        if value is not None:
            entry.value.CopyFrom(value)
    return response


def test_attribute_map_instance_validates_and_compares_by_value() -> None:
    assert NOTES.instance("n1") == AttributeMapInstance(NOTES, "n1")
    assert hash(NOTES.instance("n1")) == hash(AttributeMapInstance(NOTES, "n1"))
    assert NOTES.instance("n1") != NOTES.instance("n2")
    with pytest.raises(ValueError, match="must not contain"):
        NOTES.instance("a/b")
    with pytest.raises(ValueError, match="required"):
        NOTES.instance("")
    with pytest.raises(TypeError, match="requires an AttributeMap"):
        AttributeMapInstance(cast(Any, STATUS), "n1")
    with pytest.raises(TypeError, match="instance must be a string"):
        NOTES.instance(cast(Any, 5))


def test_get_attributes_sends_one_request_and_reports_set_and_unset_reads() -> None:
    client, service = _client(
        _response(
            ("status", pb.Value(string_value="")),
            ("notes/n%201", pb.Value(string_value="first")),
            (
                "secret",
                pb.Value(null_value=cast(struct_pb2.NullValue, struct_pb2.NULL_VALUE)),
            ),
        )
    )
    try:
        values = client.get_attributes(
            "flow-1", STATUS, COUNT, NOTES.instance("n 1"), NOTES.instance("n2"), SECRET
        )
    finally:
        client.close()
    assert len(service.requests) == 1
    assert service.requests[0].flow_id == "flow-1"
    assert tuple(service.requests[0].keys) == (
        "status",
        "count",
        "notes/n%201",
        "notes/n2",
        "secret",
    )
    assert values[STATUS] == ""
    assert STATUS in values
    assert Attribute("status", str) in values
    assert values[NOTES.instance("n 1")] == "first"
    assert SECRET in values
    assert values[SECRET] is None
    assert values.get(SECRET, "default") is None
    assert COUNT not in values
    assert values.get(COUNT) is None
    assert values.get(COUNT, 5) == 5
    with pytest.raises(KeyError, match="count"):
        values[COUNT]
    assert NOTES.instance("n2") not in values
    with pytest.raises(KeyError, match="notes/n%202"):
        values[NOTES.instance("n 2")]
    with pytest.raises(TypeError, match="not iterable"):
        iter(cast(Any, values))
    unread = Attribute("unread", str)
    assert unread not in values
    assert values.get(unread, "fallback") == "fallback"
    assert cast(object, "status") not in values
    assert "first" not in repr(values)
    assert "'status'" in repr(values)
    assert "'notes/n%201'" in repr(values)
    assert STATUS not in AttributeValues()


def test_get_attributes_rejects_invalid_reads_before_transport() -> None:
    client, service = _client(pb.GetAttributesResponse())
    try:
        with pytest.raises(ValueError, match="non-empty Flow ID"):
            client.get_attributes("", STATUS)
        with pytest.raises(ValueError, match="'flow-1' requires at least one"):
            client.get_attributes("flow-1")
        with pytest.raises(ValueError, match="duplicate Attribute read 'notes/n1'"):
            client.get_attributes("flow-1", NOTES.instance("n1"), NOTES.instance("n1"))
        with pytest.raises(TypeError, match="AttributeMap 'notes' needs an instance"):
            client.get_attributes("flow-1", cast(Any, NOTES))
        with pytest.raises(TypeError, match="AttributeMapLoad"):
            client.get_attributes("flow-1", cast(Any, NOTES.load("n1")))
        with pytest.raises(
            FlowDefinitionError, match="'flow-1' Attribute 'unknown'.*not registered"
        ):
            client.get_attributes("flow-1", Attribute("unknown", str))
        with pytest.raises(
            FlowDefinitionError, match="'flow-1' Attribute 'status'.*does not match"
        ):
            client.get_attributes("flow-1", Attribute("status", int))
        with pytest.raises(FlowDefinitionError, match="does not match"):
            client.get_attributes("flow-1", Attribute("secret", str))
        with pytest.raises(FlowDefinitionError, match="does not match"):
            client.get_attributes("flow-1", AttributeMap("status", str).instance("x"))
    finally:
        client.close()
    assert service.requests == []


@pytest.mark.parametrize(
    ("response", "message"),
    [
        (
            _response(("other", pb.Value(string_value="x"))),
            "unexpected Attribute 'other'",
        ),
        (
            _response(
                ("status", pb.Value(string_value="a")),
                ("status", pb.Value(string_value="b")),
            ),
            "duplicate Attribute 'status'",
        ),
        (_response(("status", None)), "Attribute 'status' without a value"),
    ],
)
def test_get_attributes_rejects_invalid_responses(
    response: pb.GetAttributesResponse, message: str
) -> None:
    client, _ = _client(response)
    try:
        with pytest.raises(ValueError, match=f"'flow-1' returned {message}"):
            client.get_attributes("flow-1", STATUS)
    finally:
        client.close()


def test_get_attributes_decode_failure_names_flow_and_key() -> None:
    client, _ = _client(
        _response(
            ("status", pb.Value(string_value="ok")),
            ("count", pb.Value(string_value="not-an-int")),
        )
    )
    try:
        with pytest.raises(ValueMappingError, match="'flow-1' Attribute 'count'"):
            client.get_attributes("flow-1", STATUS, COUNT)
    finally:
        client.close()


def test_async_get_attributes_shares_validation_and_decoding() -> None:
    asyncio.run(_assert_async_get_attributes())


async def _assert_async_get_attributes() -> None:
    client = AsyncClient(
        Registry((GetAttributesContractFlow(),)), cast(BlobCache, object())
    )
    service = AsyncGetAttributesService(
        _response(("notes/n1", pb.Value(string_value="first")))
    )
    client._service = cast(Any, service)
    try:
        values = await client.get_attributes("flow-1", NOTES.instance("n1"), COUNT)
        assert values[NOTES.instance("n1")] == "first"
        assert COUNT not in values
        with pytest.raises(ValueError, match="duplicate Attribute read 'count'"):
            await client.get_attributes("flow-1", COUNT, COUNT)
        assert len(service.requests) == 1
        service.response = _response(("count", pb.Value(string_value="x")))
        with pytest.raises(ValueMappingError, match="Attribute 'count'"):
            await client.get_attributes("flow-1", COUNT)
    finally:
        await client.close()


BLOB_ID = "blob-1"
BLOB_REFERENCE = pb.Value(internal_blob_id_for_string_value=BLOB_ID)
LARGE_STATUS = "large-status-" * 16


class FlowNotExistsRpcError(grpc.RpcError):
    def code(self) -> grpc.StatusCode:
        return grpc.StatusCode.NOT_FOUND

    def details(self) -> str:
        return "server detail"

    def trailing_metadata(self) -> tuple[Any, ...]:
        packed = any_pb2.Any()
        packed.Pack(
            pb.ServiceErrorResponse(
                detail="server detail",
                sub_status=pb.ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
            )
        )
        status = status_pb2.Status(
            code=grpc.StatusCode.NOT_FOUND.value[0],
            message="server detail",
            details=[packed],
        )
        return (("grpc-status-details-bin", status.SerializeToString()),)


class BlobService:
    def __init__(self) -> None:
        self.get_attributes_error: grpc.RpcError | None = None
        self.load_blobs_error: grpc.RpcError | None = None
        self.loaded_values: dict[str, pb.Value] = {
            BLOB_ID: pb.Value(string_value=LARGE_STATUS)
        }
        self.load_requests: list[pb.LoadBlobsRequest] = []

    def GetAttributes(self, request: pb.GetAttributesRequest) -> Any:
        if self.get_attributes_error is not None:
            raise self.get_attributes_error
        return _response(("status", BLOB_REFERENCE))

    def LoadBlobs(self, request: pb.LoadBlobsRequest) -> Any:
        self.load_requests.append(request)
        if self.load_blobs_error is not None:
            raise self.load_blobs_error
        return pb.LoadBlobsResponse(values=self.loaded_values)


class AsyncBlobService(BlobService):
    async def GetAttributes(self, request: pb.GetAttributesRequest) -> Any:
        return BlobService.GetAttributes(self, request)

    async def LoadBlobs(self, request: pb.LoadBlobsRequest) -> Any:
        return BlobService.LoadBlobs(self, request)


def test_get_attributes_hydrates_blobs_and_maps_service_errors(
    tmp_path: Path,
) -> None:
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path), 1024 * 1024))
    client = Client(Registry((GetAttributesContractFlow(),)), cache)
    service = BlobService()
    client._service = cast(Any, service)
    client._hydrator._service = cast(Any, service)
    try:
        assert client.get_attributes("flow-1", STATUS)[STATUS] == LARGE_STATUS
        assert client.get_attributes("flow-1", STATUS)[STATUS] == LARGE_STATUS
        assert len(service.load_requests) == 1
        assert service.load_requests[0].entries[0].flow_id == "flow-1"

        service.loaded_values = {}
        with pytest.raises(ValueMappingError) as omitted:
            client.get_attributes("flow-2", STATUS)
        _assert_blob_omitted(omitted.value)

        service.load_blobs_error = FlowNotExistsRpcError()
        with pytest.raises(FlowNotFoundError) as load_failure:
            client.get_attributes("flow-3", STATUS)
        _assert_flow_not_found(load_failure.value, "flow-3", "LoadBlobs")

        service.get_attributes_error = FlowNotExistsRpcError()
        with pytest.raises(FlowNotFoundError) as read_failure:
            client.get_attributes("flow-4", STATUS)
        _assert_flow_not_found(read_failure.value, "flow-4", "GetAttributes")
    finally:
        client.close()
        cache.close()


def test_get_attributes_reports_unresolvable_inline_values() -> None:
    client, _ = _client(_response(("status", pb.Value())))
    try:
        with pytest.raises(ValueMappingError, match="returned a value that cannot"):
            client.get_attributes("flow-1", STATUS)
    finally:
        client.close()


def test_async_get_attributes_hydrates_blobs_and_maps_service_errors(
    tmp_path: Path,
) -> None:
    asyncio.run(_assert_async_get_attributes_blobs_and_errors(tmp_path))


async def _assert_async_get_attributes_blobs_and_errors(tmp_path: Path) -> None:
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path), 1024 * 1024))
    client = AsyncClient(Registry((GetAttributesContractFlow(),)), cache)
    service = AsyncBlobService()
    client._service = cast(Any, service)
    client._hydrator._service = cast(Any, service)
    try:
        assert (await client.get_attributes("flow-1", STATUS))[STATUS] == LARGE_STATUS
        assert (await client.get_attributes("flow-1", STATUS))[STATUS] == LARGE_STATUS
        assert len(service.load_requests) == 1

        service.loaded_values = {}
        with pytest.raises(ValueMappingError) as omitted:
            await client.get_attributes("flow-2", STATUS)
        _assert_blob_omitted(omitted.value)

        service.load_blobs_error = FlowNotExistsRpcError()
        with pytest.raises(FlowNotFoundError) as load_failure:
            await client.get_attributes("flow-3", STATUS)
        _assert_flow_not_found(load_failure.value, "flow-3", "LoadBlobs")

        service.get_attributes_error = FlowNotExistsRpcError()
        with pytest.raises(FlowNotFoundError) as read_failure:
            await client.get_attributes("flow-4", STATUS)
        _assert_flow_not_found(read_failure.value, "flow-4", "GetAttributes")
    finally:
        await client.close()
        cache.close()


def _assert_blob_omitted(error: ValueMappingError) -> None:
    message = str(error)
    assert "'flow-2'" in message
    assert "'status'" in message
    assert f"LoadBlobs omitted blob {BLOB_ID}" in message


def _assert_flow_not_found(
    error: FlowNotFoundError, flow_id: str, service_method: str
) -> None:
    assert error.detail == "server detail"
    assert error.flow_id == flow_id
    assert error.operation == "get_attributes"
    message = str(error)
    assert f"'{flow_id}'" in message
    assert "'status'" in message
    assert f"{service_method} failed: server detail" in message
