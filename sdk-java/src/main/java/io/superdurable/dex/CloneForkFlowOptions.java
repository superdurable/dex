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

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * Configures {@link Client#cloneForkFlow(String, String, CloneForkFlowOptions)}: the copied point
 * of the source run and the start of the new Flow.
 *
 * <p>Default options copy the latest committed state of the source Flow's latest run, keep the
 * source run's Flow configuration, task queue, and Flow timeout duration, and send a new
 * SDK-generated request ID.
 *
 * <p>The point selectors work as in {@link TimeTravelOptions}. {@link Builder#pointType} selects
 * which one Dex reads:
 *
 * <ul>
 *   <li>{@link TimeTravelType#CURRENT}, the default: the latest committed state of the current run
 *       attempt;</li>
 *   <li>{@link TimeTravelType#BEGINNING}: the state right after the source run started;</li>
 *   <li>{@link TimeTravelType#HISTORY_EVENT_TIME}: {@link Builder#historyEventTime};</li>
 *   <li>{@link TimeTravelType#STEP_TYPE}: {@link Builder#stepType};</li>
 *   <li>{@link TimeTravelType#STEP_EXECUTION_ID}: {@link Builder#stepExecutionId} and
 *       {@link Builder#stepMethod};</li>
 *   <li>{@link TimeTravelType#EVENT_ID}: {@link Builder#eventId}.</li>
 * </ul>
 *
 * <p>Every point other than the current one must be inside the source run's history window. The
 * state at the point must be running.
 *
 * <pre>{@code
 * CloneForkFlowOptions options = CloneForkFlowOptions.newBuilder()
 *         .pointType(TimeTravelType.EVENT_ID)
 *         .eventId(42)
 *         .requestId("branch-" + sessionId)
 *         .reason("try another answer")
 *         .build();
 * CloneForkFlowResult result = client.cloneForkFlow(sessionId, sessionId + "-branch", options);
 * }</pre>
 *
 * @since 1.45.2
 */
public final class CloneForkFlowOptions {
    private final String sourceRunId;
    private final int sourceRunAttempt;
    private final TimeTravelType pointType;
    private final Instant historyEventTime;
    private final String stepType;
    private final String stepExecutionId;
    private final TimeTravelStepMethod stepMethod;
    private final long eventId;
    private final IdReusePolicy idReusePolicy;
    private final boolean ignoreAlreadyStarted;
    private final String requestId;
    private final FlowConfig configOverride;
    private final String taskQueue;
    private final List<StartFlowOptions.AttributeInitialization> attributes;
    private final Duration timeout;
    private final String reason;
    private final SubFlowResetPolicy subFlowResetPolicy;

    private CloneForkFlowOptions(final Builder builder) {
        this.sourceRunId = builder.sourceRunId;
        this.sourceRunAttempt = builder.sourceRunAttempt;
        this.pointType = builder.pointType;
        this.historyEventTime = builder.historyEventTime;
        this.stepType = builder.stepType;
        this.stepExecutionId = builder.stepExecutionId;
        this.stepMethod = builder.stepMethod;
        this.eventId = builder.eventId;
        this.idReusePolicy = builder.idReusePolicy;
        this.ignoreAlreadyStarted = builder.ignoreAlreadyStarted;
        this.requestId = builder.requestId;
        this.configOverride = builder.configOverride;
        this.taskQueue = builder.taskQueue;
        this.attributes = Collections.unmodifiableList(
                new ArrayList<StartFlowOptions.AttributeInitialization>(builder.attributes));
        this.timeout = builder.timeout;
        this.reason = builder.reason;
        this.subFlowResetPolicy = builder.subFlowResetPolicy;
    }

    /**
     * Creates a builder whose defaults copy the latest committed state of the latest source run.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    String getSourceRunId() {
        return sourceRunId;
    }

    int getSourceRunAttempt() {
        return sourceRunAttempt;
    }

    TimeTravelType getPointType() {
        return pointType;
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

    long getEventId() {
        return eventId;
    }

    IdReusePolicy getIdReusePolicy() {
        return idReusePolicy;
    }

    boolean isIgnoreAlreadyStarted() {
        return ignoreAlreadyStarted;
    }

    String getRequestId() {
        return requestId;
    }

    FlowConfig getConfigOverride() {
        return configOverride;
    }

    String getTaskQueue() {
        return taskQueue;
    }

    List<StartFlowOptions.AttributeInitialization> getAttributes() {
        return attributes;
    }

    Duration getTimeout() {
        return timeout;
    }

    String getReason() {
        return reason;
    }

    SubFlowResetPolicy getSubFlowResetPolicy() {
        return subFlowResetPolicy;
    }

    /** Builds immutable {@link CloneForkFlowOptions} values. */
    public static final class Builder {
        private String sourceRunId = "";
        private int sourceRunAttempt;
        private TimeTravelType pointType = TimeTravelType.CURRENT;
        private Instant historyEventTime;
        private String stepType;
        private String stepExecutionId;
        private TimeTravelStepMethod stepMethod;
        private long eventId;
        private IdReusePolicy idReusePolicy = IdReusePolicy.DEFAULT;
        private boolean ignoreAlreadyStarted;
        private String requestId;
        private FlowConfig configOverride;
        private String taskQueue = "";
        private final List<StartFlowOptions.AttributeInitialization> attributes =
                new ArrayList<StartFlowOptions.AttributeInitialization>();
        private Duration timeout;
        private String reason = "";
        private SubFlowResetPolicy subFlowResetPolicy = SubFlowResetPolicy.DEFAULT;

        private Builder() {
        }

        /**
         * Selects the source run of the source Flow ID.
         *
         * <p>Empty, the default, selects the latest run. The source run may be closed.
         *
         * @param value the source run ID, or empty for the latest run
         * @return this builder
         * @throws NullPointerException if value is null
         */
        public Builder sourceRunId(final String value) {
            sourceRunId = Objects.requireNonNull(value, "sourceRunId");
            return this;
        }

        /**
         * Selects the run attempt of the source run whose history holds the point.
         *
         * <p>Zero, the default, selects the current run attempt. {@link TimeTravelType#CURRENT}
         * accepts only zero.
         *
         * @param value the source run attempt, or zero for the current one
         * @return this builder
         */
        public Builder sourceRunAttempt(final int value) {
            sourceRunAttempt = value;
            return this;
        }

        /**
         * Selects the copied point; the default is {@link TimeTravelType#CURRENT}.
         *
         * @param value the nonnull point type
         * @return this builder
         * @throws NullPointerException if value is null
         */
        public Builder pointType(final TimeTravelType value) {
            pointType = Objects.requireNonNull(value, "pointType");
            return this;
        }

        /**
         * Supplies the time for {@link TimeTravelType#HISTORY_EVENT_TIME}.
         *
         * @param value the history timestamp
         * @return this builder
         */
        public Builder historyEventTime(final Instant value) {
            historyEventTime = value;
            return this;
        }

        /**
         * Supplies the Step type for {@link TimeTravelType#STEP_TYPE}.
         *
         * @param value the Step type
         * @return this builder
         */
        public Builder stepType(final String value) {
            stepType = value;
            return this;
        }

        /**
         * Supplies the Step execution ID for {@link TimeTravelType#STEP_EXECUTION_ID}.
         *
         * @param value the Step execution ID
         * @return this builder
         */
        public Builder stepExecutionId(final String value) {
            stepExecutionId = value;
            return this;
        }

        /**
         * Selects WaitFor or Execute for {@link TimeTravelType#STEP_EXECUTION_ID}.
         *
         * @param value the Step method used as the point boundary
         * @return this builder
         */
        public Builder stepMethod(final TimeTravelStepMethod value) {
            stepMethod = value;
            return this;
        }

        /**
         * Supplies the history event ID for {@link TimeTravelType#EVENT_ID}; it must be positive.
         *
         * @param value the history event ID
         * @return this builder
         */
        public Builder eventId(final long value) {
            eventId = value;
            return this;
        }

        /**
         * Decides whether cloneForkFlow may start a run for a Flow ID that already has a run, as
         * {@link StartFlowOptions.Builder#idReusePolicy(IdReusePolicy)} does for startFlow.
         *
         * @param value the nonnull reuse policy
         * @return this builder
         * @throws NullPointerException if value is null
         */
        public Builder idReusePolicy(final IdReusePolicy value) {
            idReusePolicy = Objects.requireNonNull(value, "idReusePolicy");
            return this;
        }

        /**
         * Returns the existing run instead of throwing when Dex does not start a new run.
         *
         * <p>The existing run is returned only when {@link #requestId(String)} matches the request
         * that created it, as for startFlow.
         *
         * @param value {@code true} to ignore the already-started error
         * @return this builder
         */
        public Builder ignoreAlreadyStarted(final boolean value) {
            ignoreAlreadyStarted = value;
            return this;
        }

        /**
         * Identifies one logical clone fork.
         *
         * <p>{@code null}, the default, sends a new SDK-generated UUID. Set a stable ID, together
         * with {@link #ignoreAlreadyStarted(boolean)}, to retry a call whose outcome is unknown: a
         * retry with the request ID of the new Flow's latest run copies nothing again.
         *
         * @param value the stable request ID, or {@code null} to generate a UUID
         * @return this builder
         * @throws IllegalArgumentException if value is empty
         */
        public Builder requestId(final String value) {
            if (value != null && value.isEmpty()) {
                throw new IllegalArgumentException("cloneForkFlow request ID must not be empty");
            }
            requestId = value;
            return this;
        }

        /**
         * Changes the Flow configuration copied from the source run.
         *
         * <p>Unset fields keep the source run's values. {@code null}, the default, keeps the whole
         * configuration.
         *
         * @param value the configuration override, or {@code null}
         * @return this builder
         */
        public Builder configOverride(final FlowConfig value) {
            configOverride = value;
            return this;
        }

        /**
         * Names the task queue of the new Flow.
         *
         * <p>Empty, the default, keeps the source run's task queue; {@code dex.default} selects the
         * default task queue. Set a task queue when Workers pick their Flows by task queue, so
         * that the source run's Workers do not run the new Flow's Steps. Names follow
         * {@link StartFlowOptions.Builder#taskQueue(String)}.
         *
         * @param value the task queue name, {@code dex.default}, or empty
         * @return this builder
         * @throws IllegalArgumentException if value is null, reserved, or malformed
         */
        public Builder taskQueue(final String value) {
            taskQueue = TaskQueueNames.validateFlowTaskQueue(value);
            return this;
        }

        /**
         * Adds an Attribute write that Dex applies after the copy, in the change that creates the
         * new Flow.
         *
         * @param attribute the nonnull Attribute
         * @param value the typed value
         * @param <T> the Attribute value type
         * @return this builder
         * @throws NullPointerException if {@code attribute} is {@code null}
         */
        public <T> Builder addAttribute(final Attribute<T> attribute, final T value) {
            attributes.add(new StartFlowOptions.AttributeInitialization(
                    Objects.requireNonNull(attribute, "attribute"), null, value));
            return this;
        }

        /**
         * Adds an Attribute-map write that Dex applies after the copy.
         *
         * @param attributeMap the nonnull Attribute map
         * @param instance the map instance. Slash is prohibited because it is a reserved character
         * @param value the typed value
         * @param <T> the Attribute value type
         * @return this builder
         * @throws NullPointerException if {@code attributeMap} is {@code null}
         * @throws IllegalArgumentException if {@code instance} is {@code null}, blank, or contains {@code /}
         */
        public <T> Builder addAttribute(
                final AttributeMap<T> attributeMap,
                final String instance,
                final T value) {
            attributes.add(new StartFlowOptions.AttributeInitialization(
                    Objects.requireNonNull(attributeMap, "attributeMap"),
                    Attribute.requireMapInstance(instance),
                    value));
            return this;
        }

        /**
         * Sets the new Flow's Flow timeout, counted from the call, in whole seconds.
         *
         * <p>{@code null} or zero, the default, keeps the source run's Flow timeout duration, also
         * counted from the call; a source run without a Flow timeout gives a new Flow without
         * one. The new Flow keeps the source run's timeout policy and handler options.
         *
         * @param value the nonnegative whole-second timeout, or {@code null}
         * @return this builder
         * @throws IllegalArgumentException if value is negative or not whole seconds
         */
        public Builder timeout(final Duration value) {
            if (value != null && (value.isNegative() || value.getNano() != 0
                    || value.getSeconds() > Integer.MAX_VALUE)) {
                throw new IllegalArgumentException(
                        "cloneForkFlow timeout must be nonnegative whole seconds within int32");
            }
            timeout = value;
            return this;
        }

        /**
         * Explains the clone fork; Dex records it with the new Flow's start.
         *
         * @param value the reason, or {@code null} for none
         * @return this builder
         */
        public Builder reason(final String value) {
            reason = value == null ? "" : value;
            return this;
        }

        /**
         * Decides what happens to the SubFlow conditions that copied Steps wait on.
         *
         * <p>{@link SubFlowResetPolicy#DEFAULT}, the default, uses
         * {@link SubFlowResetPolicy#ATTACH_ELSE_RESTART}. A child that ended attaches; a child
         * that still runs takes the policy's second action.
         *
         * @param value the nonnull policy
         * @return this builder
         * @throws NullPointerException if value is null
         */
        public Builder subFlowResetPolicy(final SubFlowResetPolicy value) {
            subFlowResetPolicy = Objects.requireNonNull(value, "subFlowResetPolicy");
            return this;
        }

        /**
         * Builds immutable clone fork options from the current values.
         *
         * @return the configured options
         */
        public CloneForkFlowOptions build() {
            return new CloneForkFlowOptions(this);
        }
    }
}
