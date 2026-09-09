// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Codec } from "./codec.js";
import {
  AttributeMatchOperator as ProtoAttributeMatchOperator,
  type Value as ProtoValue,
} from "./gen/dex.js";
import { encodeValue } from "./value-mapper.js";

interface EncodedAttributeMatch {
  operator: ProtoAttributeMatchOperator;
  operand: ProtoValue;
}

/**
 * Describes a scalar Attribute predicate used by {@link Client.waitForAttributeMatch}.
 *
 * String and boolean Attributes support equality operators. Integer and number
 * Attributes support every operator. A missing Attribute never matches.
 *
 * @example
 * ```ts
 * const match = AttributeMatch.greaterThan(3);
 * ```
 * @typeParam T - Attribute value type.
 */
export class AttributeMatch<T> {
  private constructor(
    private readonly operator: ProtoAttributeMatchOperator,
    private readonly operand: T,
  ) {}

  /**
   * Creates an equality match.
   * @typeParam T - Attribute value type.
   * @param operand - Scalar value to compare.
   * @returns A typed equality match.
   */
  public static equalTo<T>(operand: T): AttributeMatch<T> {
    return new AttributeMatch(ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand);
  }

  /**
   * Creates a not-equal match. A missing Attribute does not match.
   * @typeParam T - Attribute value type.
   * @param operand - Scalar value to compare.
   * @returns A typed not-equal match.
   */
  public static notEqualTo<T>(operand: T): AttributeMatch<T> {
    return new AttributeMatch(ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand);
  }

  /**
   * Creates a greater-than match for an integer or finite number Attribute.
   * @typeParam T - Attribute value type.
   * @param operand - Numeric value to compare.
   * @returns A typed greater-than match.
   */
  public static greaterThan<T>(operand: T): AttributeMatch<T> {
    return new AttributeMatch(ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN, operand);
  }

  /**
   * Creates a greater-than-or-equal match for an integer or finite number Attribute.
   * @typeParam T - Attribute value type.
   * @param operand - Numeric value to compare.
   * @returns A typed greater-than-or-equal match.
   */
  public static greaterThanOrEqual<T>(operand: T): AttributeMatch<T> {
    return new AttributeMatch(
      ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN_OR_EQUAL,
      operand,
    );
  }

  /**
   * Creates a less-than match for an integer or finite number Attribute.
   * @typeParam T - Attribute value type.
   * @param operand - Numeric value to compare.
   * @returns A typed less-than match.
   */
  public static lessThan<T>(operand: T): AttributeMatch<T> {
    return new AttributeMatch(ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_LESS_THAN, operand);
  }

  /**
   * Creates a less-than-or-equal match for an integer or finite number Attribute.
   * @typeParam T - Attribute value type.
   * @param operand - Numeric value to compare.
   * @returns A typed less-than-or-equal match.
   */
  public static lessThanOrEqual<T>(operand: T): AttributeMatch<T> {
    return new AttributeMatch(
      ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_LESS_THAN_OR_EQUAL,
      operand,
    );
  }

  /**
   * Encodes this match for the Client transport.
   * @param codec - Registered Attribute codec.
   * @returns The protocol operator and operand.
   * @internal
   */
  public encode(codec: Codec<T>): EncodedAttributeMatch {
    const operand = encodeValue(codec, this.operand);
    const kind = operand.kind?.$case;
    const isOrdering = this.operator >= ProtoAttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN;
    if (kind !== "stringValue" && kind !== "boolValue" && kind !== "intValue" && kind !== "doubleValue") {
      throw new TypeError(
        "AttributeMatch supports only string, boolean, integer, or number operands",
      );
    }
    if (isOrdering && kind !== "intValue" && kind !== "doubleValue") {
      throw new TypeError("AttributeMatch ordering requires an integer or number operand");
    }
    if (
      operand.kind?.$case === "doubleValue" &&
      !Number.isFinite(operand.kind.value)
    ) {
      throw new TypeError("AttributeMatch number operand must be finite");
    }
    return { operator: this.operator, operand };
  }
}
