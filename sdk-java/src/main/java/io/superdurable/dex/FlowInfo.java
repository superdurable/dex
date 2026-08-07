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

    public String getFlowId() {
        return flowId;
    }

    public String getRunId() {
        return runId;
    }

    public String getFlowType() {
        return flowType;
    }

    public FlowStatus getStatus() {
        return status;
    }

    public Instant getStartedAt() {
        return startedAt;
    }
}
