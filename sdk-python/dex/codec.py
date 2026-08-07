# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import json
import math
from dataclasses import asdict, dataclass, field, fields, is_dataclass
from datetime import datetime
from enum import Enum
from typing import (
    Any,
    Callable,
    Generic,
    Mapping,
    Protocol,
    Sequence,
    TypeVar,
    cast,
    get_args,
    get_origin,
    get_type_hints,
)

ValueT = TypeVar("ValueT")


class WireKind(Enum):
    STRING = "string"
    BOOL = "bool"
    INT64 = "int64"
    DOUBLE = "double"
    BYTES = "bytes"
    JSON = "json"


@dataclass(frozen=True)
class Value:
    kind: WireKind
    data: str | bool | int | float | bytes


class Codec(Protocol[ValueT]):
    @property
    def type_name(self) -> str: ...

    @property
    def wire_kind(self) -> WireKind: ...

    def encode(self, value: ValueT) -> Value: ...

    def decode(self, value: Value) -> ValueT: ...


@dataclass(frozen=True)
class _ScalarCodec(Generic[ValueT]):
    type_name: str
    wire_kind: WireKind
    expected_type: type[Any]
    validator: Callable[[ValueT], None] | None = None

    def encode(self, value: ValueT) -> Value:
        if type(value) is not self.expected_type:
            raise TypeError(
                f"{self.type_name} requires {self.expected_type.__name__}, "
                f"got {type(value).__name__}"
            )
        if self.validator is not None:
            self.validator(value)
        return Value(self.wire_kind, cast(str | bool | int | float | bytes, value))

    def decode(self, value: Value) -> ValueT:
        if value.kind is not self.wire_kind:
            raise TypeError(
                f"{self.type_name} cannot decode wire kind {value.kind.value}"
            )
        decoded = value.data
        if type(decoded) is not self.expected_type:
            raise TypeError(f"invalid {self.type_name} payload")
        typed = cast(ValueT, decoded)
        if self.validator is not None:
            self.validator(typed)
        return typed


def _validate_int64(value: int) -> None:
    if value < -(2**63) or value > 2**63 - 1:
        raise OverflowError(f"integer {value} exceeds int64")


def _validate_double(value: float) -> None:
    if not math.isfinite(value):
        raise ValueError("non-finite floating-point values are unsupported")


STRING: Codec[str] = _ScalarCodec("str", WireKind.STRING, str)
BOOL: Codec[bool] = _ScalarCodec("bool", WireKind.BOOL, bool)
INT64: Codec[int] = _ScalarCodec("int", WireKind.INT64, int, _validate_int64)
DOUBLE: Codec[float] = _ScalarCodec("float", WireKind.DOUBLE, float, _validate_double)
BYTES: Codec[bytes] = _ScalarCodec("bytes", WireKind.BYTES, bytes)


@dataclass(frozen=True)
class JsonCodec(Generic[ValueT]):
    type_name: str
    decoder: Callable[[Any], ValueT]
    encoder: Callable[[ValueT], Any] = field(default=lambda value: value)
    wire_kind: WireKind = field(default=WireKind.JSON, init=False)

    def encode(self, value: ValueT) -> Value:
        payload = json.dumps(
            self.encoder(value),
            allow_nan=False,
            separators=(",", ":"),
            sort_keys=True,
        )
        return Value(WireKind.JSON, payload)

    def decode(self, value: Value) -> ValueT:
        if value.kind is not WireKind.JSON or not isinstance(value.data, str):
            raise TypeError(f"{self.type_name} requires a JSON value")
        return self.decoder(json.loads(value.data))


class CodecRegistry:
    def __init__(self, codecs: Mapping[object, Codec[Any]] | None = None) -> None:
        self._codecs = dict(codecs or {})

    def resolve(self, type_hint: object) -> Codec[Any]:
        custom = self._codecs.get(type_hint)
        if custom is not None:
            return custom
        builtins: dict[object, Codec[Any]] = {
            str: STRING,
            bool: BOOL,
            int: INT64,
            float: DOUBLE,
            bytes: BYTES,
        }
        builtin = builtins.get(type_hint)
        if builtin is not None:
            return builtin
        if _supports_automatic_json(type_hint):
            return JsonCodec(
                _type_name(type_hint),
                lambda value: _decode_json_value(value, type_hint),
                _encode_json_value,
            )
        raise TypeError(
            f"no codec for {_type_name(type_hint)}; register one in CodecRegistry"
        )


def _supports_automatic_json(type_hint: object) -> bool:
    origin = get_origin(type_hint)
    return (
        (
            isinstance(type_hint, type)
            and (is_dataclass(type_hint) or issubclass(type_hint, Enum))
        )
        or type_hint is datetime
        or origin in (list, tuple, dict, Mapping, Sequence)
    )


def _type_name(type_hint: object) -> str:
    return getattr(type_hint, "__qualname__", str(type_hint))


def _encode_json_value(value: Any) -> Any:
    if is_dataclass(value) and not isinstance(value, type):
        return asdict(value)
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, Enum):
        return value.value
    if isinstance(value, Mapping):
        return {key: _encode_json_value(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_encode_json_value(item) for item in value]
    return value


def _decode_json_value(value: Any, type_hint: object) -> Any:
    origin = get_origin(type_hint)
    arguments = get_args(type_hint)
    if isinstance(type_hint, type) and is_dataclass(type_hint):
        if not isinstance(value, Mapping):
            raise TypeError(f"{_type_name(type_hint)} requires a JSON object")
        hints = get_type_hints(type_hint)
        return type_hint(
            **{
                definition.name: _decode_json_value(
                    value[definition.name], hints[definition.name]
                )
                for definition in fields(type_hint)
            }
        )
    if type_hint is datetime:
        if not isinstance(value, str):
            raise TypeError("datetime requires a JSON string")
        return datetime.fromisoformat(value)
    if isinstance(type_hint, type) and issubclass(type_hint, Enum):
        return type_hint(value)
    if origin is list:
        return [_decode_json_value(item, arguments[0]) for item in value]
    if origin is tuple:
        return tuple(_decode_json_value(item, arguments[0]) for item in value)
    if origin in (dict, Mapping):
        return {
            _decode_json_value(key, arguments[0]): _decode_json_value(
                item, arguments[1]
            )
            for key, item in value.items()
        }
    if type_hint in (str, bool, int, float):
        if type(value) is not type_hint:
            raise TypeError(f"expected {_type_name(type_hint)}")
    return value
