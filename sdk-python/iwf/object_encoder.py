# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Base converter and implementations for data conversion.
Adapted from https://github.com/temporalio/sdk-python/blob/main/temporalio/converter.py
"""

from __future__ import annotations

import collections
import collections.abc
import dataclasses
import inspect
import json
import sys
import uuid
import warnings
from abc import ABC, abstractmethod
from dataclasses import dataclass
from enum import IntEnum
from typing import (
    Any,
    ClassVar,
    Dict,
    Mapping,
    NewType,
    Optional,
    Sequence,
    Tuple,
    Type,
    TypeVar,
    Union,
    get_type_hints,
)

from typing_extensions import Literal

from iwf.iwf_api.models import EncodedObject
from iwf.iwf_api.types import UNSET, Unset

# StrEnum is available in 3.11+
if sys.version_info >= (3, 11):
    from enum import StrEnum

if sys.version_info >= (3, 10):
    from types import UnionType


class PayloadConverter(ABC):
    """Base payload converter to/from payload/value."""

    @abstractmethod
    def to_payload(
        self,
        value: Any,
    ) -> Union[EncodedObject, Unset]:
        """Encode values into payloads.

        Args:
            value: value to be converted

        Returns:
            A boolean to indicate if the payload was converted and the converted value
            or Unset

        Raises:
            Exception: Any issue during conversion.
        """
        raise NotImplementedError

    @abstractmethod
    def from_payload(
        self,
        payload: EncodedObject,
        type_hint: Optional[Type] = None,
    ) -> Any:
        """Decode payloads into values.

        Args:
            payload: Payload to convert to Python values.
            type_hint: Type that are expected if any.

        Returns:
            payload value

        Raises:
            Exception: Any issue during conversion.
        """
        raise NotImplementedError


class EncodingPayloadConverter(ABC):
    """Base converter for a **known encoding** for use in CompositePayloadConverter."""

    @property
    @abstractmethod
    def encoding(self) -> Union[str, Unset]:
        """Encoding for the payload this converter works with."""
        raise NotImplementedError

    @abstractmethod
    def to_payload(self, value: Any) -> tuple[bool, Union[EncodedObject, Unset]]:
        """Encode a single value to a payload or None.

        Args:
            value: Value to be converted.

        Returns:
            A boolean to indicate if the payload was converted and the converted value
            or Unset

        Raises:
            TypeError: Value is not the expected type.
            ValueError: Value is of the expected type but otherwise incorrect.
            RuntimeError: General error during encoding.
        """
        raise NotImplementedError

    @abstractmethod
    def from_payload(
        self,
        payload: EncodedObject,
        type_hint: Optional[Type] = None,
    ) -> Any:
        """Decode a single payload to a Python value or raise exception.

        Args:
            payload: Payload to convert to Python value.
            type_hint: Type that is expected if any. This may not have a type if
                there are no annotations on the target.

        Return:
            The decoded value from the payload. Since the encoding is checked by
            the caller, this should raise an exception if the payload cannot be
            converted.

        Raises:
            RuntimeError: General error during decoding.
        """
        raise NotImplementedError


class CompositePayloadConverter(PayloadConverter):
    """Composite payload converter that delegates to a list of encoding payload converters.

    Encoding/decoding are attempted on each payload converter successively until
    it succeeds.

    Attributes:
        converters: List of payload converters to delegate to, in order.
    """

    converters: Mapping[Union[str, Unset], EncodingPayloadConverter]

    def __init__(self, *converters: EncodingPayloadConverter) -> None:
        """Initializes the data converter.

        Args:
            converters: Payload converters to delegate to, in order.
        """
        # Insertion order preserved here since Python 3.7
        self.converters = {c.encoding: c for c in converters}

    def to_payload(
        self,
        value: Any,
    ) -> Union[EncodedObject, Unset]:
        """Encode values trying each converter.

        See base class. Always returns the same number of payloads as values.

        Raises:
            RuntimeError: No known converter
        """
        # We intentionally attempt these serially just in case a stateful
        # converter may rely on the previous values
        payload: Union[EncodedObject, Unset] = Unset()
        is_encoded = False
        for converter in self.converters.values():
            is_encoded, payload = converter.to_payload(value)
            if is_encoded:
                break
        if not is_encoded:
            raise RuntimeError(
                f"Value of type {type(value)} has no known converter",
            )
        return payload

    def from_payload(
        self,
        payload: EncodedObject,
        type_hint: Optional[Type] = None,
    ) -> Any:
        """Decode values trying each converter.

        See base class. Always returns the same number of values as payloads.

        Raises:
            KeyError: Unknown payload encoding
            RuntimeError: Error during decode
        """
        encoding = payload.encoding
        assert isinstance(encoding, (str, Unset))
        converter = self.converters.get(encoding)
        if converter is None:
            raise KeyError(f"Unknown payload encoding {encoding}")
        try:
            value = converter.from_payload(payload, type_hint)
        except RuntimeError as err:
            raise RuntimeError(
                f"Payload with encoding {encoding} could not be converted",
            ) from err
        return value


class DefaultPayloadConverter(CompositePayloadConverter):
    """Default payload converter compatible with other Temporal SDKs.

    This handles None, bytes, all protobuf message types, and any type that
    :py:func:`json.dump` accepts. A singleton instance of this is available at
    :py:attr:`PayloadConverter.default`.
    """

    default_encoding_payload_converters: Tuple[EncodingPayloadConverter, ...]
    """Default set of encoding payload converters the default payload converter
    uses.
    """

    def __init__(self) -> None:
        """Create a default payload converter."""
        super().__init__(*DefaultPayloadConverter.default_encoding_payload_converters)


class UnsetPayloadConverter(EncodingPayloadConverter):
    """Converter for 'unset' payloads supporting None values."""

    @property
    def encoding(self) -> Union[str, Unset]:
        """See base class."""
        return UNSET

    def to_payload(self, value: Any) -> tuple[bool, Union[EncodedObject, Unset]]:
        """See base class."""
        if value is None:
            return (True, UNSET)
        return (False, UNSET)

    def from_payload(
        self,
        payload: EncodedObject,
        type_hint: Optional[Type] = None,
    ) -> Any:
        """See base class."""
        if isinstance(payload.data, str) and len(payload.data) > 0:
            raise RuntimeError("Expected empty data set for binary/null")
        return None


class BinaryNullPayloadConverter(UnsetPayloadConverter):
    """Converter for 'binary/null' payloads supporting None values."""

    @property
    def encoding(self) -> Union[str, Unset]:
        """See base class."""
        return "binary/null"


class BinaryPlainPayloadConverter(EncodingPayloadConverter):
    """Converter for 'binary/plain' payloads supporting bytes values."""

    @property
    def encoding(self) -> Union[str, Unset]:
        """See base class."""
        return "binary/plain"

    def to_payload(self, value: Any) -> tuple[bool, Union[EncodedObject, Unset]]:
        """See base class."""
        if isinstance(value, bytes):
            return (
                True,
                EncodedObject(
                    encoding=self.encoding,
                    data=str(value),
                ),
            )
        return (False, UNSET)

    def from_payload(
        self,
        payload: EncodedObject,
        type_hint: Optional[Type] = None,
    ) -> Any:
        """See base class."""
        return payload.data


class AdvancedJSONEncoder(json.JSONEncoder):
    """Advanced JSON encoder.

    This encoder supports dataclasses, classes with dict() functions, and
    all iterables as lists.
    """

    def default(self, o: Any) -> Any:
        """Override JSON encoding default.

        See :py:meth:`json.JSONEncoder.default`.
        """
        # Dataclass support
        if dataclasses.is_dataclass(o) and not isinstance(o, type):
            return dataclasses.asdict(o)
        # Support for models with "dict" function like Pydantic
        dict_fn = getattr(o, "dict", None)
        if callable(dict_fn):
            return dict_fn()
        # Support for non-list iterables like set
        if not isinstance(o, list) and isinstance(o, collections.abc.Iterable):
            return list(o)
        # Support for UUID
        if isinstance(o, uuid.UUID):
            return str(o)
        return super().default(o)


class JSONPlainPayloadConverter(EncodingPayloadConverter):
    """Converter for 'json/plain' payloads supporting common Python values.

    For encoding, this supports all values that :py:func:`json.dump` supports
    and by default adds extra encoding support for dataclasses, classes with
    ``dict()`` methods, and all iterables.

    For decoding, this uses type hints to attempt to rebuild the type from the
    type hint.
    """

    _encoder: Optional[Type[json.JSONEncoder]]
    _decoder: Optional[Type[json.JSONDecoder]]
    _encoding: str

    def __init__(
        self,
        *,
        encoder: Optional[Type[json.JSONEncoder]] = AdvancedJSONEncoder,
        decoder: Optional[Type[json.JSONDecoder]] = None,
        encoding: str = "json/plain",
        custom_type_converters: Sequence[JSONTypeConverter] = [],
    ) -> None:
        """Initialize a JSON data converter.

        Args:
            encoder: Custom encoder class object to use.
            decoder: Custom decoder class object to use.
            encoding: Encoding name to use.
            custom_type_converters: Set of custom type converters that are used
                when converting from a payload to type-hinted values.
        """
        super().__init__()
        self._encoder = encoder
        self._decoder = decoder
        self._encoding = encoding
        self._custom_type_converters = custom_type_converters

    @property
    def encoding(self) -> Union[str, Unset]:
        """See base class."""
        return self._encoding

    def to_payload(self, value: Any) -> tuple[bool, Union[EncodedObject, Unset]]:
        """See base class."""
        # Check for pydantic then send warning
        if hasattr(value, "parse_obj"):
            warnings.warn(
                "If you're using pydantic model, refer to "
                "https://github.com/temporalio/examples/python/tree/main/pydantic_converter for better support",
            )
        # We let JSON conversion errors be thrown to caller
        return (
            True,
            EncodedObject(
                encoding=self.encoding,
                data=json.dumps(
                    value,
                    cls=self._encoder,
                    separators=(",", ":"),
                    sort_keys=True,
                ),
            ),
        )

    def from_payload(
        self,
        payload: EncodedObject,
        type_hint: Optional[Type] = None,
    ) -> Any:
        """See base class."""
        try:
            if isinstance(payload.data, str):
                obj = json.loads(payload.data, cls=self._decoder)
                if type_hint:
                    obj = value_to_type(type_hint, obj, self._custom_type_converters)
                return obj
            else:
                return None
        except json.JSONDecodeError as err:
            raise RuntimeError("Failed parsing") from err


_JSONTypeConverterUnhandled = NewType("_JSONTypeConverterUnhandled", object)


class JSONTypeConverter(ABC):
    """Converter for converting an object from Python :py:func:`json.loads`
    result (e.g. scalar, list, or dict) to a known type.
    """

    Unhandled = _JSONTypeConverterUnhandled(object())
    """Sentinel value that must be used as the result of
    :py:meth:`to_typed_value` to say the given type is not handled by this
    converter."""

    @abstractmethod
    def to_typed_value(
        self,
        hint: Type,
        value: Any,
    ) -> Union[Optional[Any], _JSONTypeConverterUnhandled]:
        """Convert the given value to a type based on the given hint.

        Args:
            hint: Type hint to use to help in converting the value.
            value: Value as returned by :py:func:`json.loads`. Usually a scalar,
                list, or dict.

        Returns:
            The converted value or :py:attr:`Unhandled` if this converter does
            not handle this situation.
        """
        raise NotImplementedError


class PayloadCodec(ABC):
    """Codec for encoding/decoding to/from bytes.

    Commonly used for compression or encryption.
    """

    @abstractmethod
    def encode(
        self,
        payload: Union[EncodedObject, Unset],
    ) -> EncodedObject:
        """Encode the given payloads.

        Args:
            payload: Payloads to encode. This value should not be mutated.

        Returns:
            Encoded payloads. Note, this does not have to be the same number as
            payloads given, but must be at least one and cannot be more than was
            given.
        """
        raise NotImplementedError

    @abstractmethod
    def decode(
        self,
        payload: EncodedObject,
    ) -> EncodedObject:
        """Decode the given payloads.

        Args:
            payload: Payloads to decode. This value should not be mutated.

        Returns:
            Decoded payloads. Note, this does not have to be the same number as
            payloads given, but must be at least one and cannot be more than was
            given.
        """
        raise NotImplementedError


@dataclass(frozen=True)
class ObjectEncoder:
    """Object Encoder for converting and encoding payloads to/from Python values.

    This combines :py:class:`PayloadConverter` which converts values with
    :py:class:`PayloadCodec` which encodes bytes.
    """

    payload_converter_class: Type[PayloadConverter] = DefaultPayloadConverter
    """Class to instantiate for payload conversion."""

    payload_codec: Optional[PayloadCodec] = None
    """Optional codec for encoding payload bytes."""

    payload_converter: PayloadConverter = dataclasses.field(init=False)
    """Payload converter created from the :py:attr:`payload_converter_class`."""

    default: ClassVar[ObjectEncoder]
    """Singleton default data converter."""

    def __post_init__(self) -> None:  # noqa: D105
        object.__setattr__(self, "payload_converter", self.payload_converter_class())

    def encode(
        self,
        value: Any,
    ) -> Union[EncodedObject, Unset]:
        """Encode values into payloads.

        First converts values to payload then encodes payload using codec.

        Args:
            value: Values to be converted and encoded.

        Returns:
            Converted and encoded payload.
        """
        payload = self.payload_converter.to_payload(value)
        if self.payload_codec:
            payload = self.payload_codec.encode(payload)
        return payload

    def decode(
        self,
        payload: Union[Optional[EncodedObject], Unset],
        type_hint: Optional[Type] = None,
    ) -> Any:
        """Decode payloads into values.

        First decodes payloads using codec then converts payloads to values.

        Args:
            type_hint: type to decode to
            payload: Payload to be decoded and converted.

        Returns:
            Decoded and converted value.
        """
        if payload is None or isinstance(payload, Unset):
            return None
        if self.payload_codec:
            payload = self.payload_codec.decode(payload)
        return self.payload_converter.from_payload(payload, type_hint)


DefaultPayloadConverter.default_encoding_payload_converters = (
    UnsetPayloadConverter(),
    BinaryNullPayloadConverter(),
    BinaryPlainPayloadConverter(),
    JSONPlainPayloadConverter(),
)

ObjectEncoder.default = ObjectEncoder()


def value_to_type(
    hint: Type,
    value: Any,
    custom_converters,
) -> Any:
    """Convert a given value to the given type hint.

    This is used internally to convert a raw JSON loaded value to a specific
    type hint.

    Args:
        hint: Type hint to convert the value to.
        value: Raw value (e.g. primitive, dict, or list) to convert from.
        custom_converters: Set of custom converters to try before doing default
            conversion. Converters are tried in order and the first value that
            is not :py:attr:`JSONTypeConverter.Unhandled` will be returned from
            this function instead of doing default behavior.

    Returns:
        Converted value.

    Raises:
        TypeError: Unable to convert to the given hint.
    """
    # Try custom converters
    if custom_converters is None:
        custom_converters = []
    for conv in custom_converters:
        ret = conv.to_typed_value(hint, value)
        if ret is not JSONTypeConverter.Unhandled:
            return ret

    # Any or primitives
    if hint is Any:
        return value
    elif hint is int or hint is float:
        if not isinstance(value, (int, float)):
            raise TypeError(f"Expected value to be int|float, was {type(value)}")
        return hint(value)
    elif hint is bool:
        if not isinstance(value, bool):
            raise TypeError(f"Expected value to be bool, was {type(value)}")
        return bool(value)
    elif hint is str:
        if not isinstance(value, str):
            raise TypeError(f"Expected value to be str, was {type(value)}")
        return str(value)
    elif hint is bytes:
        if not isinstance(value, (str, bytes, list)):
            raise TypeError(f"Expected value to be bytes, was {type(value)}")
        # In some other SDKs, this is serialized as a base64 string, but in
        # Python this is a numeric array.
        return bytes(value)  # type: ignore
    elif hint is type(None):
        if value is not None:
            raise TypeError(f"Expected None, got value of type {type(value)}")
        return None

    # NewType. Note we cannot simply check isinstance NewType here because it's
    # only been a class since 3.10. Instead we'll just check for the presence
    # of a supertype.
    supertype = getattr(hint, "__supertype__", None)
    if supertype:
        return value_to_type(supertype, value, custom_converters)

    # Load origin for other checks
    origin = getattr(hint, "__origin__", hint)
    type_args: Tuple = getattr(hint, "__args__", ())

    # Literal
    if origin is Literal:
        if value not in type_args:
            raise TypeError(f"Value {value} not in literal values {type_args}")
        return value

    is_union = origin is Union
    if sys.version_info >= (3, 10):
        is_union = is_union or isinstance(origin, UnionType)

    # Union
    if is_union:
        # Try each one. Note, Optional is just a union w/ none.
        for arg in type_args:
            try:
                return value_to_type(arg, value, custom_converters)
            except Exception:
                pass
        raise TypeError(f"Failed converting to {hint} from {value}")

    # Mapping
    if inspect.isclass(origin) and issubclass(origin, collections.abc.Mapping):
        if not isinstance(value, collections.abc.Mapping):
            raise TypeError(f"Expected {hint}, value was {type(value)}")
        ret_dict = {}
        # If there are required or optional keys that means we are a TypedDict
        # and therefore can extract per-key types
        per_key_types: Optional[Dict[str, Type]] = None
        if getattr(origin, "__required_keys__", None) or getattr(
            origin,
            "__optional_keys__",
            None,
        ):
            per_key_types = get_type_hints(origin)
        key_type = (
            type_args[0]
            if len(type_args) > 0
            and type_args[0] is not Any
            and not isinstance(type_args[0], TypeVar)
            else None
        )
        value_type = (
            type_args[1]
            if len(type_args) > 1
            and type_args[1] is not Any
            and not isinstance(type_args[1], TypeVar)
            else None
        )
        # Convert each key/value
        for key, value in value.items():
            if key_type:
                try:
                    key = value_to_type(key_type, key, custom_converters)
                except Exception as err:
                    raise TypeError(f"Failed converting key {key} on {hint}") from err
            # If there are per-key types, use it instead of single type
            this_value_type = value_type
            if per_key_types:
                # TODO(cretz): Strict mode would fail an unknown key
                this_value_type = per_key_types.get(key)
            if this_value_type:
                try:
                    value = value_to_type(this_value_type, value, custom_converters)
                except Exception as err:
                    raise TypeError(
                        f"Failed converting value for key {key} on {hint}",
                    ) from err
            ret_dict[key] = value
        # If there are per-key types, it's a typed dict and we want to attempt
        # instantiation to get its validation
        if per_key_types:
            ret_dict = hint(**ret_dict)
        return ret_dict

    # Dataclass
    if dataclasses.is_dataclass(hint):
        if not isinstance(value, dict):
            raise TypeError(
                f"Cannot convert to dataclass {hint}, value is {type(value)} not dict",
            )
        # Obtain dataclass fields and check that all dict fields are there and
        # that no required fields are missing. Unknown fields are silently
        # ignored.
        fields = dataclasses.fields(hint)
        field_hints = get_type_hints(hint)
        field_values = {}
        for field in fields:
            field_value = value.get(field.name, dataclasses.MISSING)
            # We do not check whether field is required here. Rather, we let the
            # attempted instantiation of the dataclass raise if a field is
            # missing
            if field_value is not dataclasses.MISSING:
                try:
                    field_values[field.name] = value_to_type(
                        field_hints[field.name],
                        field_value,
                        custom_converters,
                    )
                except Exception as err:
                    raise TypeError(
                        f"Failed converting field {field.name} on dataclass {hint}",
                    ) from err
        # Simply instantiate the dataclass. This will fail as expected when
        # missing required fields.
        # TODO(cretz): Want way to convert snake case to camel case?
        return hint(**field_values)

    # If there is a @staticmethod or @classmethod parse_obj, we will use it.
    # This covers Pydantic models.
    parse_obj_attr = inspect.getattr_static(hint, "parse_obj", None)
    if isinstance(parse_obj_attr, classmethod) or isinstance(
        parse_obj_attr,
        staticmethod,
    ):
        if not isinstance(value, dict):
            raise TypeError(
                f"Cannot convert to {hint}, value is {type(value)} not dict",
            )
        return getattr(hint, "parse_obj")(value)

    # IntEnum
    if inspect.isclass(hint) and issubclass(hint, IntEnum):
        if not isinstance(value, int):
            raise TypeError(
                f"Cannot convert to enum {hint}, value not an integer, value is {type(value)}",
            )
        return hint(value)

    # StrEnum, available in 3.11+
    if sys.version_info >= (3, 11):
        if inspect.isclass(hint) and issubclass(hint, StrEnum):
            if not isinstance(value, str):
                raise TypeError(
                    f"Cannot convert to enum {hint}, value not a string, value is {type(value)}",
                )
            return hint(value)

    # UUID
    if inspect.isclass(hint) and issubclass(hint, uuid.UUID):
        return hint(value)

    # Iterable. We intentionally put this last as it catches several others.
    if inspect.isclass(origin) and issubclass(origin, collections.abc.Iterable):
        if not isinstance(value, collections.abc.Iterable):
            raise TypeError(f"Expected {hint}, value was {type(value)}")
        ret_list = []
        # If there is no type arg, just return value as is
        if not type_args or (
            len(type_args) == 1
            and (isinstance(type_args[0], TypeVar) or type_args[0] is Ellipsis)
        ):
            ret_list = list(value)
        else:
            # Otherwise convert
            for i, item in enumerate(value):
                # Non-tuples use first type arg, tuples use arg set or one
                # before ellipsis if that's set
                if origin is not tuple:
                    arg_type = type_args[0]
                elif len(type_args) > i and type_args[i] is not Ellipsis:
                    arg_type = type_args[i]
                elif type_args[-1] is Ellipsis:
                    # Ellipsis means use the second to last one
                    arg_type = type_args[-2]
                else:
                    raise TypeError(
                        f"Type {hint} only expecting {len(type_args)} values, got at least {i + 1}",
                    )
                try:
                    ret_list.append(value_to_type(arg_type, item, custom_converters))
                except Exception as err:
                    raise TypeError(f"Failed converting {hint} index {i}") from err
        # If tuple, set, or deque convert back to that type
        if origin is tuple:
            return tuple(ret_list)
        elif origin is set:
            return set(ret_list)
        elif origin is collections.deque:
            return collections.deque(ret_list)
        return ret_list

    raise TypeError(f"Unserializable type during conversion: {hint}")
