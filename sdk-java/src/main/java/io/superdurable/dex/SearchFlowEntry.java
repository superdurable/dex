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
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;

public final class SearchFlowEntry {
    private final String flowId;
    private final String runId;
    private final String flowType;
    private final FlowStatus status;
    private final Instant startedAt;
    private final Instant closedAt;
    private final Map<String, Object> searchAttributes;

    SearchFlowEntry(
            final String flowId,
            final String runId,
            final String flowType,
            final FlowStatus status,
            final Instant startedAt,
            final Instant closedAt,
            final Map<String, Object> searchAttributes) {
        this.flowId = flowId;
        this.runId = runId;
        this.flowType = flowType;
        this.status = status;
        this.startedAt = startedAt;
        this.closedAt = closedAt;
        this.searchAttributes = Collections.unmodifiableMap(
                new LinkedHashMap<String, Object>(searchAttributes));
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

    public Instant getClosedAt() {
        return closedAt;
    }

    public Map<String, Object> getSearchAttributes() {
        return searchAttributes;
    }
}
