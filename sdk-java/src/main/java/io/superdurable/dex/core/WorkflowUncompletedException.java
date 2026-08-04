/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
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
