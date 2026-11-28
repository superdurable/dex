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

import java.util.Objects;

/**
 * Configures how {@link Client#stopFlow} ends a running Flow.
 *
 * <p>The default constructor requests cancellation without a reason. Use the explicit constructor
 * to terminate or fail a Flow and to attach a human-readable reason. Use the three-argument
 * constructor to complete a Flow with an output; see {@link StopType#GRACEFUL_COMPLETE} and
 * {@link StopType#FORCE_COMPLETE}. Use {@link #withRunId(String)} to stop one exact run instead
 * of the active run.
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
    private final String runId;

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
        this(type, reason, output, "");
    }

    private StopFlowOptions(
            final StopType type,
            final String reason,
            final Object output,
            final String runId) {
        this.type = type;
        this.reason = reason;
        this.output = output;
        this.runId = runId;
    }

    /**
     * Returns a copy of these options that stops one exact run of the Flow ID.
     *
     * <p>Use it to stop exactly the run the caller tracks, such as a child Flow run whose run ID
     * a parent recorded from {@link Client#startFlow}. Without a run ID, {@link Client#stopFlow}
     * stops the active run of the Flow ID, which may be a newer run than the tracked one. Empty,
     * the default, keeps that behavior. The stop type, reason, and output of these options are
     * kept unchanged.
     *
     * <p>A stop with a run ID never affects a newer run of the same Flow ID. When that run has
     * already closed, does not exist, or belongs to another Flow ID, {@link Client#stopFlow}
     * throws {@link io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException}. The caller
     * can treat that exception as "the tracked run already closed". A run ID that is not in its
     * canonical 36-character lowercase hyphenated UUID form makes {@link Client#stopFlow} throw a
     * {@link io.superdurable.dex.exceptions.DexServiceException} whose
     * {@link io.superdurable.dex.exceptions.DexServiceException#getCode() code} is
     * {@code INVALID_ARGUMENT}.
     *
     * <pre>{@code
     * try {
     *     client.stopFlow(
     *             "shipment-42",
     *             new StopFlowOptions(StopType.TERMINATE, "claim expired").withRunId(claimedRunId));
     * } catch (FlowNotActiveOrNotFoundException alreadyClosed) {
     *     // The claimed run had already closed; a newer run is untouched.
     * }
     * }</pre>
     *
     * @param value the run ID to stop, such as one {@link Client#startFlow} returned, or empty
     *     for the active run
     * @return new options with the same stop type, reason, and output, and this run ID
     * @throws NullPointerException if value is null
     * @since 1.45.4
     */
    public StopFlowOptions withRunId(final String value) {
        return new StopFlowOptions(type, reason, output, Objects.requireNonNull(value, "runId"));
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

    String getRunId() {
        return runId;
    }
}
