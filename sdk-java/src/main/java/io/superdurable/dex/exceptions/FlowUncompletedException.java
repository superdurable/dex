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

package io.superdurable.dex.exceptions;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowErrorType;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StepCompletion;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * Thrown by {@link Client#waitForFlow(String)} when the Flow being waited for reaches a terminal
 * status other than {@link FlowStatus#COMPLETED}.
 *
 * <p>All {@code waitForFlow} overloads throw this exception instead of returning normally for that
 * status. The exception preserves the run identity, terminal status, Flow error category, message,
 * and any completed Step outputs returned by Dex. Each {@link StepCompletion} retains the Step
 * identity and decodes its own output.
 */
public final class FlowUncompletedException extends RuntimeException {
    private final String runId;
    private final FlowStatus status;
    private final FlowErrorType errorType;
    private final List<StepCompletion> completions;

    /**
     * Creates an exception for a Flow that reached a terminal status other than
     * {@link FlowStatus#COMPLETED}.
     *
     * @param runId the run ID that reached a terminal status
     * @param status the terminal Flow status, which is not {@link FlowStatus#COMPLETED}
     * @param errorType the Flow error category, or {@code null} when unavailable
     * @param message the server-provided completion detail
     * @param completions completed Step outputs returned by Dex
     */
    public FlowUncompletedException(
            final String runId,
            final FlowStatus status,
            final FlowErrorType errorType,
            final String message,
            final List<StepCompletion> completions) {
        super(message);
        this.runId = runId;
        this.status = status;
        this.errorType = errorType;
        this.completions = Collections.unmodifiableList(
                new ArrayList<StepCompletion>(completions));
    }

    /**
     * Returns the run ID observed by {@code waitForFlow}.
     *
     * @return the run ID
     */
    public String getRunId() {
        return runId;
    }

    /**
     * Returns the terminal status that prevented {@code waitForFlow} from returning normally.
     *
     * @return the Flow status
     */
    public FlowStatus getStatus() {
        return status;
    }

    /**
     * Returns the Flow failure category reported by Dex.
     *
     * @return the Flow error type, or {@code null} when Dex did not supply one
     */
    public FlowErrorType getErrorType() {
        return errorType;
    }

    /**
     * Returns all output-bearing Step completions carried by this exception.
     *
     * @return an immutable list in server collection order
     */
    public List<StepCompletion> getCompletions() {
        return completions;
    }
}
