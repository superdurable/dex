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
 * Selects one execution of a Step within a Flow.
 *
 * <p>Use this identifier with operations such as {@link Client#skipTimer} and
 * {@link Client#waitForStepCompletion}. Execution numbers begin at one for each Step type.
 *
 * <pre>{@code
 * StepExecutionId execution = new StepExecutionId("ChargeCard", 2);
 * client.waitForStepCompletion("order-123", execution, Duration.ofSeconds(30));
 * }</pre>
 */
public final class StepExecutionId {
    private final String stepType;
    private final int executionNumber;

    /**
     * Selects the first execution of a Step type.
     *
     * @param stepType the durable Step type; must not be blank
     * @throws IllegalArgumentException if {@code stepType} is {@code null} or blank
     */
    public StepExecutionId(final String stepType) {
        this(stepType, 1);
    }

    /**
     * Selects an explicit execution number of a Step type.
     *
     * @param stepType the durable Step type; must not be blank
     * @param executionNumber the one-based execution number
     * @throws IllegalArgumentException if {@code stepType} is {@code null} or blank
     */
    public StepExecutionId(final String stepType, final int executionNumber) {
        this.stepType = Attribute.requireName(stepType);
        this.executionNumber = executionNumber;
    }

    String getStepType() {
        return stepType;
    }

    int getExecutionNumber() {
        return executionNumber;
    }
}
