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

import org.immutables.value.Value;

import java.util.Optional;

@Value.Immutable
public abstract class Context {
    public abstract Long getWorkflowStartTimestampSeconds();

    /**
     * @return the StateExecutionId.
     * Only applicable for state methods (waitUntil or execute)
     */
    public abstract Optional<String> getStateExecutionId();

    public abstract String getWorkflowRunId();

    public abstract String getWorkflowId();

    public abstract String getWorkflowType();

    /**
     * @return the start time of the first attempt of the state method invocation.
     * Only applicable for state methods (waitUntil or execute)
     */
    public abstract Optional<Long> getFirstAttemptTimestampSeconds();

    /**
     * @return attempt starts from 1, and increased by 1 for every retry if retry policy is specified.
     */
    public abstract Optional<Integer> getAttempt();

    /**
     * @return the requestId that is used to start the child workflow from state method.
     * Only applicable for state methods (waitUntil or execute)
     */
    public abstract Optional<String> getChildWorkflowRequestId();
}
