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

public final class StepExecutionId {
    private final String stepType;
    private final int executionNumber;

    public StepExecutionId(final String stepType) {
        this(stepType, 1);
    }

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
