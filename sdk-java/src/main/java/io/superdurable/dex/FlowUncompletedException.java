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

import io.superdurable.gen.StepCompletionOutput;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class FlowUncompletedException extends RuntimeException {
    private final String runId;
    private final FlowStatus status;
    private final FlowErrorType errorType;
    private final List<StepCompletionOutput> results;
    private final ValueMapper values;

    FlowUncompletedException(
            final String runId,
            final FlowStatus status,
            final FlowErrorType errorType,
            final String message,
            final List<StepCompletionOutput> results,
            final ValueMapper values) {
        super(message);
        this.runId = runId;
        this.status = status;
        this.errorType = errorType;
        this.results = Collections.unmodifiableList(
                new ArrayList<StepCompletionOutput>(results));
        this.values = values;
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
        return values.decode(results.get(index).getCompletedStepOutput(), resultType);
    }
}
