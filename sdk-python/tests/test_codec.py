# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from enum import Enum
from pathlib import Path
from typing import Any, Generic, Optional, TypeVar

import pytest

from dex import CodecRegistry, JsonCodec, Value, WireKind

from integ.json_codec_models import (
    CodecEnvelope,
    CodecFailure,
    CodecItem,
    CodecReceipt,
    CodecResult,
)

NestedT = TypeVar("NestedT")


@dataclass
class OptionalItem:
    item: Optional[CodecItem]


@dataclass
class InheritedResult(CodecResult[list[NestedT]], Generic[NestedT]):
    pass


@dataclass
class RecursiveItem:
    count: int
    next_item: "Optional[RecursiveItem]" = None


class LegacyStatus(Enum):
    READY = "ready"


@dataclass
class LegacyPayload:
    call_id: str
    observed_at: datetime
    item: CodecItem
    status: LegacyStatus = LegacyStatus.READY
    failure: None = None
    retry_count: int = 0
    labels: list[str] = field(default_factory=list)


@dataclass
class OptionalTimestamp:
    observed_at: datetime | None = field(metadata={"dex_json_format": "rfc3339"})


def test_generic_dataclasses_substitute_nested_type_arguments() -> None:
    result = CodecResult("success", CodecItem(42), None)
    envelope = CodecEnvelope([result], {"result": result})
    codec = CodecRegistry().resolve(CodecEnvelope[CodecItem])
    decoded = codec.decode(codec.encode(envelope))
    assert decoded == envelope
    assert type(decoded.results[0].value) is CodecItem
    assert type(decoded.indexed["result"].value) is CodecItem

    nested_codec = CodecRegistry().resolve(CodecResult[CodecResult[CodecItem]])
    nested = CodecResult("success", result, None)
    assert nested_codec.decode(nested_codec.encode(nested)) == nested


def test_inherited_generic_dataclass_substitutes_base_type_arguments() -> None:
    codec = CodecRegistry().resolve(InheritedResult[CodecItem])
    result = InheritedResult("success", [CodecItem(42)], None)
    assert codec.decode(codec.encode(result)) == result


@pytest.mark.parametrize("type_hint", [Optional[CodecItem], CodecItem | None])
def test_optional_dataclasses_decode_objects_and_null(type_hint: object) -> None:
    codec = CodecRegistry().resolve(type_hint)
    assert codec.decode(codec.encode(CodecItem(42))) == CodecItem(42)
    assert codec.decode(codec.encode(None)) is None
    nested_codec = CodecRegistry().resolve(OptionalItem)
    nested = nested_codec.decode(Value(WireKind.JSON, '{"item":{"count":42}}'))
    assert type(nested.item) is CodecItem
    assert nested_codec.decode(nested_codec.encode(OptionalItem(None))).item is None


def test_recursive_optional_dataclasses() -> None:
    codec = CodecRegistry().resolve(RecursiveItem)
    value = RecursiveItem(1, RecursiveItem(2))
    assert codec.decode(codec.encode(value)) == value


@pytest.mark.parametrize(
    ("type_hint", "value"),
    [
        (int | str, 42),
        (int | str, "ready"),
        (int | bool, True),
        (int | bool, 42),
        (CodecItem | str | None, CodecItem(42)),
        (CodecItem | str | None, "ready"),
        (CodecItem | str | None, None),
        (list[CodecItem] | str, [CodecItem(42)]),
        (Mapping[str, CodecItem] | int, {"one": CodecItem(42)}),
        (Sequence[CodecItem] | int, [CodecItem(42)]),
        (tuple[CodecItem, str] | int, (CodecItem(42), "ready")),
        (tuple[CodecItem, ...] | int, (CodecItem(42), CodecItem(43))),
        (LegacyStatus | int, LegacyStatus.READY),
    ],
)
def test_disjoint_json_unions_round_trip(type_hint: object, value: Any) -> None:
    codec = CodecRegistry().resolve(type_hint)
    assert codec.decode(codec.encode(value)) == value


@pytest.mark.parametrize(
    "type_hint",
    [
        int | float,
        str | datetime,
        CodecItem | CodecFailure,
        CodecItem | dict[str, int],
        list[int] | list[str],
        list[int] | tuple[int, ...],
        LegacyStatus | str,
        Any | int,
        CodecResult[int | float],
        list[CodecResult[CodecItem | CodecFailure]],
    ],
)
def test_ambiguous_json_unions_fail_at_resolution(type_hint: object) -> None:
    with pytest.raises(TypeError, match="ambiguous JSON union"):
        CodecRegistry().resolve(type_hint)


def test_union_rejects_unmatched_json_kind_and_wrong_member_payload() -> None:
    codec = CodecRegistry().resolve(CodecItem | str | None)
    with pytest.raises(TypeError, match="cannot decode JSON boolean"):
        codec.decode(Value(WireKind.JSON, "true"))
    with pytest.raises(TypeError, match="expected int"):
        codec.decode(Value(WireKind.JSON, '{"count":"42"}'))
    with pytest.raises(TypeError, match="requires"):
        codec.encode(42)


def test_defaults_restore_absent_fields_and_factories_create_fresh_values() -> None:
    codec = CodecRegistry().resolve(CodecResult[int])
    payload = Value(WireKind.JSON, '{"branch":"success","value":42,"receipt":null}')
    first = codec.decode(payload)
    second = codec.decode(payload)
    assert first == CodecResult("success", 42, None)
    first.labels.append("first")
    assert second.labels == []
    assert _json_payload(codec.encode(second)) == {
        "branch": "success",
        "value": 42,
        "receipt": None,
        "failure": None,
    }
    first.attempts = 1
    encoded = _json_payload(codec.encode(first))
    assert encoded["labels"] == ["first"]
    assert encoded["attempts"] == 1


def test_defaulted_fields_preserve_explicit_null_and_reject_wrong_payloads() -> None:
    codec = CodecRegistry().resolve(CodecResult[int])
    payload = {
        "branch": "success",
        "value": 42,
        "receipt": None,
        "failure": {"message": "failed"},
    }
    result = codec.decode(Value(WireKind.JSON, json.dumps(payload)))
    assert result.failure == CodecFailure("failed")
    payload["failure"] = None
    assert codec.decode(Value(WireKind.JSON, json.dumps(payload))).failure is None
    payload["attempts"] = None
    with pytest.raises(TypeError, match="expected int"):
        codec.decode(Value(WireKind.JSON, json.dumps(payload)))


def test_missing_required_wire_key_has_clear_codec_error() -> None:
    codec = CodecRegistry().resolve(CodecReceipt)
    with pytest.raises(
        ValueError, match="CodecReceipt missing required JSON field 'callId'"
    ):
        codec.decode(Value(WireKind.JSON, '{"observedAt":"2026-10-30T07:00:00Z"}'))


@pytest.mark.parametrize(
    "metadata",
    [
        {"dex_json_name": ""},
        {"dex_json_name": 1},
        {"dex_json_omit_default": "yes"},
        {"dex_json_omit_default": True},
        {"dex_json_format": "rfc3339"},
        {"dex_json_format": "other"},
    ],
)
def test_invalid_metadata_fails_at_resolution(metadata: dict[str, Any]) -> None:
    @dataclass
    class InvalidMetadata:
        value: int = field(metadata=metadata)

    with pytest.raises(TypeError):
        CodecRegistry().resolve(InvalidMetadata)


def test_duplicate_wire_names_fail_at_resolution() -> None:
    @dataclass
    class DuplicateNames:
        first: int = field(metadata={"dex_json_name": "second"})
        second: int

    with pytest.raises(TypeError, match="duplicate JSON field name 'second'"):
        CodecRegistry().resolve(DuplicateNames)


def test_go_golden_json_decodes_dataclasses_and_reencodes_equivalently() -> None:
    golden = (
        Path(__file__)
        .with_name("fixtures")
        .joinpath("json_codec", "go_result.json")
        .read_bytes()
    )
    codec = CodecRegistry().resolve(CodecResult[CodecItem])
    result = codec.decode(Value(WireKind.JSON, golden.decode()))
    assert result == CodecResult(
        "success",
        CodecItem(42),
        CodecReceipt(
            "call-42", datetime(2026, 10, 30, 7, 0, 0, 120000, tzinfo=timezone.utc)
        ),
    )
    assert _json_payload(codec.encode(result)) == json.loads(golden)


@pytest.mark.parametrize(
    "timestamp",
    [
        "2026-10-30T07:00:00Z",
        "2026-10-30T07:00:00+00:00",
        "2026-10-30T07:00:00.123456789Z",
    ],
)
def test_datetime_accepts_go_strings_without_format_metadata(timestamp: str) -> None:
    codec = CodecRegistry().resolve(LegacyPayload)
    payload = {"call_id": "call-1", "observed_at": timestamp, "item": {"count": 1}}
    decoded = codec.decode(Value(WireKind.JSON, json.dumps(payload)))
    assert decoded.observed_at == datetime.fromisoformat(timestamp)
    assert decoded.observed_at.tzinfo == timezone.utc


@pytest.mark.parametrize(
    ("timestamp", "expected"),
    [
        (datetime(2026, 10, 30, 7, tzinfo=timezone.utc), "2026-10-30T07:00:00Z"),
        (
            datetime(
                2026,
                10,
                30,
                7,
                microsecond=120000,
                tzinfo=timezone(timedelta(hours=-7)),
            ),
            "2026-10-30T07:00:00.12-07:00",
        ),
    ],
)
def test_rfc3339_format_matches_go_timestamp_spelling(
    timestamp: datetime, expected: str
) -> None:
    codec = CodecRegistry().resolve(CodecReceipt)
    encoded = codec.encode(CodecReceipt("call-1", timestamp))
    assert _json_payload(encoded)["observedAt"] == expected
    assert codec.decode(encoded) == CodecReceipt("call-1", timestamp)


def test_rfc3339_format_rejects_naive_timestamps() -> None:
    codec = CodecRegistry().resolve(CodecReceipt)
    with pytest.raises(ValueError, match="requires a timezone"):
        codec.encode(CodecReceipt("call-1", datetime(2026, 10, 30)))


def test_optional_rfc3339_format_preserves_null_and_rejects_subminute_offsets() -> None:
    codec = CodecRegistry().resolve(OptionalTimestamp)
    assert codec.decode(codec.encode(OptionalTimestamp(None))) == OptionalTimestamp(
        None
    )
    timestamp = datetime(2026, 10, 30, tzinfo=timezone(timedelta(seconds=30)))
    with pytest.raises(ValueError, match="whole-minute offset"):
        codec.encode(OptionalTimestamp(timestamp))


def test_existing_dataclass_payload_is_unchanged_without_metadata() -> None:
    codec = CodecRegistry().resolve(LegacyPayload)
    value = LegacyPayload(
        "call-1", datetime(2026, 10, 30, 7, tzinfo=timezone.utc), CodecItem(42)
    )
    encoded = codec.encode(value)
    assert encoded == Value(
        WireKind.JSON,
        '{"call_id":"call-1","failure":null,"item":{"count":42},"labels":[],"observed_at":"2026-10-30T07:00:00+00:00","retry_count":0,"status":"ready"}',
    )
    assert codec.decode(encoded) == value


def test_existing_homogeneous_tuple_payload_is_unchanged() -> None:
    codec = CodecRegistry().resolve(tuple[int])
    encoded = codec.encode((1, 2, 3))
    assert encoded == Value(WireKind.JSON, "[1,2,3]")
    assert codec.decode(encoded) == (1, 2, 3)


def test_explicit_codec_overrides_automatic_union_validation() -> None:
    codec = JsonCodec[object]("custom", lambda value: value)
    registry = CodecRegistry({int | float: codec})
    assert registry.resolve(int | float) is codec


def _json_payload(value: Value) -> Any:
    assert value.kind is WireKind.JSON
    assert isinstance(value.data, str)
    return json.loads(value.data)
