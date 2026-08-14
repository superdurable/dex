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
    """Identify the protocol representation carried by an encoded Value.

    Attributes:
        STRING: A UTF-8 string value.
        BOOL: A Boolean value.
        INT64: A signed 64-bit integer value.
        DOUBLE: A finite IEEE-754 double value.
        BYTES: An uninterpreted byte sequence.
        JSON: A canonical JSON document stored as text.
    """

    STRING = "string"
    BOOL = "bool"
    INT64 = "int64"
    DOUBLE = "double"
    BYTES = "bytes"
    JSON = "json"


@dataclass(frozen=True)
class Value:
    """Contain one validated SDK value before protocol mapping.

    Attributes:
        kind: The wire representation used for ``data``.
        data: The primitive value, bytes, or JSON text payload compatible with ``kind``.
    """

    kind: WireKind
    data: str | bool | int | float | bytes


class Codec(Protocol[ValueT]):
    """Define bidirectional conversion between a Python type and Dex Values.

    Custom codecs must be deterministic: equivalent inputs should produce the same
    Value so durable replays observe identical data.
    """

    @property
    def type_name(self) -> str:
        """Return the application-facing type name used in error messages.

        Returns:
            A stable, non-empty type name.
        """
        ...

    @property
    def wire_kind(self) -> WireKind:
        """Return the single wire kind emitted and accepted by this codec.

        Returns:
            The codec's protocol representation.
        """
        ...

    def encode(self, value: ValueT) -> Value:
        """Encode one typed Python value.

        Args:
            value: The application value to encode.

        Returns:
            A Value whose kind equals ``wire_kind``.

        Raises:
            TypeError: If ``value`` is incompatible with the codec.
            ValueError: If the value is not representable by Dex.
        """
        ...

    def decode(self, value: Value) -> ValueT:
        """Decode one protocol Value into the application type.

        Args:
            value: The validated Value to decode.

        Returns:
            The decoded application value.

        Raises:
            TypeError: If the wire kind or payload is incompatible.
            ValueError: If the payload is malformed.
        """
        ...


@dataclass(frozen=True)
class _PrimitiveCodec(Generic[ValueT]):
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


#: The built-in UTF-8 ``str`` codec.
STRING: Codec[str] = _PrimitiveCodec("str", WireKind.STRING, str)
#: The built-in strict ``bool`` codec; integers are not accepted as Booleans.
BOOL: Codec[bool] = _PrimitiveCodec("bool", WireKind.BOOL, bool)
#: The built-in signed 64-bit ``int`` codec; larger values raise OverflowError.
INT64: Codec[int] = _PrimitiveCodec("int", WireKind.INT64, int, _validate_int64)
#: The built-in finite ``float`` codec; NaN and infinities are rejected.
DOUBLE: Codec[float] = _PrimitiveCodec(
    "float", WireKind.DOUBLE, float, _validate_double
)
#: The built-in raw ``bytes`` codec.
BYTES: Codec[bytes] = _PrimitiveCodec("bytes", WireKind.BYTES, bytes)


@dataclass(frozen=True)
class _NoneCodec:
    type_name: str = "None"
    wire_kind: WireKind = WireKind.JSON

    def encode(self, value: None) -> Value:
        if value is not None:
            raise TypeError("None codec requires None")
        return Value(WireKind.JSON, "null")

    def decode(self, value: Value) -> None:
        if value.kind is not WireKind.JSON or value.data != "null":
            raise TypeError("None codec requires JSON null")
        return None


@dataclass(frozen=True)
class _DateTimeCodec:
    type_name: str = "datetime"
    wire_kind: WireKind = WireKind.STRING

    def encode(self, value: datetime) -> Value:
        if not isinstance(value, datetime):
            raise TypeError("datetime codec requires datetime")
        return Value(WireKind.STRING, value.isoformat())

    def decode(self, value: Value) -> datetime:
        if value.kind is not WireKind.STRING or not isinstance(value.data, str):
            raise TypeError("datetime codec requires a string value")
        return datetime.fromisoformat(value.data)


_NONE: Codec[None] = _NoneCodec()
_DATETIME: Codec[datetime] = _DateTimeCodec()


@dataclass(frozen=True)
class JsonCodec(Generic[ValueT]):
    """Encode an application type as deterministic JSON text.

    ``encoder`` first converts the typed value to JSON-compatible data; ``decoder``
    performs the reverse conversion. JSON is emitted with sorted keys, compact
    separators, and no NaN or infinity values.

    Attributes:
        type_name: The stable type name used in diagnostics.
        decoder: A callable from parsed JSON-compatible data to ``ValueT``.
        encoder: A callable from ``ValueT`` to JSON-compatible data.
        expected_type: Optional runtime type hint validated before encoding.
        wire_kind: Always :attr:`WireKind.JSON`.

    Examples:
        >>> codec = JsonCodec("Point", lambda data: Point(**data), vars, Point)
        >>> codec.decode(codec.encode(Point(x=1, y=2)))
        Point(x=1, y=2)
    """

    type_name: str
    decoder: Callable[[Any], ValueT]
    encoder: Callable[[ValueT], Any] = field(default=lambda value: value)
    expected_type: object | None = None
    wire_kind: WireKind = field(default=WireKind.JSON, init=False)

    def encode(self, value: ValueT) -> Value:
        """Encode a typed value as canonical JSON.

        Args:
            value: The application value accepted by ``encoder``.

        Returns:
            A JSON-kind Value containing compact, sorted JSON text.

        Raises:
            TypeError: If ``expected_type`` does not match.
            ValueError: If the encoded data contains unsupported JSON values.
        """
        if self.expected_type is not None and not _matches_type(
            value, self.expected_type
        ):
            raise TypeError(
                f"{self.type_name} requires {_type_name(self.expected_type)}, "
                f"got {type(value).__name__}"
            )
        payload = json.dumps(
            self.encoder(value),
            allow_nan=False,
            separators=(",", ":"),
            sort_keys=True,
        )
        return Value(WireKind.JSON, payload)

    def decode(self, value: Value) -> ValueT:
        """Decode JSON text with this codec's decoder.

        Args:
            value: A JSON-kind Value containing valid JSON text.

        Returns:
            The application value returned by ``decoder``.

        Raises:
            TypeError: If ``value`` is not a JSON Value.
            ValueError: If its JSON text is malformed.
        """
        if value.kind is not WireKind.JSON or not isinstance(value.data, str):
            raise TypeError(f"{self.type_name} requires a JSON value")
        return self.decoder(json.loads(value.data))


class CodecRegistry:
    """Resolve codecs for type hints used by registered SDK definitions.

    Explicit registrations take precedence over built-ins. The default registry
    supports primitive types, ``None``, ``datetime``, dataclasses, enums, and typed
    list, tuple, mapping, and sequence containers.

    Examples:
        >>> registry = CodecRegistry({Money: money_codec})
        >>> registry.resolve(Money) is money_codec
        True
    """

    def __init__(self, codecs: Mapping[object, Codec[Any]] | None = None) -> None:
        """Create a registry with optional custom type-hint mappings.

        Args:
            codecs: Custom codecs keyed by the exact type hint used in definitions.
                The mapping is copied and may be mutated after construction.
        """
        self._codecs = dict(codecs or {})

    def resolve(self, type_hint: object) -> Codec[Any]:
        """Return the codec for an exact type hint.

        Args:
            type_hint: A runtime type or supported parameterized type hint.

        Returns:
            The registered, built-in, or automatically derived codec.

        Raises:
            TypeError: If no codec can represent ``type_hint``.
        """
        custom = self._codecs.get(type_hint)
        if custom is not None:
            return custom
        builtins: dict[object, Codec[Any]] = {
            str: STRING,
            bool: BOOL,
            int: INT64,
            float: DOUBLE,
            bytes: BYTES,
            type(None): _NONE,
            datetime: _DATETIME,
        }
        builtin = builtins.get(type_hint)
        if builtin is not None:
            return builtin
        if _supports_automatic_json(type_hint):
            return JsonCodec(
                _type_name(type_hint),
                lambda value: _decode_json_value(value, type_hint),
                _encode_json_value,
                type_hint,
            )
        raise TypeError(
            f"no codec for {_type_name(type_hint)}; register one in CodecRegistry"
        )


def _supports_automatic_json(type_hint: object) -> bool:
    origin = get_origin(type_hint)
    return (
        isinstance(type_hint, type)
        and (is_dataclass(type_hint) or issubclass(type_hint, Enum))
    ) or origin in (list, tuple, dict, Mapping, Sequence)


def _type_name(type_hint: object) -> str:
    return getattr(type_hint, "__qualname__", str(type_hint))


def _matches_type(value: object, type_hint: object) -> bool:
    origin = get_origin(type_hint)
    arguments = get_args(type_hint)
    if origin is list:
        return isinstance(value, list) and all(
            _matches_type(item, arguments[0]) for item in value
        )
    if origin is tuple:
        return isinstance(value, tuple) and all(
            _matches_type(item, arguments[0]) for item in value
        )
    if origin in (dict, Mapping):
        return isinstance(value, Mapping) and all(
            _matches_type(key, arguments[0]) and _matches_type(item, arguments[1])
            for key, item in value.items()
        )
    return isinstance(type_hint, type) and type(value) is type_hint


def _encode_json_value(value: Any) -> Any:
    if is_dataclass(value) and not isinstance(value, type):
        return _encode_json_value(asdict(value))
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
