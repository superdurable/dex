/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

/**
 * Describes the Flow run that {@link Client#cloneForkFlow(String, String, CloneForkFlowOptions)}
 * created and the copied point of the source run.
 *
 * <pre>{@code
 * CloneForkFlowResult result = client.cloneForkFlow("session-1", "session-1-branch",
 *         CloneForkFlowOptions.newBuilder().build());
 * log.info("branch run {} copies source event {}", result.getRunId(), result.getSourceEventId());
 * }</pre>
 *
 * @since 1.45.2
 */
public final class CloneForkFlowResult {
    private final String runId;
    private final String sourceRunId;
    private final int sourceRunAttempt;
    private final long sourceEventId;

    CloneForkFlowResult(
            final String runId,
            final String sourceRunId,
            final int sourceRunAttempt,
            final long sourceEventId) {
        this.runId = runId;
        this.sourceRunId = sourceRunId;
        this.sourceRunAttempt = sourceRunAttempt;
        this.sourceEventId = sourceEventId;
    }

    /**
     * Returns the run ID of the new Flow's run.
     *
     * @return the new run ID
     */
    public String getRunId() {
        return runId;
    }

    /**
     * Returns the run ID of the source run.
     *
     * @return the source run ID
     */
    public String getSourceRunId() {
        return sourceRunId;
    }

    /**
     * Returns the run attempt of the source run that holds the copied point.
     *
     * @return the source run attempt
     */
    public int getSourceRunAttempt() {
        return sourceRunAttempt;
    }

    /**
     * Returns the ID of the last source history event that the copied point includes.
     *
     * @return the source history event ID
     */
    public long getSourceEventId() {
        return sourceEventId;
    }
}
