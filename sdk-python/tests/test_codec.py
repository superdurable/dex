# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field, make_dataclass
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from enum import Enum, StrEnum
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable, Generic, Literal, Optional, TypeVar

import pytest

from dex import CodecRegistry, JsonCodec, Value, WireKind
from dex._value_mapper import ValueMapper
from dex.runtime_errors import ValueMappingError

from integ.json_codec_models import (
    CodecEnvelope,
    CodecFailure,
    CodecItem,
    CodecReceipt,
    CodecResult,
)

if TYPE_CHECKING:
    from decimal import Context as TypeCheckingOnlyContext

NestedT = TypeVar("NestedT")
FirstT = TypeVar("FirstT")
SecondT = TypeVar("SecondT")


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


class LiteralStage(StrEnum):
    SENT = "sent"


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


@dataclass
class ReusedBase(Generic[NestedT]):
    base_value: NestedT


@dataclass
class ReusedChild(ReusedBase[list[NestedT]], Generic[NestedT]):
    child_value: NestedT


@dataclass
class ReusedLeaf(ReusedChild[CodecItem]):
    pass


@dataclass
class OrderedPair(Generic[FirstT, SecondT]):
    first: FirstT
    second: SecondT


@dataclass
class SwappedPair(OrderedPair[SecondT, FirstT], Generic[FirstT, SecondT]):
    pass


@dataclass
class UnboundMutation(Generic[NestedT]):
    branch: str
    value: NestedT | None = None


@dataclass
class IntegerBox(ReusedBase[int]):
    pass


@dataclass
class ItemBox(ReusedBase[CodecItem]):
    pass


@dataclass
class OptionalRatio:
    ratio: float | None = None


@dataclass
class RequiredRatio:
    ratio: float


@dataclass
class OptionalLegacyFields:
    observed_at: datetime | None = None
    status: LegacyStatus | None = None


@dataclass
class UnresolvedAnnotations:
    context: "TypeCheckingOnlyContext | None" = None
    callback: Callable[[int], int] | None = None


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
    ],
)
def test_ambiguous_json_unions_fail_at_resolution(type_hint: object) -> None:
    with pytest.raises(TypeError, match="ambiguous JSON union"):
        CodecRegistry().resolve(type_hint)


@pytest.mark.parametrize(
    ("field_type", "payload"),
    [
        (int | float, 1),
        (int | float, 1.5),
        (int | float | None, None),
        (Optional[Any], {"nested": [1, None]}),
        (Any | None, "text"),
        (Optional[Literal["first", "second"]], "unknown"),
        (bytes | None, "raw"),
        (Decimal | None, 1.5),
        (str | datetime, "2026-10-30T07:00:00+00:00"),
        (float | None, 1),
        (int | None, "not-a-number"),
        (CodecItem | CodecFailure, {"count": 1}),
        (tuple[int, ...] | None, [1, 2]),
        (list[int] | None, ["text"]),
        (dict[str, int] | None, {"count": "text"}),
        (list[int | str], [1, "text", 1.5]),
        (Mapping[str, int], {"count": "text"}),
        (Sequence[str], "text"),
    ],
)
def test_nested_unions_and_abstract_containers_keep_earlier_json_values(
    field_type: object, payload: Any
) -> None:
    holder = make_dataclass("LegacyHolder", [("value", field_type)])
    codec = CodecRegistry().resolve(holder)
    data = json.dumps({"value": payload}, separators=(",", ":"))
    decoded = codec.decode(Value(WireKind.JSON, data))
    assert decoded.value == payload
    assert type(decoded.value) is type(payload)
    assert codec.encode(decoded) == Value(WireKind.JSON, data)


@pytest.mark.parametrize(
    ("type_hint", "data", "expected"),
    [
        (
            CodecResult[int | float],
            '{"branch":"success","receipt":null,"value":1}',
            CodecResult("success", 1, None),
        ),
        (
            list[CodecResult[CodecItem | CodecFailure]],
            '[{"branch":"success","receipt":null,"value":{"count":1}}]',
            [CodecResult("success", {"count": 1}, None)],
        ),
    ],
)
def test_nested_ambiguous_unions_resolve_and_keep_json_values(
    type_hint: object, data: str, expected: Any
) -> None:
    codec = CodecRegistry().resolve(type_hint)
    assert codec.decode(Value(WireKind.JSON, data)) == expected


def test_nested_optional_datetime_and_enum_fields_decode_typed_values() -> None:
    codec = CodecRegistry().resolve(OptionalLegacyFields)
    decoded = codec.decode(
        Value(
            WireKind.JSON,
            '{"observed_at":"2026-10-30T07:00:00+00:00","status":"ready"}',
        )
    )
    assert decoded == OptionalLegacyFields(
        datetime(2026, 10, 30, 7, tzinfo=timezone.utc), LegacyStatus.READY
    )


def test_unparameterized_generic_with_optional_type_variable_keeps_json_values() -> (
    None
):
    codec = CodecRegistry().resolve(UnboundMutation)
    encoded = codec.encode(UnboundMutation("success", 3))
    assert encoded == Value(WireKind.JSON, '{"branch":"success","value":3}')
    assert codec.decode(encoded) == UnboundMutation("success", 3)
    dynamic = ValueMapper(CodecRegistry()).encode_dynamic(UnboundMutation("success", 3))
    assert dynamic.obj_value.payload == b'{"branch":"success","value":3}'


@pytest.mark.parametrize("holder", [OptionalRatio, RequiredRatio])
def test_float_fields_accept_json_integers_and_keep_their_bytes(
    holder: type[Any],
) -> None:
    codec = CodecRegistry().resolve(holder)
    decoded = codec.decode(Value(WireKind.JSON, '{"ratio":1}'))
    assert type(decoded.ratio) is int
    assert codec.encode(decoded) == Value(WireKind.JSON, '{"ratio":1}')


def test_float_containers_accept_json_integers() -> None:
    codec = CodecRegistry().resolve(list[float])
    decoded = codec.decode(Value(WireKind.JSON, "[1,2.5]"))
    assert decoded == [1, 2.5]
    assert codec.encode(decoded) == Value(WireKind.JSON, "[1,2.5]")


def test_inherited_type_variable_fields_decode_only_structured_bindings() -> None:
    integer_codec = CodecRegistry().resolve(IntegerBox)
    assert (
        integer_codec.decode(Value(WireKind.JSON, '{"base_value":"7"}')).base_value
        == "7"
    )
    item_codec = CodecRegistry().resolve(ItemBox)
    decoded = item_codec.decode(Value(WireKind.JSON, '{"base_value":{"count":7}}'))
    assert decoded == ItemBox(CodecItem(7))


def test_unresolvable_annotations_resolve_and_encode_like_earlier_releases() -> None:
    codec = CodecRegistry().resolve(UnresolvedAnnotations)
    encoded = codec.encode(UnresolvedAnnotations())
    assert encoded == Value(WireKind.JSON, '{"callback":null,"context":null}')
    dynamic = ValueMapper(CodecRegistry()).encode_dynamic(UnresolvedAnnotations())
    assert dynamic.obj_value.payload == b'{"callback":null,"context":null}'
    with pytest.raises(NameError):
        codec.decode(encoded)


def test_reused_type_variables_bind_per_class() -> None:
    codec = CodecRegistry().resolve(ReusedChild[CodecItem])
    value = ReusedChild([CodecItem(1)], CodecItem(2))
    decoded = codec.decode(codec.encode(value))
    assert decoded == value
    assert type(decoded.base_value[0]) is CodecItem
    assert type(decoded.child_value) is CodecItem


def test_permuted_type_variables_bind_by_position() -> None:
    codec = CodecRegistry().resolve(SwappedPair[CodecItem, CodecFailure])
    value = SwappedPair(CodecFailure("failed"), CodecItem(1))
    decoded = codec.decode(codec.encode(value))
    assert decoded == value
    assert type(decoded.first) is CodecFailure
    assert type(decoded.second) is CodecItem


def test_multilevel_generic_hierarchy_binds_each_level() -> None:
    codec = CodecRegistry().resolve(ReusedLeaf)
    decoded = codec.decode(
        Value(
            WireKind.JSON,
            '{"base_value":[{"count":1}],"child_value":{"count":2}}',
        )
    )
    assert decoded == ReusedLeaf([CodecItem(1)], CodecItem(2))
    assert type(decoded.base_value[0]) is CodecItem
    assert type(decoded.child_value) is CodecItem


def test_union_rejects_unmatched_json_kind_and_wrong_member_payload() -> None:
    codec = CodecRegistry().resolve(CodecItem | str | None)
    with pytest.raises(TypeError, match="cannot decode JSON boolean"):
        codec.decode(Value(WireKind.JSON, "true"))
    with pytest.raises(TypeError, match="expected int"):
        codec.decode(Value(WireKind.JSON, '{"count":"42"}'))
    with pytest.raises(TypeError, match="requires"):
        codec.encode(42)


@pytest.mark.parametrize(
    ("type_hint", "data", "expected"),
    [
        (Literal["draft", "sent"] | None, '"sent"', "sent"),
        (Literal["draft", "sent"] | None, "null", None),
        (Literal[1, 2] | str, "2", 2),
        (Literal[1, 2] | str, '"other"', "other"),
        (Literal[True] | int, "true", True),
        (Literal[True] | int, "1", 1),
        (Literal[1, True] | None, "1", 1),
        (Literal[1, True] | None, "true", True),
        (Literal[LiteralStage.SENT] | None, '"sent"', LiteralStage.SENT),
    ],
)
def test_direct_union_literal_alternatives_decode_members(
    type_hint: object, data: str, expected: Any
) -> None:
    codec = CodecRegistry().resolve(type_hint)
    decoded = codec.decode(Value(WireKind.JSON, data))
    assert decoded == expected
    assert type(decoded) is type(expected)
    assert codec.encode(decoded) == Value(WireKind.JSON, data)


@pytest.mark.parametrize(
    ("type_hint", "data"),
    [
        (Literal["draft", "sent"] | None, '"unknown"'),
        (Literal[1, 2] | str, "3"),
        (Literal[1, 2] | str, "1.0"),
        (Literal[True] | int, "false"),
        (Literal[1, True] | None, "2"),
    ],
)
def test_direct_union_literal_alternatives_reject_non_members(
    type_hint: object, data: str
) -> None:
    codec = CodecRegistry().resolve(type_hint)
    with pytest.raises(ValueError, match="is not a member of"):
        codec.decode(Value(WireKind.JSON, data))


def test_direct_union_literal_rejects_boolean_for_numeric_members() -> None:
    codec = CodecRegistry().resolve(Literal[0, 1] | None)
    with pytest.raises(TypeError, match="cannot decode JSON boolean"):
        codec.decode(Value(WireKind.JSON, "true"))


@pytest.mark.parametrize(
    "field_type", [Literal["first", "second"], Literal[1, 2] | None]
)
def test_nested_literal_fields_keep_earlier_json_values(field_type: object) -> None:
    holder = make_dataclass("LegacyLiteralHolder", [("value", field_type)])
    codec = CodecRegistry().resolve(holder)
    assert codec.decode(Value(WireKind.JSON, '{"value":"unknown"}')).value == "unknown"
    assert codec.decode(Value(WireKind.JSON, '{"value":3}')).value == 3


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


@dataclass
class DuplicateWireNames:
    left: int = field(metadata={"dex_json_name": "same"})
    right: int = field(metadata={"dex_json_name": "same"})


@dataclass
class OmitWithoutDefault:
    value: int = field(metadata={"dex_json_omit_default": True})


@dataclass
class NonBoolOmitDefault:
    value: int = field(default=0, metadata={"dex_json_omit_default": "yes"})


@dataclass
class UnknownFormat:
    value: datetime = field(metadata={"dex_json_format": "iso"})


@dataclass
class FormatOnInteger:
    value: int = field(metadata={"dex_json_format": "rfc3339"})


@dataclass
class DuplicateWireNamesHolder:
    item: Any


@pytest.mark.parametrize(
    "value",
    [
        DuplicateWireNames(1, 2),
        OmitWithoutDefault(1),
        NonBoolOmitDefault(1),
        UnknownFormat(datetime(2026, 10, 30, 7, tzinfo=timezone.utc)),
        FormatOnInteger(1),
    ],
)
def test_dynamic_collections_reject_invalid_metadata_like_direct_values(
    value: Any,
) -> None:
    values = ValueMapper(CodecRegistry())
    for dynamic_value in (value, [value], (value,), {"item": value}):
        with pytest.raises(ValueMappingError):
            values.encode_dynamic(dynamic_value)


def test_dynamic_collections_reject_invalid_metadata_in_untyped_fields() -> None:
    values = ValueMapper(CodecRegistry())
    holder = DuplicateWireNamesHolder(DuplicateWireNames(1, 2))
    with pytest.raises(ValueMappingError, match="duplicate JSON field name 'same'"):
        values.encode_dynamic(holder)
    with pytest.raises(TypeError, match="duplicate JSON field name 'same'"):
        CodecRegistry().resolve(DuplicateWireNamesHolder).encode(holder)
    with pytest.raises(ValueMappingError, match="duplicate JSON field name 'same'"):
        values.encode_dynamic([holder])
    with pytest.raises(ValueMappingError, match="duplicate JSON field name 'same'"):
        values.encode_dynamic({"items": [{"item": DuplicateWireNames(1, 2)}]})


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


@pytest.mark.parametrize("type_hint", [tuple[int], tuple[int, int]])
def test_existing_homogeneous_tuple_payload_is_unchanged(type_hint: object) -> None:
    codec = CodecRegistry().resolve(type_hint)
    encoded = codec.encode((1, 2, 3))
    assert encoded == Value(WireKind.JSON, "[1,2,3]")
    assert codec.decode(encoded) == (1, 2, 3)


def test_dynamic_collections_encode_dataclasses_with_json_metadata() -> None:
    values = ValueMapper(CodecRegistry())
    receipt = CodecReceipt("call-1", datetime(2026, 10, 30, 7, tzinfo=timezone.utc))
    expected = b'{"callId":"call-1","observedAt":"2026-10-30T07:00:00Z"}'
    encoded = values.encode_dynamic([receipt]).obj_value.payload
    assert encoded == b"[" + expected + b"]"
    assert values.encode_dynamic({"receipt": receipt}).obj_value.payload == (
        b'{"receipt":' + expected + b"}"
    )
    codec = CodecRegistry().resolve(list[CodecReceipt])
    assert codec.decode(Value(WireKind.JSON, encoded.decode())) == [receipt]


def test_dynamic_collections_of_plain_dataclasses_are_unchanged() -> None:
    values = ValueMapper(CodecRegistry())
    value = LegacyPayload(
        "call-1", datetime(2026, 10, 30, 7, tzinfo=timezone.utc), CodecItem(42)
    )
    expected = b'{"call_id":"call-1","failure":null,"item":{"count":42},"labels":[],"observed_at":"2026-10-30T07:00:00+00:00","retry_count":0,"status":"ready"}'
    assert values.encode_dynamic([value]).obj_value.payload == b"[" + expected + b"]"
    assert values.encode_dynamic((value,)).obj_value.payload == b"[" + expected + b"]"
    assert values.encode_dynamic({"k": value}).obj_value.payload == (
        b'{"k":' + expected + b"}"
    )


def test_explicit_codec_overrides_automatic_union_validation() -> None:
    codec = JsonCodec[object]("custom", lambda value: value)
    registry = CodecRegistry({int | float: codec})
    assert registry.resolve(int | float) is codec


def _json_payload(value: Value) -> Any:
    assert value.kind is WireKind.JSON
    assert isinstance(value.data, str)
    return json.loads(value.data)
