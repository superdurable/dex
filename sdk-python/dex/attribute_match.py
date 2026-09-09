# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dataclasses import dataclass
from math import isfinite
from typing import Any, Generic, TypeVar

from dex._value_mapper import ValueMapper
from dex.codec import Codec
from dex.dexpb import dex_pb2 as pb

ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class AttributeMatch(Generic[ValueT]):
    """Describe a scalar Attribute predicate.

    Create matches with the class methods and pass them to
    :meth:`Client.wait_for_attribute_match` or
    :meth:`AsyncClient.wait_for_attribute_match`. String and bool Attributes
    support equality operators. Int and float Attributes support every operator.
    A missing Attribute never matches.

    Examples:
        >>> match = AttributeMatch.greater_than(3)
    """

    _operator: pb.AttributeMatchOperator
    _operand: ValueT

    @classmethod
    def equal_to(cls, operand: ValueT) -> AttributeMatch[ValueT]:
        """Return a match requiring equality with ``operand``."""
        return cls(pb.ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand)

    @classmethod
    def not_equal_to(cls, operand: ValueT) -> AttributeMatch[ValueT]:
        """Return a match requiring an existing value different from ``operand``."""
        return cls(pb.ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand)

    @classmethod
    def greater_than(cls, operand: ValueT) -> AttributeMatch[ValueT]:
        """Return a numeric match requiring a value greater than ``operand``."""
        return cls(pb.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN, operand)

    @classmethod
    def greater_than_or_equal(cls, operand: ValueT) -> AttributeMatch[ValueT]:
        """Return a numeric greater-than-or-equal match."""
        return cls(pb.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN_OR_EQUAL, operand)

    @classmethod
    def less_than(cls, operand: ValueT) -> AttributeMatch[ValueT]:
        """Return a numeric match requiring a value less than ``operand``."""
        return cls(pb.ATTRIBUTE_MATCH_OPERATOR_LESS_THAN, operand)

    @classmethod
    def less_than_or_equal(cls, operand: ValueT) -> AttributeMatch[ValueT]:
        """Return a numeric less-than-or-equal match."""
        return cls(pb.ATTRIBUTE_MATCH_OPERATOR_LESS_THAN_OR_EQUAL, operand)


def _encode_attribute_match(
    match: AttributeMatch[Any],
    values: ValueMapper,
    codec: Codec[Any],
) -> pb.AttributeMatch:
    if not isinstance(match, AttributeMatch):
        raise TypeError("wait_for_attribute_match requires an AttributeMatch")
    encoded = values.encode(match._operand, codec)
    kind = encoded.WhichOneof("kind")
    if kind not in {"string_value", "bool_value", "int_value", "double_value"}:
        raise ValueError(
            "AttributeMatch supports only string, boolean, integer, or float operands"
        )
    is_ordering = match._operator >= pb.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN
    if is_ordering and kind not in {"int_value", "double_value"}:
        raise ValueError("AttributeMatch ordering requires an integer or float operand")
    if kind == "double_value" and not isfinite(encoded.double_value):
        raise ValueError("AttributeMatch float operand must be finite")
    return pb.AttributeMatch(operator=match._operator, operand=encoded)
