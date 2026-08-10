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
 * Selects a reset point and replay behavior for {@link Client#resetFlow}.
 *
 * <p>Create the builder with one {@link ResetType}, then set the selector that corresponds to that
 * type. Reset creates a new run of the Flow. Channel messages and locking RPCs are reapplied by
 * default unless explicitly skipped.
 *
 * <pre>{@code
 * ResetFlowOptions options = ResetFlowOptions.newBuilder(ResetType.STEP_TYPE)
 *         .stepType("ChargeOrder")
 *         .reason("retry after operator review")
 *         .build();
 * String newRunId = client.resetFlow("order-123", options);
 * }</pre>
 */
public final class ResetFlowOptions {
    private final ResetType type;
    private final Long historyEventId;
    private final Instant historyEventTime;
    private final String stepType;
    private final String stepExecutionId;
    private final String reason;
    private final boolean skipChannelMessagesReapply;
    private final boolean skipLockingRpcReapply;

    private ResetFlowOptions(final Builder builder) {
        this.type = builder.type;
        this.historyEventId = builder.historyEventId;
        this.historyEventTime = builder.historyEventTime;
        this.stepType = builder.stepType;
        this.stepExecutionId = builder.stepExecutionId;
        this.reason = builder.reason;
        this.skipChannelMessagesReapply = builder.skipChannelMessagesReapply;
        this.skipLockingRpcReapply = builder.skipLockingRpcReapply;
    }

    /**
     * Creates a builder for a reset strategy.
     *
     * @param type the reset-point strategy
     * @return a new mutable builder
     */
    public static Builder newBuilder(final ResetType type) {
        return new Builder(type);
    }

    ResetType getType() {
        return type;
    }

    Long getHistoryEventId() {
        return historyEventId;
    }

    Instant getHistoryEventTime() {
        return historyEventTime;
    }

    String getStepType() {
        return stepType;
    }

    String getStepExecutionId() {
        return stepExecutionId;
    }

    String getReason() {
        return reason;
    }

    boolean isSkipChannelMessagesReapply() {
        return skipChannelMessagesReapply;
    }

    boolean isSkipLockingRpcReapply() {
        return skipLockingRpcReapply;
    }

    /** Builds immutable {@link ResetFlowOptions} values. */
    public static final class Builder {
        private final ResetType type;
        private Long historyEventId;
        private Instant historyEventTime;
        private String stepType;
        private String stepExecutionId;
        private String reason;
        private boolean skipChannelMessagesReapply;
        private boolean skipLockingRpcReapply;

        private Builder(final ResetType type) {
            this.type = type;
        }

        /**
         * Selects a history event by numeric ID.
         *
         * @param value the history event ID
         * @return this builder
         */
        public Builder historyEventId(final long value) {
            historyEventId = value;
            return this;
        }

        /**
         * Selects the latest eligible history event at or before a timestamp.
         *
         * @param value the history timestamp
         * @return this builder
         */
        public Builder historyEventTime(final Instant value) {
            historyEventTime = value;
            return this;
        }

        /**
         * Selects a reset point by Step type.
         *
         * @param value the Step type
         * @return this builder
         */
        public Builder stepType(final String value) {
            stepType = value;
            return this;
        }

        /**
         * Selects a reset point by server Step execution ID.
         *
         * @param value the Step execution ID
         * @return this builder
         */
        public Builder stepExecutionId(final String value) {
            stepExecutionId = value;
            return this;
        }

        /**
         * Records a human-readable reset reason.
         *
         * @param value the reset reason, or {@code null}
         * @return this builder
         */
        public Builder reason(final String value) {
            reason = value;
            return this;
        }

        /**
         * Controls whether historical Channel messages are omitted from replay.
         *
         * @param value {@code true} to skip reapplying Channel messages
         * @return this builder
         */
        public Builder skipChannelMessagesReapply(final boolean value) {
            skipChannelMessagesReapply = value;
            return this;
        }

        /**
         * Controls whether historical locking RPCs are omitted from replay.
         *
         * @param value {@code true} to skip reapplying locking RPCs
         * @return this builder
         */
        public Builder skipLockingRpcReapply(final boolean value) {
            skipLockingRpcReapply = value;
            return this;
        }

        /**
         * Builds immutable reset options from the current values.
         *
         * @return the configured reset options
         */
        public ResetFlowOptions build() {
            return new ResetFlowOptions(this);
        }
    }
}
