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

"""Writes the proto3 JSON of an encryptable ``dex.Value`` and measures it before writing.

The server checks the decoded-bytes budget with ``measure_value_json_bytes``
before it builds the JSON text. Escaping can make the JSON of a string six
times larger than the string.
"""

from __future__ import annotations

import base64
import json
import math

from dex.dexpb import dex_pb2 as pb

def _json_string_byte_weight(byte: int) -> int:
    """Return the JSON bytes that ``json.dumps`` (ASCII output) writes for one UTF-8 byte."""
    if byte in b'"\\\b\f\n\r\t':
        return 2
    if 0x20 <= byte <= 0x7E:
        return 1
    if byte < 0x20 or byte == 0x7F:
        return 6
    # Continuation bytes add nothing; their lead byte counts the whole code point.
    if byte <= 0xBF:
        return 0
    # A 4-byte sequence is outside the BMP and becomes a \uXXXX surrogate pair.
    return 12 if byte >= 0xF0 else 6


_JSON_STRING_BYTE_WEIGHTS = bytes(_json_string_byte_weight(byte) for byte in range(256))
_DISTINCT_STRING_BYTE_WEIGHTS = (1, 2, 6, 12)
_MEASURE_SLICE_CHARACTERS = 1 << 20


def measure_value_json_bytes(value: pb.Value) -> int:
    """Return the exact length of ``serialize_value_json(value)`` without building it.

    Args:
        value: A Value of an encryptable kind.

    Returns:
        The JSON length in bytes. The JSON is ASCII, so bytes equal characters.

    Raises:
        ValueError: For a Value that is not of an encryptable kind.
    """
    kind = value.WhichOneof("kind")
    if kind == "string_value":
        return len('{"stringValue":}') + _measure_json_string_bytes(value.string_value)
    if kind == "int_value":
        return len('{"intValue":""}') + len(str(value.int_value))
    if kind == "double_value":
        return len('{"doubleValue":}') + len(_double_json(value.double_value))
    if kind == "bool_value":
        return len('{"boolValue":}') + len("true" if value.bool_value else "false")
    if kind == "obj_value":
        return _measure_object_json_bytes(value.obj_value)
    raise ValueError("value is not of an encryptable kind")


def serialize_value_json(value: pb.Value) -> str:
    """Return the compact proto3 JSON of ``value``, as section 11 of the format spec describes.

    Args:
        value: A Value of an encryptable kind.

    Returns:
        ASCII JSON text, such as ``{"intValue":"42"}``.

    Raises:
        ValueError: For a Value that is not of an encryptable kind.
    """
    kind = value.WhichOneof("kind")
    if kind == "string_value":
        return '{"stringValue":' + json.dumps(value.string_value) + "}"
    if kind == "int_value":
        return '{"intValue":"' + str(value.int_value) + '"}'
    if kind == "double_value":
        return '{"doubleValue":' + _double_json(value.double_value) + "}"
    if kind == "bool_value":
        return '{"boolValue":' + ("true" if value.bool_value else "false") + "}"
    if kind == "obj_value":
        return _object_json_text(value.obj_value)
    raise ValueError("value is not of an encryptable kind")


def _measure_json_string_bytes(text: str) -> int:
    length = 2
    # Fixed-size slices keep the temporary copies small for long strings.
    for start in range(0, len(text), _MEASURE_SLICE_CHARACTERS):
        weights = (
            text[start : start + _MEASURE_SLICE_CHARACTERS]
            .encode("utf-8")
            .translate(_JSON_STRING_BYTE_WEIGHTS)
        )
        length += sum(weight * weights.count(weight) for weight in _DISTINCT_STRING_BYTE_WEIGHTS)
    return length


def _double_json(number: float) -> str:
    if math.isnan(number):
        return '"NaN"'
    if math.isinf(number):
        return '"Infinity"' if number > 0 else '"-Infinity"'
    return repr(number)


def _measure_object_json_bytes(encoded_object: pb.EncodedObject) -> int:
    """Measure ``_object_json_text``; proto3 JSON omits empty ``encoding`` and ``payload``."""
    field_lengths: list[int] = []
    if encoded_object.encoding:
        field_lengths.append(
            len('"encoding":') + _measure_json_string_bytes(encoded_object.encoding)
        )
    if encoded_object.payload:
        base64_length = 4 * ((len(encoded_object.payload) + 2) // 3)
        field_lengths.append(len('"payload":""') + base64_length)
    separators = max(len(field_lengths) - 1, 0)
    return len('{"objValue":{}}') + sum(field_lengths) + separators


def _object_json_text(encoded_object: pb.EncodedObject) -> str:
    fields: list[str] = []
    if encoded_object.encoding:
        fields.append('"encoding":' + json.dumps(encoded_object.encoding))
    if encoded_object.payload:
        payload = base64.b64encode(encoded_object.payload).decode("ascii")
        fields.append('"payload":"' + payload + '"')
    return '{"objValue":{' + ",".join(fields) + "}}"
