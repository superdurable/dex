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

/**
 * Represents one Flow execution returned by a search query.
 *
 * <p>The entry is an immutable snapshot. Indexed Attribute values are decoded to their natural
 * Java representations and exposed through an unmodifiable map keyed by index name.
 */
public final class SearchFlowEntry {
    private final String flowId;
    private final String runId;
    private final String flowType;
    private final FlowStatus status;
    private final Instant startedAt;
    private final Instant closedAt;
    private final Map<String, Object> indexedAttributes;

    SearchFlowEntry(
            final String flowId,
            final String runId,
            final String flowType,
            final FlowStatus status,
            final Instant startedAt,
            final Instant closedAt,
            final Map<String, Object> indexedAttributes) {
        this.flowId = flowId;
        this.runId = runId;
        this.flowType = flowType;
        this.status = status;
        this.startedAt = startedAt;
        this.closedAt = closedAt;
        this.indexedAttributes = Collections.unmodifiableMap(
                new LinkedHashMap<String, Object>(indexedAttributes));
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
     * Returns the registered Flow type.
     *
     * @return the Flow type
     */
    public String getFlowType() {
        return flowType;
    }

    /**
     * Returns the status captured by this search result.
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

    /**
     * Returns when this Flow execution closed.
     *
     * @return the close timestamp, or {@code null} while the Flow is running
     */
    public Instant getClosedAt() {
        return closedAt;
    }

    /**
     * Returns decoded Indexed Attribute values for this execution.
     *
     * @return an unmodifiable map of index names to values
     */
    public Map<String, Object> getIndexedAttributes() {
        return indexedAttributes;
    }
}
