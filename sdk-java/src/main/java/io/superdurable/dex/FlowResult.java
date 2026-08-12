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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * Describes the observed state and output of a Flow used as a SubFlow or waited on by a client.
 *
 * <p>A result returned by {@link Client#waitForFlow(String)} is always terminal. A result obtained
 * from {@link SubFlow#getConditionResults(Context)} may have {@link FlowStatus#RUNNING} when a
 * surrounding {@link Wait#anyOf(Condition...)} completed first. That running status is a durable
 * snapshot, not a live backend query. Its Flow ID remains available so the application can later
 * inspect or stop the SubFlow.
 */
public final class FlowResult {
    private final String flowId;
    private final String runId;
    private final FlowStatus status;
    private final FlowErrorType errorType;
    private final String errorMessage;
    private final List<StepCompletion> completions;

    FlowResult(
            final String flowId,
            final String runId,
            final FlowStatus status,
            final FlowErrorType errorType,
            final String errorMessage,
            final List<StepCompletion> completions) {
        if (flowId == null || flowId.isEmpty()) {
            throw new IllegalArgumentException("Flow result requires a Flow ID");
        }
        if (status == null) {
            throw new IllegalArgumentException("Flow result requires a status");
        }
        this.flowId = flowId;
        this.runId = runId;
        this.status = status;
        this.errorType = errorType;
        this.errorMessage = errorMessage;
        this.completions = Collections.unmodifiableList(
                new ArrayList<StepCompletion>(completions));
    }

    /**
     * Returns the stable Flow ID used to inspect, signal, or stop this Flow.
     *
     * @return the nonempty Flow ID
     */
    public String getFlowId() {
        return flowId;
    }

    /**
     * Returns the terminal run ID.
     *
     * @return the run ID, or {@code null} for a running SubFlow snapshot
     */
    public String getRunId() {
        return runId;
    }

    /**
     * Returns the observed Flow status.
     *
     * <p>For a SubFlow result, {@link FlowStatus#RUNNING} describes the durable snapshot created
     * when the parent Wait completed. It does not guarantee that the SubFlow is still running when
     * this method is called.
     *
     * @return the non-null observed status
     */
    public FlowStatus getStatus() {
        return status;
    }

    /**
     * Returns whether this result represents a terminal Flow execution.
     *
     * @return {@code false} for running or continued-as-new states; otherwise {@code true}
     */
    public boolean isTerminal() {
        return status != FlowStatus.RUNNING && status != FlowStatus.CONTINUED_AS_NEW;
    }

    /**
     * Returns the Dex Flow failure category.
     *
     * @return the category, or {@code null} when the Flow did not expose a Dex application error
     */
    public FlowErrorType getErrorType() {
        return errorType;
    }

    /**
     * Returns the server-provided failure detail.
     *
     * @return the failure detail, or {@code null} when no detail is available
     */
    public String getErrorMessage() {
        return errorMessage;
    }

    /**
     * Returns every output-bearing Step completion in server collection order.
     *
     * <p>The returned list is immutable. Parallel completion order is not a business ordering
     * contract. A running SubFlow snapshot always returns an empty list.
     *
     * @return an immutable, non-null completion list
     */
    public List<StepCompletion> getCompletions() {
        return completions;
    }

    /**
     * Decodes the output when exactly one Step completion exists.
     *
     * @param outputType the concrete Java output class
     * @param <T> the expected output type
     * @return the only decoded Step output
     * @throws IllegalStateException if the result is running or has other than one output
     * @throws io.superdurable.dex.exceptions.ValueMappingException if decoding fails
     */
    public <T> T getSingleOutput(final Class<T> outputType) {
        if (!isTerminal()) {
            throw new IllegalStateException("Flow result is not terminal");
        }
        if (completions.size() != 1) {
            throw new IllegalStateException(
                    "Expected exactly one Step output, found " + completions.size());
        }
        return completions.get(0).getOutput(outputType);
    }
}
