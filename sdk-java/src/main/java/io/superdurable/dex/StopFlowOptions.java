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
 * Configures how {@link Client#stopFlow} ends a running Flow.
 *
 * <p>The default constructor requests cancellation without a reason. Use the explicit constructor
 * to terminate or fail a Flow and to attach a human-readable reason. Use the three-argument
 * constructor to complete a Flow with an output; see {@link StopType#GRACEFUL_COMPLETE} and
 * {@link StopType#FORCE_COMPLETE}.
 *
 * <pre>{@code
 * client.stopFlow(
 *         "order-123",
 *         new StopFlowOptions(StopType.TERMINATE, "operator request"));
 * client.stopFlow(
 *         "session-42",
 *         new StopFlowOptions(StopType.FORCE_COMPLETE, null, "closed by operator"));
 * }</pre>
 */
public final class StopFlowOptions {
    private final StopType type;
    private final String reason;
    private final Object output;

    /** Creates cancellation options without an explicit reason. */
    public StopFlowOptions() {
        this(StopType.CANCEL, null);
    }

    /**
     * Creates stop options with an explicit mode and reason.
     *
     * @param type the requested terminal behavior
     * @param reason the user-visible reason, or {@code null}
     */
    public StopFlowOptions(final StopType type, final String reason) {
        this(type, reason, null);
    }

    /**
     * Creates stop options with an explicit mode, reason, and Flow result output.
     *
     * <p>The output becomes a Flow result when {@code type} is {@link StopType#GRACEFUL_COMPLETE}
     * or {@link StopType#FORCE_COMPLETE}. The Client encodes it like a Step decision output, so
     * {@link Client#waitForFlow(String)} returns it as a {@link StepCompletion} whose Step type and
     * Step execution ID are empty. {@code null} adds no result. The server rejects an output that
     * exceeds its maximum value size.
     *
     * <p>{@link StopType#TERMINATE} keeps the reason in the Flow closed history event, and
     * {@link StopType#FAIL} uses it as the Flow error message. The other types ignore it.
     *
     * <pre>{@code
     * client.stopFlow(
     *         "session-42",
     *         new StopFlowOptions(StopType.GRACEFUL_COMPLETE, null, "user ended the session"));
     * }</pre>
     *
     * @param type the requested terminal behavior
     * @param reason the user-visible reason, or {@code null}
     * @param output the Flow result output, or {@code null} for no output; {@link Client#stopFlow}
     *     throws {@link IllegalArgumentException} when it is non-null and {@code type} does not
     *     complete the Flow
     * @since 1.45.3
     */
    public StopFlowOptions(final StopType type, final String reason, final Object output) {
        this.type = type;
        this.reason = reason;
        this.output = output;
    }

    StopType getType() {
        return type;
    }

    String getReason() {
        return reason;
    }

    Object getOutput() {
        return output;
    }
}
