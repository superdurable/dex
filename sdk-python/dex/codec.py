# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import json
import math
from collections.abc import Mapping, Sequence
from dataclasses import MISSING, Field, dataclass, field, fields, is_dataclass
from datetime import datetime
from enum import Enum
from types import UnionType
from typing import (
    Any,
    Callable,
    Generic,
    Protocol,
    TypeVar,
    Union,
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
    list, tuple, mapping, and sequence containers. Parameterized generic
    dataclasses substitute their type arguments recursively. Optional types and
    unions use JSON, including when their alternatives have primitive codecs.

    Unions select an alternative by JSON kind: null, boolean, number, string,
    array, or object. Alternatives must have disjoint kinds. For example,
    ``Receipt | None`` and ``int | str`` work, but ``int | float``,
    ``str | datetime``, and unions of two dataclasses are ambiguous and raise
    TypeError during resolution. A custom codec can implement a discriminator.
    Boolean values never select an integer alternative.

    Dataclass fields use their Python names unless
    ``field(metadata={"dex_json_name": "callId"})`` specifies a non-empty wire
    name. Names must be unique within the dataclass. Unknown input keys are
    ignored. Absent keys use the declared default or default factory; a missing
    required field raises ValueError. Explicit null requires an optional type.

    ``dex_json_omit_default=True`` in field metadata omits values equal to the
    field's default. It requires a default or default factory. A factory is
    called again for comparison on every encode, so it must be deterministic
    and free of side effects. Without this opt-in, defaults and nulls are
    included in encoded JSON.

    Datetime fields decode ISO 8601 strings, including Go RFC3339 strings with
    ``Z`` or numeric timezone offsets. Python retains microsecond precision;
    additional fractional digits are truncated on decode. Encoding retains
    ``datetime.isoformat()`` by default. Field metadata
    ``dex_json_format="rfc3339"`` instead emits Go-compatible timestamps with
    ``Z`` for UTC and no trailing fractional zeroes. This opt-in requires a
    datetime or optional datetime field and an aware value whose timezone offset
    is a whole number of minutes.

    Examples:
        >>> registry = CodecRegistry({Money: money_codec})
        >>> registry.resolve(Money) is money_codec
        True

        A generic dataclass needs no explicit codec registration::

            from dataclasses import dataclass, field
            from datetime import datetime, timezone
            from typing import Generic, TypeVar
            from dex import CodecRegistry

            ResultT = TypeVar("ResultT")

            @dataclass
            class Receipt:
                call_id: str = field(metadata={"dex_json_name": "callId"})
                observed_at: datetime = field(metadata={
                    "dex_json_name": "observedAt", "dex_json_format": "rfc3339"
                })

            @dataclass
            class Result(Generic[ResultT]):
                value: ResultT
                receipt: Receipt | None = None
                failure: str | None = field(default=None, metadata={
                    "dex_json_omit_default": True
                })

            codec = CodecRegistry().resolve(Result[int])
            result = Result(42, Receipt("call-1", datetime.now(timezone.utc)))
            assert codec.decode(codec.encode(result)) == result
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
            TypeError: If no codec can represent ``type_hint``, a JSON union has
                overlapping kinds, or dataclass JSON metadata is invalid.
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
            _validate_json_type(type_hint, set())
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
    concrete_type = origin or type_hint
    return (
        isinstance(concrete_type, type)
        and (is_dataclass(concrete_type) or issubclass(concrete_type, Enum))
    ) or origin in (list, tuple, dict, Mapping, Sequence, Union, UnionType)


def _validate_json_type(type_hint: object, visited: set[object]) -> None:
    if type_hint in visited:
        return
    visited.add(type_hint)
    origin = get_origin(type_hint)
    concrete_type = origin or type_hint
    if origin in (Union, UnionType):
        seen_kinds: set[str] = set()
        for argument in get_args(type_hint):
            kinds = _json_type_kinds(argument)
            if seen_kinds.intersection(kinds):
                raise TypeError(
                    f"ambiguous JSON union {type_hint}: alternatives share a JSON kind"
                )
            seen_kinds.update(kinds)
    if isinstance(concrete_type, type) and is_dataclass(concrete_type):
        hints = _dataclass_type_hints(type_hint)
        wire_names: set[str] = set()
        for definition in fields(concrete_type):
            wire_name = _field_wire_name(definition)
            if wire_name in wire_names:
                raise TypeError(f"duplicate JSON field name {wire_name!r}")
            wire_names.add(wire_name)
            should_omit_default = definition.metadata.get(
                "dex_json_omit_default", False
            )
            if type(should_omit_default) is not bool:
                raise TypeError("dex_json_omit_default must be a bool")
            if (
                should_omit_default
                and definition.default is MISSING
                and definition.default_factory is MISSING
            ):
                raise TypeError(
                    f"{definition.name}: dex_json_omit_default requires a default"
                )
            if "dex_json_format" in definition.metadata:
                if definition.metadata["dex_json_format"] != "rfc3339" or hints[
                    definition.name
                ] not in (datetime, datetime | None):
                    raise TypeError(
                        f"{definition.name}: dex_json_format requires 'rfc3339' "
                        "and a datetime or optional datetime field"
                    )
            _validate_json_type(hints[definition.name], visited)
    else:
        for argument in get_args(type_hint):
            _validate_json_type(argument, visited)


def _json_type_kinds(type_hint: object) -> set[str]:
    origin = get_origin(type_hint)
    concrete_type = origin or type_hint
    if type_hint is Any:
        return {"null", "boolean", "number", "string", "array", "object"}
    if type_hint is type(None):
        return {"null"}
    if type_hint is bool:
        return {"boolean"}
    if type_hint in (int, float):
        return {"number"}
    if type_hint in (str, datetime):
        return {"string"}
    if isinstance(concrete_type, type) and is_dataclass(concrete_type):
        return {"object"}
    if isinstance(concrete_type, type) and issubclass(concrete_type, Enum):
        return {_json_value_kind(member.value) for member in concrete_type}
    if origin in (dict, Mapping):
        return {"object"}
    if origin in (list, tuple, Sequence):
        return {"array"}
    raise TypeError(f"unsupported JSON union alternative {_type_name(type_hint)}")


def _json_value_kind(value: Any) -> str:
    if value is None:
        return "null"
    if type(value) is bool:
        return "boolean"
    if type(value) in (int, float):
        return "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, (list, tuple)):
        return "array"
    if isinstance(value, Mapping):
        return "object"
    raise TypeError(f"unsupported JSON value {type(value).__name__}")


def _dataclass_type_hints(type_hint: object) -> dict[str, Any]:
    concrete_type = cast(type[Any], get_origin(type_hint) or type_hint)
    bindings = dict(
        zip(getattr(concrete_type, "__parameters__", ()), get_args(type_hint))
    )
    for parent_type in concrete_type.__mro__:
        for base in parent_type.__dict__.get("__orig_bases__", ()):
            base_type = get_origin(base)
            if base_type is not None:
                bindings.update(
                    zip(
                        getattr(base_type, "__parameters__", ()),
                        (
                            _substitute_type_hint(argument, bindings)
                            for argument in get_args(base)
                        ),
                    )
                )
    return {
        name: _substitute_type_hint(hint, bindings)
        for name, hint in get_type_hints(concrete_type).items()
    }


def _substitute_type_hint(type_hint: Any, bindings: dict[Any, Any]) -> Any:
    if isinstance(type_hint, TypeVar):
        return bindings.get(type_hint, type_hint)
    origin = get_origin(type_hint)
    if origin is None:
        return type_hint
    arguments = tuple(
        _substitute_type_hint(argument, bindings) for argument in get_args(type_hint)
    )
    if origin in (Union, UnionType):
        return Union[arguments]
    if hasattr(type_hint, "copy_with"):
        return type_hint.copy_with(arguments)
    return origin[arguments]


def _field_wire_name(definition: Field[Any]) -> str:
    wire_name = definition.metadata.get("dex_json_name", definition.name)
    if not isinstance(wire_name, str) or not wire_name:
        raise TypeError(f"{definition.name}: dex_json_name must be a non-empty string")
    return wire_name


def _type_name(type_hint: object) -> str:
    return getattr(type_hint, "__qualname__", str(type_hint))


def _matches_type(value: object, type_hint: object) -> bool:
    origin = get_origin(type_hint)
    arguments = get_args(type_hint)
    if type_hint is Any:
        return True
    if origin in (Union, UnionType):
        return any(_matches_type(value, argument) for argument in arguments)
    if origin in (list, Sequence):
        return isinstance(value, list if origin is list else (list, tuple)) and all(
            _matches_type(item, arguments[0]) for item in value
        )
    if origin is tuple:
        if not isinstance(value, tuple):
            return False
        if len(arguments) == 1 or (len(arguments) == 2 and arguments[1] is Ellipsis):
            return all(_matches_type(item, arguments[0]) for item in value)
        return len(value) == len(arguments) and all(
            _matches_type(item, argument) for item, argument in zip(value, arguments)
        )
    if origin in (dict, Mapping):
        return isinstance(value, Mapping) and all(
            _matches_type(key, arguments[0]) and _matches_type(item, arguments[1])
            for key, item in value.items()
        )
    concrete_type = origin or type_hint
    return isinstance(concrete_type, type) and type(value) is concrete_type


def _encode_json_value(value: Any) -> Any:
    if is_dataclass(value) and not isinstance(value, type):
        encoded = {}
        for definition in fields(value):
            item = getattr(value, definition.name)
            if definition.metadata.get("dex_json_omit_default", False):
                default = definition.default
                if definition.default_factory is not MISSING:
                    default = definition.default_factory()
                if item == default:
                    continue
            encoded[_field_wire_name(definition)] = (
                _encode_rfc3339_datetime(item)
                if definition.metadata.get("dex_json_format") == "rfc3339"
                and item is not None
                else _encode_json_value(item)
            )
        return encoded
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, Enum):
        return value.value
    if isinstance(value, Mapping):
        return {key: _encode_json_value(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_encode_json_value(item) for item in value]
    return value


def _encode_rfc3339_datetime(value: datetime) -> str:
    if not isinstance(value, datetime):
        raise TypeError("dex_json_format='rfc3339' requires datetime")
    offset = value.utcoffset()
    if offset is None or offset.total_seconds() % 60:
        raise ValueError(
            "RFC3339 datetime requires a timezone with whole-minute offset"
        )
    timestamp = value.isoformat(timespec="seconds")
    fraction = f".{value.microsecond:06d}".rstrip("0") if value.microsecond else ""
    timestamp = timestamp[:19] + fraction + timestamp[19:]
    return timestamp[:-6] + "Z" if timestamp.endswith("+00:00") else timestamp


def _decode_json_value(value: Any, type_hint: object) -> Any:
    origin = get_origin(type_hint)
    arguments = get_args(type_hint)
    concrete_type = origin or type_hint
    if origin in (Union, UnionType):
        kind = _json_value_kind(value)
        for argument in arguments:
            if kind in _json_type_kinds(argument):
                return _decode_json_value(value, argument)
        raise TypeError(f"{type_hint} cannot decode JSON {kind}")
    if isinstance(concrete_type, type) and is_dataclass(concrete_type):
        if not isinstance(value, Mapping):
            raise TypeError(f"{_type_name(type_hint)} requires a JSON object")
        hints = _dataclass_type_hints(type_hint)
        decoded = {}
        for definition in fields(concrete_type):
            if not definition.init:
                continue
            wire_name = _field_wire_name(definition)
            if wire_name not in value:
                if (
                    definition.default is not MISSING
                    or definition.default_factory is not MISSING
                ):
                    continue
                raise ValueError(
                    f"{_type_name(type_hint)} missing required JSON field {wire_name!r}"
                )
            decoded[definition.name] = _decode_json_value(
                value[wire_name], hints[definition.name]
            )
        return concrete_type(**decoded)
    if type_hint is datetime:
        if not isinstance(value, str):
            raise TypeError("datetime requires a JSON string")
        return datetime.fromisoformat(value)
    if isinstance(type_hint, type) and issubclass(type_hint, Enum):
        return type_hint(value)
    if origin in (list, Sequence):
        if not isinstance(value, list):
            raise TypeError(f"{type_hint} requires a JSON array")
        return [_decode_json_value(item, arguments[0]) for item in value]
    if origin is tuple:
        if not isinstance(value, list):
            raise TypeError(f"{type_hint} requires a JSON array")
        if len(arguments) == 1 or (len(arguments) == 2 and arguments[1] is Ellipsis):
            return tuple(_decode_json_value(item, arguments[0]) for item in value)
        if len(value) != len(arguments):
            raise ValueError(f"{type_hint} requires {len(arguments)} items")
        return tuple(
            _decode_json_value(item, argument)
            for item, argument in zip(value, arguments)
        )
    if origin in (dict, Mapping):
        if not isinstance(value, Mapping):
            raise TypeError(f"{type_hint} requires a JSON object")
        return {
            _decode_json_value(key, arguments[0]): _decode_json_value(
                item, arguments[1]
            )
            for key, item in value.items()
        }
    if type_hint in (str, bool, int, float):
        if type(value) is not type_hint:
            raise TypeError(f"expected {_type_name(type_hint)}")
    if type_hint is type(None) and value is not None:
        raise TypeError("expected JSON null")
    return value
