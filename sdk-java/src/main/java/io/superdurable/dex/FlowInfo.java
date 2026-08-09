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

import java.time.Instant;

/**
 * Describes the identity and current status of one Flow execution.
 *
 * <p>Obtain this immutable snapshot from {@link Client#describeFlow}. A later request may observe a
 * different status while the Flow continues to run.
 */
public final class FlowInfo {
    private final String flowId;
    private final String runId;
    private final String flowType;
    private final FlowStatus status;
    private final Instant startedAt;

    FlowInfo(
            final String flowId,
            final String runId,
            final String flowType,
            final FlowStatus status,
            final Instant startedAt) {
        this.flowId = flowId;
        this.runId = runId;
        this.flowType = flowType;
        this.status = status;
        this.startedAt = startedAt;
    }

    /**
     * Returns the application-assigned Flow ID.
     *
     * @return the Flow ID
     */
    public String getFlowId() {
        return flowId;
    }

    /**
     * Returns the server-assigned run ID.
     *
     * @return the run ID
     */
    public String getRunId() {
        return runId;
    }

    /**
     * Returns the registered durable Flow type.
     *
     * @return the Flow type
     */
    public String getFlowType() {
        return flowType;
    }

    /**
     * Returns the status captured by this snapshot.
     *
     * @return the Flow status
     */
    public FlowStatus getStatus() {
        return status;
    }

    /**
     * Returns when this Flow execution started.
     *
     * @return the start timestamp
     */
    public Instant getStartedAt() {
        return startedAt;
    }
}
