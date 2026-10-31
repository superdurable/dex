# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any

import grpc

from dex._grpc_errors import translate_rpc_error
from dex._value_mapper import ValueMapper
from dex.attribute import Attribute, AttributeMap, AttributeMapInstance
from dex.attribute_values import AttributeValues
from dex.codec import Codec
from dex.dexpb import dex_pb2 as pb
from dex.flow import Registry
from dex.runtime_errors import DexServiceError, FlowDefinitionError, ValueMappingError

_OPERATION = "get_attributes"

AttributeRead = Attribute[Any] | AttributeMapInstance[Any]


@dataclass(frozen=True)
class _PlannedAttributeRead:
    read: AttributeRead
    key: str
    codec: Codec[Any]


@dataclass(frozen=True)
class AttributeReadPlan:
    """Validated reads of one get_attributes call, shared by both Clients."""

    flow_id: str
    reads: tuple[_PlannedAttributeRead, ...]

    @property
    def keys(self) -> tuple[str, ...]:
        return tuple(planned.key for planned in self.reads)

    def request(self) -> pb.GetAttributesRequest:
        return pb.GetAttributesRequest(flow_id=self.flow_id, keys=self.keys)

    def service_error(
        self, error: grpc.RpcError, service_method: str
    ) -> DexServiceError:
        translated = translate_rpc_error(error, _OPERATION, self.flow_id, "existing")
        translated.args = (
            f"{self._context()} Attributes {self._joined_keys()} {service_method} "
            f"failed: {translated.detail}",
        )
        return translated

    def hydration_error(self, error: Exception) -> ValueMappingError:
        return ValueMappingError(
            "decode",
            f"{self._context()} Attributes {self._joined_keys()} returned a value "
            f"that cannot be resolved: {error}",
        )

    def set_entries(self, response: pb.GetAttributesResponse) -> list[pb.KV]:
        """Return the set entries after rejecting malformed responses.

        Returns:
            One entry per set read, in response order. An omitted key is unset.

        Raises:
            ValueError: If an entry has an unexpected or duplicate key, or no value.
        """
        requested = set(self.keys)
        seen: set[str] = set()
        entries: list[pb.KV] = []
        for entry in response.attributes:
            if entry.key not in requested:
                raise ValueError(
                    f"{self._context()} returned unexpected Attribute {entry.key!r}"
                )
            if entry.key in seen:
                raise ValueError(
                    f"{self._context()} returned duplicate Attribute {entry.key!r}"
                )
            if not entry.HasField("value"):
                raise ValueError(
                    f"{self._context()} returned Attribute {entry.key!r} without a value"
                )
            seen.add(entry.key)
            entries.append(entry)
        return entries

    def decode(
        self,
        values: ValueMapper,
        entries: Sequence[pb.KV],
        hydrated: Sequence[pb.Value],
    ) -> AttributeValues:
        """Decode every set value, or raise before returning any of them.

        Returns:
            The set values keyed by the read passed to the call.

        Raises:
            ValueMappingError: If a value cannot be decoded as its value type.
        """
        hydrated_by_key = {
            entry.key: value for entry, value in zip(entries, hydrated, strict=True)
        }
        decoded: dict[AttributeRead, object] = {}
        for planned in self.reads:
            value = hydrated_by_key.get(planned.key)
            if value is None:
                continue
            try:
                decoded[planned.read] = values.decode(value, planned.codec)
            except ValueMappingError as error:
                raise ValueMappingError(
                    "decode",
                    f"{self._context()} Attribute {planned.key!r}: {error.detail}",
                ) from error
        return AttributeValues._of_set_values(decoded)

    def _context(self) -> str:
        return f"{_OPERATION} Flow {self.flow_id!r}"

    def _joined_keys(self) -> str:
        return ", ".join(repr(key) for key in self.keys)


def plan_attribute_reads(
    registry: Registry,
    values: ValueMapper,
    flow_id: str,
    reads: Sequence[object],
) -> AttributeReadPlan:
    """Validate the reads of one get_attributes call before any transport.

    Returns:
        The plan whose keys follow the order of ``reads``.

    Raises:
        ValueError: If the Flow ID is empty, no read is given, or a key repeats.
        TypeError: If a read is not an Attribute or AttributeMapInstance.
        FlowDefinitionError: If no registered Flow declares an equal definition.
    """
    if not isinstance(flow_id, str) or not flow_id.strip():
        raise ValueError(f"{_OPERATION} requires a non-empty Flow ID, got {flow_id!r}")
    if not reads:
        raise ValueError(
            f"{_OPERATION} Flow {flow_id!r} requires at least one Attribute read"
        )
    planned_reads: list[_PlannedAttributeRead] = []
    seen_keys: set[str] = set()
    for read in reads:
        typed_read, definition, key = _definition_and_key(flow_id, read)
        try:
            registry._require_registered_attribute(definition)
        except FlowDefinitionError as error:
            raise FlowDefinitionError(
                f"{_OPERATION} Flow {flow_id!r} Attribute {key!r}: {error}"
            ) from error
        if key in seen_keys:
            raise ValueError(
                f"{_OPERATION} Flow {flow_id!r} has duplicate Attribute read {key!r}"
            )
        seen_keys.add(key)
        planned_reads.append(
            _PlannedAttributeRead(
                read=typed_read,
                key=key,
                codec=values.codec(definition.value_type),
            )
        )
    return AttributeReadPlan(flow_id, tuple(planned_reads))


def _definition_and_key(
    flow_id: str,
    read: object,
) -> tuple[AttributeRead, Attribute[Any] | AttributeMap[Any], str]:
    if isinstance(read, Attribute):
        return read, read, read.name
    if isinstance(read, AttributeMapInstance):
        key = Registry.physical_name(read.attribute_map.name, read.instance)
        return read, read.attribute_map, key
    if isinstance(read, AttributeMap):
        raise TypeError(
            f"{_OPERATION} Flow {flow_id!r} AttributeMap {read.name!r} needs an "
            "instance; pass AttributeMap.instance(key)"
        )
    raise TypeError(
        f"{_OPERATION} Flow {flow_id!r} read must be an Attribute or "
        f"AttributeMapInstance, not {type(read).__name__}"
    )
