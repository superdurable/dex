/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.core;

import io.superdurable.dex.gen.models.StateCompletionOutput;
import io.superdurable.dex.gen.models.WorkflowErrorType;
import io.superdurable.dex.gen.models.WorkflowStatus;

import java.util.List;

public class WorkflowUncompletedException extends RuntimeException {
    private final String runId;
    private final WorkflowStatus closedStatus;
    private final WorkflowErrorType errorType;
    private final String errorMessage;
    private final List<StateCompletionOutput> stateResults;
    private final ObjectEncoder encoder;

    public WorkflowUncompletedException(
            final String runId, final WorkflowStatus closedStatus, final WorkflowErrorType errorType, final String errorMessage,
            final List<StateCompletionOutput> stateResults, final ObjectEncoder encoder) {
        this.runId = runId;
        this.closedStatus = closedStatus;
        this.errorType = errorType;
        this.errorMessage = errorMessage;
        this.stateResults = stateResults;
        this.encoder = encoder;
    }

    public String getRunId() {
        return runId;
    }

    public WorkflowStatus getClosedStatus() {
        return closedStatus;
    }

    // Today, this only applies to FAILED as closedStatus to differentiate different failed types
    public WorkflowErrorType getErrorSubType() {
        return errorType;
    }

    public String getErrorMessage() {
        return errorMessage;
    }

    public int getStateResultsSize() {
        if (stateResults == null) {
            return 0;
        }
        return stateResults.size();
    }

    public <T> T getStateResult(final int index, Class<T> type) {
        final StateCompletionOutput output = stateResults.get(index);
        return encoder.decode(output.getCompletedStateOutput(), type);
    }

}
