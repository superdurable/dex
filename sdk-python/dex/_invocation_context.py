# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from enum import Enum
from inspect import isawaitable
from typing import Any, Callable, Sequence, TypeVar, cast
from urllib.parse import unquote

from dex._utils import require_name
from dex._value_mapper import ValueMapper
from dex.attribute import Attribute, AttributeMap, _apply_attribute_store_sync
from dex.channel import Channel, ChannelMap
from dex.codec import Codec
from dex.dexpb import dex_pb2 as pb
from dex.flow import Registry, _RegisteredFlow
from dex.flow_result import FlowResult, flow_result_from_proto
from dex.stream import Stream

ValueT = TypeVar("ValueT")
_Definition = (
    Attribute[Any] | AttributeMap[Any] | Channel[Any] | ChannelMap[Any] | Stream[Any]
)


class InvocationMethod(Enum):
    WAIT_FOR = "wait_for"
    EXECUTE = "execute"
    RPC = "rpc"


class InvocationContext:
    def __init__(
        self,
        method: InvocationMethod,
        flow: _RegisteredFlow,
        metadata: pb.Context,
        values: ValueMapper,
        stream_writer: Callable[[pb.WriteStreamRequest], Any],
        attributes: Sequence[pb.KV],
        locals: Sequence[pb.KV] = (),
        condition_results: pb.ConditionResults | None = None,
        channel_infos: dict[str, pb.ChannelInfo] | None = None,
        is_active: Callable[[], bool] | None = None,
    ) -> None:
        self._method = method
        self._flow = flow
        self._metadata = metadata
        self._values = values
        self._stream_writer = stream_writer
        self._attributes = self._map_values("Attribute", attributes)
        self._locals = self._map_values("step-execution local", locals)
        self._condition_results = condition_results
        self._channel_infos = dict(channel_infos or {})
        self._is_active = is_active or _always_active
        self.attribute_writes: dict[str, pb.AttributeWrite] = {}
        self.local_writes: dict[str, pb.KV] = {}
        self.events: list[pb.KV] = []
        self.publications: list[pb.ChannelMessage] = []
        self._event_names: set[str] = set()
        self._stream_writes: set[int] = set()

    @property
    def flow_id(self) -> str:
        return self._metadata.flow_id

    @property
    def run_id(self) -> str:
        return self._metadata.run_id

    @property
    def step_execution_id(self) -> str:
        return self._metadata.step_execution_id

    @property
    def from_step_execution_id(self) -> str:
        return self._metadata.from_step_execution_id

    @property
    def attempt(self) -> int:
        return self._metadata.attempt

    def has_timer_fired(self, index: int | None = None) -> bool:
        if self._condition_results is None:
            return False
        results = self._condition_results.timer_results
        if index is None:
            return any(
                result.condition_status == pb.CONDITION_STATUS_COMPLETED
                for result in results
            )
        return (
            0 <= index < len(results)
            and results[index].condition_status == pb.CONDITION_STATUS_COMPLETED
        )

    def wait_for_method_failed(self) -> bool:
        return bool(
            self._condition_results is not None
            and self._condition_results.wait_for_failed
        )

    def sub_flow_result(self, index: int = 0) -> FlowResult:
        if self._method is not InvocationMethod.EXECUTE:
            raise ValueError("SubFlow results are available only during execute")
        if (
            self._condition_results is None
            or index < 0
            or index >= len(self._condition_results.sub_flow_results)
        ):
            raise ValueError(f"SubFlow result index is unavailable: {index}")
        return flow_result_from_proto(
            self._condition_results.sub_flow_results[index],
            lambda value, output_type: self._values.decode(
                value, self._values.codec(output_type)
            ),
        )

    def sub_flow_id(self, index: int = 0) -> str:
        self.sub_flow_result(index)
        return f"SubFlow:{self.flow_id}-{self.step_execution_id}-{index}"

    def is_cancellation_requested(self) -> bool:
        return not self._is_active()

    def set_step_execution_local(self, key: str, value: object) -> None:
        require_name(key)
        self.local_writes[key] = pb.KV(
            key=key,
            value=self._values.encode_dynamic(value),
        )

    def get_step_execution_local(
        self,
        key: str,
        value_type: type[ValueT],
    ) -> ValueT | None:
        require_name(key)
        entry = self.local_writes.get(key)
        value = entry.value if entry is not None else self._locals.get(key)
        if value is None:
            return None
        return cast(
            ValueT,
            self._values.decode(value, self._flow_codec(value_type)),
        )

    def record_event(self, name: str, value: object) -> None:
        require_name(name)
        if name in self._event_names:
            raise ValueError(f"event was already recorded: {name}")
        self._event_names.add(name)
        self.events.append(pb.KV(key=name, value=self._values.encode_dynamic(value)))

    def _write_stream(
        self,
        definition: Stream[ValueT],
        value: ValueT,
    ) -> Any:
        if self._method is InvocationMethod.RPC:
            raise ValueError("Stream writes require a Step Context")
        self._require_registered(definition)
        identity = id(definition)
        if identity in self._stream_writes:
            raise ValueError(
                f"Stream {definition.name} was already written by this Step execution"
            )
        request = pb.WriteStreamRequest(
            flow_id=self.flow_id,
            flow_type=self._flow.name,
            stream_name=definition.name,
            max_estimated_bytes=definition.max_estimated_bytes,
            value=self._values.encode(
                value,
                self._values.codec(definition.value_type),
            ),
            idempotency_key=f"{self.run_id}#{self.step_execution_id}",
        )
        result = self._stream_writer(request)
        if isawaitable(result):
            self._stream_writes.add(identity)
            return self._finish_async_stream_write(result, identity)
        self._stream_writes.add(identity)
        return None

    async def _finish_async_stream_write(
        self,
        result: Any,
        identity: int,
    ) -> None:
        try:
            await result
        except BaseException:
            self._stream_writes.remove(identity)
            raise

    def _get_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
    ) -> ValueT:
        self._require_registered(definition)
        key = self._physical_name(definition, instance)
        write = self.attribute_writes.get(key)
        if write is not None:
            if write.value.WhichOneof("kind") == "null_value":
                return cast(ValueT, self._default_value(definition.value_type))
            value: pb.Value | None = write.value
        else:
            value = self._attributes.get(key)
        if value is None:
            return cast(ValueT, self._default_value(definition.value_type))
        codec = self._flow_codec(definition.value_type)
        return cast(ValueT, self._values.decode(value, codec))

    def _set_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None:
        self._require_registered(definition)
        key = self._physical_name(definition, instance)
        write = pb.AttributeWrite(
            key=key,
            value=self._values.encode(value, self._flow_codec(definition.value_type)),
        )
        index = self._values.index_config(
            definition.index,
            isinstance(definition, AttributeMap),
        )
        if index is not None:
            write.index_config.CopyFrom(index)
        _apply_attribute_store_sync(write, definition)
        self.attribute_writes[key] = write

    def _delete_attribute(
        self,
        definition: Attribute[object] | AttributeMap[object],
        instance: str | None,
    ) -> None:
        self._require_registered(definition)
        key = self._physical_name(definition, instance)
        write = pb.AttributeWrite(key=key, value=self._values.deletion())
        index = self._values.index_config(
            definition.index,
            isinstance(definition, AttributeMap),
        )
        if index is not None:
            write.index_config.CopyFrom(index)
        _apply_attribute_store_sync(write, definition)
        self.attribute_writes[key] = write

    def _publish_channel(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None:
        self._require_registered(definition)
        name = self._physical_name(definition, instance)
        self.publications.append(
            pb.ChannelMessage(
                channel_name=name,
                value=self._values.encode(
                    value,
                    self._flow_codec(definition.value_type),
                ),
            )
        )
        if self._method is InvocationMethod.RPC:
            current = self._channel_infos.get(name)
            self._channel_infos[name] = pb.ChannelInfo(
                size=(current.size if current is not None else 0) + 1
            )

    def _attribute_map_keys(
        self,
        definition: AttributeMap[object],
    ) -> tuple[str, ...]:
        self._require_registered(definition)
        prefix = f"{definition.name}/"
        physical_keys = {key for key in self._attributes if key.startswith(prefix)}
        for key, write in self.attribute_writes.items():
            if not key.startswith(prefix):
                continue
            if write.value.WhichOneof("kind") == "null_value":
                physical_keys.discard(key)
            else:
                physical_keys.add(key)
        return tuple(sorted(unquote(key[len(prefix) :]) for key in physical_keys))

    def _channel_map_keys(
        self,
        definition: ChannelMap[object],
    ) -> tuple[str, ...]:
        self._require_registered(definition)
        if self._method is not InvocationMethod.RPC:
            raise ValueError("ChannelMap introspection requires an RPC invocation")
        prefix = f"{definition.name}/"
        return tuple(
            sorted(
                unquote(key[len(prefix) :])
                for key, info in self._channel_infos.items()
                if key.startswith(prefix) and info.size > 0
            )
        )

    def _channel_size(
        self,
        definition: Channel[object] | ChannelMap[object],
        instance: str | None,
    ) -> int:
        self._require_registered(definition)
        info = self._channel_infos.get(self._physical_name(definition, instance))
        return info.size if info is not None else 0

    def _channel_results(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
    ) -> Sequence[ValueT]:
        self._require_registered(definition)
        if self._condition_results is None:
            return ()
        name = self._physical_name(definition, instance)
        codec = self._flow_codec(definition.value_type)
        return tuple(
            cast(ValueT, self._values.decode(value, codec))
            for result in self._condition_results.channel_results
            if result.channel_name == name
            and result.condition_status == pb.CONDITION_STATUS_COMPLETED
            for value in result.values
        )

    def _flow_codec(self, value_type: object) -> Codec[Any]:
        return self._values.codec(value_type)

    def _require_registered(self, definition: _Definition) -> None:
        registered = self._flow.persistence.get(definition.name)
        if registered is not definition:
            raise ValueError(
                f"persistence definition does not belong to Flow: {definition.name}"
            )

    @staticmethod
    def _physical_name(definition: _Definition, instance: str | None) -> str:
        if isinstance(definition, (AttributeMap, ChannelMap)):
            if instance is None:
                raise ValueError("dynamic definition requires an instance")
            return Registry.physical_name(definition.name, instance)
        if instance is not None:
            raise ValueError("static definition cannot use an instance")
        return definition.name

    @staticmethod
    def _map_values(kind: str, entries: Sequence[pb.KV]) -> dict[str, pb.Value]:
        values: dict[str, pb.Value] = {}
        for entry in entries:
            if not entry.key or not entry.HasField("value") or entry.key in values:
                raise ValueError(f"invalid or duplicate {kind}")
            values[entry.key] = entry.value
        return values

    @staticmethod
    def _default_value(value_type: object) -> object | None:
        if value_type is bool:
            return False
        if value_type is int:
            return 0
        if value_type is float:
            return 0.0
        return None


def _always_active() -> bool:
    return True
