# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Worker serves RPC requests whose input is unset, as released TypeScript Clients send."""

from __future__ import annotations

import asyncio
from typing import Any, cast

import pytest

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    Registry,
    RPCResult,
    StepList,
    rpc,
)
from dex._async_value_hydrator import AsyncValueHydrator
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex._server_protocol import NegotiatedServerProtocol
from dex._value_hydrator import ValueHydrator
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb
from dex.runtime_errors import ValueMappingError

NOTE = Attribute("note", str)


class NoteFlow(Flow[None]):
    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(NOTE)

    @rpc
    def get_note(self, context: Context) -> RPCResult[str]:
        return RPCResult(NOTE.get(context) or "")

    @rpc
    def set_note(self, context: Context, note: str) -> RPCResult[str]:
        NOTE.set(context, note)
        return RPCResult(note)


def _server_protocol() -> NegotiatedServerProtocol:
    server_protocol = NegotiatedServerProtocol()
    server_protocol.record_version(4)
    return server_protocol


def _request(
    values: ValueMapper,
    rpc_name: str,
    input: pb.Value | None = None,
) -> pb.InvokeWorkerRPCRequest:
    request = pb.InvokeWorkerRPCRequest(
        flow_type="NoteFlow",
        rpc_name=rpc_name,
        context=pb.Context(flow_id="note-flow", run_id="run"),
        attributes=[pb.KV(key="note", value=values.encode("saved", values.codec(str)))],
    )
    if input is not None:
        request.input.CopyFrom(input)
    return request


def _values() -> tuple[Registry, ValueMapper]:
    registry = Registry((NoteFlow(),))
    return registry, ValueMapper(registry.codec_registry)


def _sync_dispatcher() -> tuple[WorkerDispatcher, ValueMapper]:
    registry, values = _values()
    hydrator = ValueHydrator(cast(Any, None), cast(Any, None))
    return WorkerDispatcher(registry, values, hydrator, _server_protocol()), values


def _async_dispatcher() -> tuple[AsyncWorkerDispatcher, ValueMapper]:
    registry, values = _values()
    hydrator = AsyncValueHydrator(cast(Any, None), cast(Any, None))
    return (
        AsyncWorkerDispatcher(registry, values, hydrator, _server_protocol()),
        values,
    )


def test_hydrator_leaves_an_unset_rpc_input_unset() -> None:
    _, values = _values()
    request = _request(values, "get_note")
    hydrated = ValueHydrator(cast(Any, None), cast(Any, None)).rpc_request(request)
    assert not hydrated.HasField("input")
    assert hydrated.attributes[0].value == request.attributes[0].value
    hydrated = asyncio.run(
        AsyncValueHydrator(cast(Any, None), cast(Any, None)).rpc_request(request)
    )
    assert not hydrated.HasField("input")
    assert hydrated.attributes[0].value == request.attributes[0].value


def test_worker_serves_an_input_free_rpc_with_unset_input() -> None:
    dispatcher, values = _sync_dispatcher()
    response = dispatcher.invoke_rpc(_request(values, "get_note"))
    assert values.decode(response.output, values.codec(str)) == "saved"


def test_async_worker_serves_an_input_free_rpc_with_unset_input() -> None:
    dispatcher, values = _async_dispatcher()
    response = asyncio.run(dispatcher.invoke_rpc(_request(values, "get_note")))
    assert values.decode(response.output, values.codec(str)) == "saved"


def test_worker_serves_an_input_free_rpc_with_null_input() -> None:
    dispatcher, values = _sync_dispatcher()
    response = dispatcher.invoke_rpc(
        _request(values, "get_note", values.encode_dynamic(None))
    )
    assert values.decode(response.output, values.codec(str)) == "saved"


def test_worker_rejects_an_input_bearing_rpc_with_unset_input() -> None:
    dispatcher, values = _sync_dispatcher()
    with pytest.raises(ValueMappingError, match="'set_note' request is missing"):
        dispatcher.invoke_rpc(_request(values, "set_note"))
    async_dispatcher, _ = _async_dispatcher()
    with pytest.raises(ValueMappingError, match="'set_note' request is missing"):
        asyncio.run(async_dispatcher.invoke_rpc(_request(values, "set_note")))


def test_worker_decodes_an_input_bearing_rpc_input() -> None:
    dispatcher, values = _sync_dispatcher()
    input = values.encode("changed", values.codec(str))
    response = dispatcher.invoke_rpc(_request(values, "set_note", input))
    assert values.decode(response.output, values.codec(str)) == "changed"
