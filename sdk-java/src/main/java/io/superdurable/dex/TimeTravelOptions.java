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

import java.time.Instant;

/**
 * Selects a historical point and replay behavior for {@link Client#timeTravel}.
 *
 * <p>Create the builder with one {@link TimeTravelType}, then set the selector that corresponds to that
 * type. Time travel creates a new run attempt of the Flow under the same run ID. Writes after the
 * selected point are reapplied by default unless explicitly skipped.
 *
 * <pre>{@code
 * TimeTravelOptions options = TimeTravelOptions.newBuilder(TimeTravelType.STEP_TYPE)
 *         .stepType("ChargeOrder")
 *         .reason("retry after operator review")
 *         .build();
 * String runId = client.timeTravel("order-123", options);
 * }</pre>
 */
public final class TimeTravelOptions {
    private final TimeTravelType type;
    private final Instant historyEventTime;
    private final String stepType;
    private final String stepExecutionId;
    private final TimeTravelStepMethod stepMethod;
    private final String reason;
    private final boolean skipWritesReapply;
    private final long eventId;
    private final SubFlowResetPolicy subFlowResetPolicy;

    private TimeTravelOptions(final Builder builder) {
        this.type = builder.type;
        this.historyEventTime = builder.historyEventTime;
        this.stepType = builder.stepType;
        this.stepExecutionId = builder.stepExecutionId;
        this.stepMethod = builder.stepMethod;
        this.reason = builder.reason;
        this.skipWritesReapply = builder.skipWritesReapply;
        this.eventId = builder.eventId;
        this.subFlowResetPolicy = builder.subFlowResetPolicy;
    }

    /**
     * Creates a builder for a time travel strategy.
     *
     * @param type the historical-point strategy
     * @return a new mutable builder
     */
    public static Builder newBuilder(final TimeTravelType type) {
        return new Builder(type);
    }

    TimeTravelType getType() {
        return type;
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

    TimeTravelStepMethod getStepMethod() {
        return stepMethod;
    }

    String getReason() {
        return reason;
    }

    boolean isSkipWritesReapply() {
        return skipWritesReapply;
    }

    long getEventId() {
        return eventId;
    }

    SubFlowResetPolicy getSubFlowResetPolicy() {
        return subFlowResetPolicy;
    }

    /** Builds immutable {@link TimeTravelOptions} values. */
    public static final class Builder {
        private final TimeTravelType type;
        private Instant historyEventTime;
        private String stepType;
        private String stepExecutionId;
        private TimeTravelStepMethod stepMethod;
        private String reason;
        private boolean skipWritesReapply;
        private long eventId;
        private SubFlowResetPolicy subFlowResetPolicy = SubFlowResetPolicy.DEFAULT;

        private Builder(final TimeTravelType type) {
            this.type = type;
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
         * Selects a time travel point by Step type.
         *
         * @param value the Step type
         * @return this builder
         */
        public Builder stepType(final String value) {
            stepType = value;
            return this;
        }

        /**
         * Selects a time travel point by server Step execution ID.
         *
         * @param value the Step execution ID
         * @return this builder
         */
        public Builder stepExecutionId(final String value) {
            stepExecutionId = value;
            return this;
        }

        /**
         * Selects the WaitFor or Execute boundary for a Step execution.
         *
         * @param value the Step method used as the time travel boundary
         * @return this builder
         */
        public Builder stepMethod(final TimeTravelStepMethod value) {
            stepMethod = value;
            return this;
        }

        /**
         * Records a human-readable time travel reason.
         *
         * @param value the time travel reason, or {@code null}
         * @return this builder
         */
        public Builder reason(final String value) {
            reason = value;
            return this;
        }

        /**
         * Controls whether historical writes are omitted from replay.
         *
         * <p>Writes include RPCs, Channel publications, and Attribute writes after the selected point.
         *
         * @param value {@code true} to skip reapplying writes
         * @return this builder
         */
        public Builder skipWritesReapply(final boolean value) {
            skipWritesReapply = value;
            return this;
        }

        /**
         * Selects a time travel point by history event ID for {@link TimeTravelType#EVENT_ID}.
         *
         * <p>The ID must be positive for that type and unset for the others.
         *
         * <pre>{@code
         * client.timeTravel("order-123", TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID)
         *         .eventId(42)
         *         .build());
         * }</pre>
         *
         * @param value the history event ID
         * @return this builder
         * @since 1.45.2
         */
        public Builder eventId(final long value) {
            eventId = value;
            return this;
        }

        /**
         * Decides what happens to the SubFlow conditions that restored Steps wait on.
         *
         * <p>{@link SubFlowResetPolicy#DEFAULT}, the default, uses
         * {@link SubFlowResetPolicy#ATTACH_ELSE_RESTART}.
         *
         * @param value the nonnull policy
         * @return this builder
         * @throws NullPointerException if value is null
         * @since 1.45.2
         */
        public Builder subFlowResetPolicy(final SubFlowResetPolicy value) {
            subFlowResetPolicy = java.util.Objects.requireNonNull(value, "subFlowResetPolicy");
            return this;
        }

        /**
         * Builds immutable time travel options from the current values.
         *
         * @return the configured time travel options
         */
        public TimeTravelOptions build() {
            return new TimeTravelOptions(this);
        }
    }
}
