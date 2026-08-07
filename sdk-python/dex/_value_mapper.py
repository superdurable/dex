# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import json
from dataclasses import asdict, is_dataclass
from datetime import datetime
from enum import Enum
from typing import Any, cast

from google.protobuf import struct_pb2

from dex.attribute import AttributeIndex, IndexType
from dex.codec import Codec, CodecRegistry, Value, WireKind
from dex.dexpb import dex_pb2 as pb

_JSON_ENCODING = "json"
_RAW_BYTES_ENCODING = "rawbytes"


class ValueMapper:
    def __init__(self, codecs: CodecRegistry) -> None:
        self._codecs = codecs

    def codec(self, value_type: object) -> Codec[Any]:
        return self._codecs.resolve(value_type)

    def encode(self, value: Any, codec: Codec[Any]) -> pb.Value:
        if value is None:
            return pb.Value(
                obj_value=pb.EncodedObject(
                    encoding=_JSON_ENCODING,
                    payload=b"null",
                )
            )
        logical = codec.encode(value)
        if logical.kind is WireKind.STRING:
            return pb.Value(string_value=self._require(logical.data, str))
        if logical.kind is WireKind.BOOL:
            return pb.Value(bool_value=self._require_exact(logical.data, bool))
        if logical.kind is WireKind.INT64:
            return pb.Value(int_value=self._require_exact(logical.data, int))
        if logical.kind is WireKind.DOUBLE:
            return pb.Value(double_value=self._require_exact(logical.data, float))
        if logical.kind is WireKind.BYTES:
            return pb.Value(
                obj_value=pb.EncodedObject(
                    encoding=_RAW_BYTES_ENCODING,
                    payload=self._require(logical.data, bytes),
                )
            )
        if logical.kind is WireKind.JSON:
            payload = self._require(logical.data, str).encode("utf-8")
            return pb.Value(
                obj_value=pb.EncodedObject(
                    encoding=_JSON_ENCODING,
                    payload=payload,
                )
            )
        raise TypeError(f"unsupported wire kind {logical.kind}")

    def encode_dynamic(self, value: Any) -> pb.Value:
        if value is None:
            return self.encode(value, self._codecs.resolve(type(None)))
        value_type = type(value)
        try:
            codec = self._codecs.resolve(value_type)
        except TypeError:
            if isinstance(value, (list, tuple, dict)):
                return pb.Value(
                    obj_value=pb.EncodedObject(
                        encoding=_JSON_ENCODING,
                        payload=self._encode_json_collection(value),
                    )
                )
            raise
        return self.encode(value, codec)

    def decode(self, value: pb.Value, codec: Codec[Any]) -> Any:
        kind = value.WhichOneof("kind")
        expected = codec.wire_kind
        if kind == "string_value" and expected is WireKind.STRING:
            return codec.decode(Value(WireKind.STRING, value.string_value))
        if kind == "bool_value" and expected is WireKind.BOOL:
            return codec.decode(Value(WireKind.BOOL, value.bool_value))
        if kind == "int_value" and expected is WireKind.INT64:
            return codec.decode(Value(WireKind.INT64, value.int_value))
        if kind == "double_value" and expected is WireKind.DOUBLE:
            return codec.decode(Value(WireKind.DOUBLE, value.double_value))
        if kind == "obj_value" and expected is WireKind.BYTES:
            self._require_encoding(value.obj_value, _RAW_BYTES_ENCODING)
            return codec.decode(Value(WireKind.BYTES, value.obj_value.payload))
        if kind == "obj_value" and expected is WireKind.JSON:
            self._require_encoding(value.obj_value, _JSON_ENCODING)
            payload = value.obj_value.payload.decode("utf-8")
            return codec.decode(Value(WireKind.JSON, payload))
        if kind == "obj_value" and value.obj_value.encoding == _JSON_ENCODING:
            if value.obj_value.payload == b"null":
                return None
        if kind in (
            "internal_blob_id_for_string_value",
            "internal_blob_id_for_obj_value",
        ):
            raise ValueError("blob-backed Value was not hydrated")
        if kind == "null_value":
            raise ValueError("attribute deletion marker cannot be decoded")
        raise TypeError(f"cannot decode {kind or 'empty Value'} as {codec.type_name}")

    @staticmethod
    def deletion() -> pb.Value:
        return pb.Value(null_value=cast(struct_pb2.NullValue, struct_pb2.NULL_VALUE))

    @staticmethod
    def index_config(
        index: AttributeIndex | None,
        dynamic: bool,
    ) -> pb.IndexConfig | None:
        if index is None:
            return None
        mapped_types = {
            IndexType.KEYWORD: pb.INDEX_TYPE_KEYWORD,
            IndexType.FULL_TEXT: pb.INDEX_TYPE_TEXT,
            IndexType.KEYWORD_ARRAY: pb.INDEX_TYPE_KEYWORD_ARRAY,
            IndexType.INT: pb.INDEX_TYPE_INT,
            IndexType.DOUBLE: pb.INDEX_TYPE_DOUBLE,
            IndexType.BOOL: pb.INDEX_TYPE_BOOL,
            IndexType.DATETIME: pb.INDEX_TYPE_DATETIME,
        }
        config = pb.IndexConfig(enable=True, type=mapped_types[index.type])
        if index.index_key or dynamic:
            config.index_key = index.index_key
        return config

    @staticmethod
    def _require(value: object, expected: type[Any]) -> Any:
        if not isinstance(value, expected):
            raise TypeError(f"expected {expected.__name__}")
        return value

    @staticmethod
    def _require_exact(value: object, expected: type[Any]) -> Any:
        if type(value) is not expected:
            raise TypeError(f"expected {expected.__name__}")
        return value

    @staticmethod
    def _require_encoding(value: pb.EncodedObject, expected: str) -> None:
        if value.encoding != expected:
            raise TypeError(f"expected {expected} encoding, got {value.encoding}")

    @staticmethod
    def _encode_json_collection(value: object) -> bytes:
        def default(item: object) -> object:
            if is_dataclass(item) and not isinstance(item, type):
                return asdict(item)
            if isinstance(item, datetime):
                return item.isoformat()
            if isinstance(item, Enum):
                return item.value
            raise TypeError(f"cannot encode {type(item).__name__} as JSON")

        return json.dumps(
            value,
            default=default,
            allow_nan=False,
            separators=(",", ":"),
            sort_keys=True,
        ).encode("utf-8")
