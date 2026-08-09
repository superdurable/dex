/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

/**
 * Selects a timer condition within one Step execution.
 *
 * <p>A timer can be selected by the stable condition ID supplied to
 * {@link Timer#byDuration(java.time.Duration, String)} or by its zero-based position among the
 * Step's timer conditions. Prefer a condition ID when definitions may be reordered.
 *
 * <pre>{@code
 * client.skipTimer(
 *         "order-123",
 *         new StepExecutionId("AwaitPayment"),
 *         TimerId.byConditionId("payment-deadline"));
 * }</pre>
 */
public final class TimerId {
    private final String conditionId;
    private final Integer index;

    private TimerId(final String conditionId, final Integer index) {
        this.conditionId = conditionId;
        this.index = index;
    }

    /**
     * Selects a timer by its user-defined condition ID.
     *
     * @param conditionId the nonblank ID assigned when creating the timer
     * @return a timer selector for that condition ID
     * @throws IllegalArgumentException if {@code conditionId} is {@code null} or blank
     */
    public static TimerId byConditionId(final String conditionId) {
        return new TimerId(Attribute.requireName(conditionId), null);
    }

    /**
     * Selects a timer by its condition index.
     *
     * @param index the zero-based timer condition index
     * @return a timer selector for that index
     */
    public static TimerId byConditionIndex(final int index) {
        return new TimerId(null, index);
    }

    String getConditionId() {
        return conditionId;
    }

    Integer getIndex() {
        return index;
    }
}
