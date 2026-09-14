/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import io.superdurable.gen.AttributeMatchOperator;

/**
 * Describes a scalar Attribute predicate.
 *
 * <p>Create a match with a static factory and pass it to {@link
 * Client#waitForAttributeMatch(String, Attribute, AttributeMatch, WaitForAttributeOptions)}.
 * String and Boolean Attributes support equality operators. Integer and floating-point Attributes
 * support every operator. A missing Attribute never matches.
 *
 * <pre>{@code
 * AttributeMatch<Long> changed = AttributeMatch.greaterThan(3L);
 * }</pre>
 *
 * @param <T> the Attribute value type
 */
public final class AttributeMatch<T> {
    private final AttributeMatchOperator operator;
    private final T operand;

    private AttributeMatch(final AttributeMatchOperator operator, final T operand) {
        this.operator = operator;
        this.operand = operand;
    }

    /**
     * Creates an equality match.
     *
     * @param operand the scalar value to compare
     * @param <T> the Attribute value type
     * @return an equality match
     */
    public static <T> AttributeMatch<T> equalTo(final T operand) {
        return new AttributeMatch<>(AttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand);
    }

    /**
     * Creates a not-equal match. A missing Attribute does not match.
     *
     * @param operand the scalar value to compare
     * @param <T> the Attribute value type
     * @return a not-equal match
     */
    public static <T> AttributeMatch<T> notEqualTo(final T operand) {
        return new AttributeMatch<>(AttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand);
    }

    /**
     * Creates a numeric greater-than match.
     *
     * @param operand the numeric value to compare
     * @param <T> the Attribute value type
     * @return a greater-than match
     */
    public static <T> AttributeMatch<T> greaterThan(final T operand) {
        return new AttributeMatch<>(AttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN, operand);
    }

    /**
     * Creates a numeric greater-than-or-equal match.
     *
     * @param operand the numeric value to compare
     * @param <T> the Attribute value type
     * @return a greater-than-or-equal match
     */
    public static <T> AttributeMatch<T> greaterThanOrEqual(final T operand) {
        return new AttributeMatch<>(
                AttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN_OR_EQUAL,
                operand);
    }

    /**
     * Creates a numeric less-than match.
     *
     * @param operand the numeric value to compare
     * @param <T> the Attribute value type
     * @return a less-than match
     */
    public static <T> AttributeMatch<T> lessThan(final T operand) {
        return new AttributeMatch<>(AttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_LESS_THAN, operand);
    }

    /**
     * Creates a numeric less-than-or-equal match.
     *
     * @param operand the numeric value to compare
     * @param <T> the Attribute value type
     * @return a less-than-or-equal match
     */
    public static <T> AttributeMatch<T> lessThanOrEqual(final T operand) {
        return new AttributeMatch<>(
                AttributeMatchOperator.ATTRIBUTE_MATCH_OPERATOR_LESS_THAN_OR_EQUAL,
                operand);
    }

    AttributeMatchOperator getOperator() {
        return operator;
    }

    T getOperand() {
        return operand;
    }
}
