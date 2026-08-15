# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import logging
from dataclasses import dataclass, field

from dex.blob_cache import BlobCache
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc

_LOGGER = logging.getLogger(__name__)


@dataclass
class _PendingBlob:
    blob_id: str
    is_object: bool
    request: pb.Value
    indexes: list[int] = field(default_factory=list)
    hydrated: pb.Value | None = None


class AsyncValueHydrator:
    def __init__(
        self,
        service: dex_pb2_grpc.FlowServiceStub,
        cache: BlobCache,
    ) -> None:
        self._service = service
        self._cache = cache

    async def hydrate(self, value: pb.Value) -> pb.Value:
        return (await self.hydrate_all([value]))[0]

    async def hydrate_all(self, values: list[pb.Value]) -> list[pb.Value]:
        hydrated = list(values)
        pending: dict[tuple[str, bool], _PendingBlob] = {}
        for index, value in enumerate(values):
            key = self._blob_key(value)
            if key is None:
                self._validate_concrete(value)
                continue
            blob = pending.get(key)
            if blob is None:
                blob = _PendingBlob(key[0], key[1], value)
                pending[key] = blob
            blob.indexes.append(index)

        misses: list[_PendingBlob] = []
        for blob in pending.values():
            concrete = self._read_cache(blob)
            if concrete is None:
                misses.append(blob)
            else:
                blob.hydrated = concrete
        await self._load_misses(misses)

        for blob in pending.values():
            if blob.hydrated is None:
                raise RuntimeError(f"blob was not hydrated: {blob.blob_id}")
            for index in blob.indexes:
                hydrated[index] = blob.hydrated
        return hydrated

    async def wait_for_request(
        self,
        request: pb.InvokeWaitForMethodRequest,
    ) -> pb.InvokeWaitForMethodRequest:
        result = pb.InvokeWaitForMethodRequest()
        result.CopyFrom(request)
        values = [request.step_input, *(entry.value for entry in request.attributes)]
        hydrated = await self.hydrate_all(values)
        result.step_input.CopyFrom(hydrated[0])
        for entry, value in zip(result.attributes, hydrated[1:]):
            entry.value.CopyFrom(value)
        return result

    async def execute_request(
        self,
        request: pb.InvokeExecuteMethodRequest,
    ) -> pb.InvokeExecuteMethodRequest:
        result = pb.InvokeExecuteMethodRequest()
        result.CopyFrom(request)
        has_step_input = request.HasField("step_input")
        values = [request.step_input] if has_step_input else []
        values.extend(entry.value for entry in request.attributes)
        values.extend(entry.value for entry in request.step_exe_locals)
        for channel_result in request.condition_results.channel_results:
            values.extend(channel_result.values)
        hydrated = iter(await self.hydrate_all(values))
        if has_step_input:
            result.step_input.CopyFrom(next(hydrated))
        for entry in result.attributes:
            entry.value.CopyFrom(next(hydrated))
        for entry in result.step_exe_locals:
            entry.value.CopyFrom(next(hydrated))
        for channel_result in result.condition_results.channel_results:
            for value in channel_result.values:
                value.CopyFrom(next(hydrated))
        return result

    async def rpc_request(
        self,
        request: pb.InvokeWorkerRPCRequest,
    ) -> pb.InvokeWorkerRPCRequest:
        result = pb.InvokeWorkerRPCRequest()
        result.CopyFrom(request)
        values = [request.input, *(entry.value for entry in request.attributes)]
        hydrated = await self.hydrate_all(values)
        result.input.CopyFrom(hydrated[0])
        for entry, value in zip(result.attributes, hydrated[1:]):
            entry.value.CopyFrom(value)
        return result

    async def step_outputs(
        self,
        outputs: list[pb.StepCompletionOutput],
    ) -> list[pb.StepCompletionOutput]:
        values = [output.completed_step_output for output in outputs]
        hydrated = await self.hydrate_all(values)
        results: list[pb.StepCompletionOutput] = []
        for output, value in zip(outputs, hydrated):
            result = pb.StepCompletionOutput()
            result.CopyFrom(output)
            result.completed_step_output.CopyFrom(value)
            results.append(result)
        return results

    async def _load_misses(self, misses: list[_PendingBlob]) -> None:
        if not misses:
            return
        response = await self._service.LoadBlobs(
            pb.LoadBlobsRequest(values=[miss.request for miss in misses])
        )
        for miss in misses:
            concrete = response.values.get(miss.blob_id)
            if concrete is None:
                raise RuntimeError(f"LoadBlobs omitted blob {miss.blob_id}")
            self._validate_hydrated(miss, concrete)
            miss.hydrated = concrete
            self._write_cache(miss, concrete)

    def _read_cache(self, blob: _PendingBlob) -> pb.Value | None:
        try:
            payload = self._cache.get(blob.blob_id)
            if payload is None:
                return None
            if blob.is_object:
                concrete = pb.Value(obj_value=pb.EncodedObject.FromString(payload))
            else:
                concrete = pb.Value(string_value=payload.decode("utf-8"))
            self._validate_hydrated(blob, concrete)
            return concrete
        except Exception:
            _LOGGER.warning("cannot read cached blob %s", blob.blob_id, exc_info=True)
            try:
                self._cache.delete(blob.blob_id)
            except Exception:
                _LOGGER.warning(
                    "cannot delete cached blob %s",
                    blob.blob_id,
                    exc_info=True,
                )
            return None

    def _write_cache(self, blob: _PendingBlob, concrete: pb.Value) -> None:
        try:
            payload = (
                concrete.obj_value.SerializeToString()
                if blob.is_object
                else concrete.string_value.encode("utf-8")
            )
            self._cache.put(blob.blob_id, payload)
        except Exception:
            _LOGGER.warning("cannot cache blob %s", blob.blob_id, exc_info=True)

    @staticmethod
    def _blob_key(value: pb.Value) -> tuple[str, bool] | None:
        kind = value.WhichOneof("kind")
        if kind == "internal_blob_id_for_string_value":
            blob_id = value.internal_blob_id_for_string_value
            if not blob_id:
                raise ValueError("blob ID is required")
            return blob_id, False
        if kind == "internal_blob_id_for_obj_value":
            blob_id = value.internal_blob_id_for_obj_value
            if not blob_id:
                raise ValueError("blob ID is required")
            return blob_id, True
        return None

    @staticmethod
    def _validate_hydrated(blob: _PendingBlob, value: pb.Value) -> None:
        expected = "obj_value" if blob.is_object else "string_value"
        if value.WhichOneof("kind") != expected:
            raise RuntimeError(
                f"blob {blob.blob_id} hydrated to {value.WhichOneof('kind')}"
            )
        AsyncValueHydrator._validate_concrete(value)

    @staticmethod
    def _validate_concrete(value: pb.Value) -> None:
        kind = value.WhichOneof("kind")
        if kind in ("string_value", "int_value", "bool_value", "null_value"):
            return
        if kind == "double_value":
            import math

            if not math.isfinite(value.double_value):
                raise ValueError("non-finite numbers are unsupported")
            return
        if kind == "obj_value":
            if value.obj_value.encoding not in ("json", "rawbytes"):
                raise ValueError(
                    f"unsupported object encoding {value.obj_value.encoding}"
                )
            return
        raise ValueError("Value has no concrete kind")
