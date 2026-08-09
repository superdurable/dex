/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.exceptions;

import io.superdurable.dex.FlowErrorType;
import io.superdurable.dex.FlowStatus;
import io.superdurable.gen.StepCompletionOutput;
import io.superdurable.gen.Value;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.function.BiFunction;

public final class FlowUncompletedException extends RuntimeException {
    private final String runId;
    private final FlowStatus status;
    private final FlowErrorType errorType;
    private final List<StepCompletionOutput> results;
    private final BiFunction<Value, Class<?>, Object> decoder;

    public FlowUncompletedException(
            final String runId,
            final FlowStatus status,
            final FlowErrorType errorType,
            final String message,
            final List<StepCompletionOutput> results,
            final BiFunction<Value, Class<?>, Object> decoder) {
        super(message);
        this.runId = runId;
        this.status = status;
        this.errorType = errorType;
        this.results = Collections.unmodifiableList(
                new ArrayList<StepCompletionOutput>(results));
        this.decoder = decoder;
    }

    public String getRunId() {
        return runId;
    }

    public FlowStatus getStatus() {
        return status;
    }

    public FlowErrorType getErrorType() {
        return errorType;
    }

    public int getResultCount() {
        return results.size();
    }

    public <T> T getResult(final int index, final Class<T> resultType) {
        return resultType.cast(decoder.apply(
                results.get(index).getCompletedStepOutput(),
                resultType));
    }
}
