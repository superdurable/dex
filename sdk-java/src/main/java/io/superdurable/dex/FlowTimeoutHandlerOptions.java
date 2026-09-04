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

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * Configures one Flow timeout handler's Execute semantics and state snapshot.
 *
 * <p>Ordinary Attributes and Channel size metadata load automatically. AttributeMap values and
 * pending Channel messages require explicit selections. One logical handler execution may retry.
 *
 * <pre>{@code
 * FlowTimeoutHandlerOptions options = FlowTimeoutHandlerOptions.newBuilder()
 *         .methodTimeout(Duration.ofSeconds(30))
 *         .retry(RetryPolicy.newBuilder().maximumAttempts(3).build())
 *         .addLoadChannel(commands)
 *         .onFailureProceedTo(TimeoutRecoveryStep.class)
 *         .build();
 * }</pre>
 */
public final class FlowTimeoutHandlerOptions {
    private final Duration methodTimeout;
    private final Duration heartbeatTimeout;
    private final RetryPolicy retry;
    private final FailureTarget failureTarget;
    private final StepDurability durability;
    private final List<AttributeLock> locks;
    private final HandlerStateLoads stateLoads;

    private FlowTimeoutHandlerOptions(final Builder builder) {
        methodTimeout = builder.methodTimeout;
        heartbeatTimeout = builder.heartbeatTimeout;
        retry = builder.retry;
        failureTarget = builder.failureTarget;
        durability = builder.durability;
        locks = Collections.unmodifiableList(new ArrayList<AttributeLock>(builder.locks));
        stateLoads = builder.stateLoads.build();
    }

    /**
     * Creates a builder initialized with server defaults.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    Duration getMethodTimeout() {
        return methodTimeout;
    }

    Duration getHeartbeatTimeout() {
        return heartbeatTimeout;
    }

    RetryPolicy getRetry() {
        return retry;
    }

    FailureTarget getFailureTarget() {
        return failureTarget;
    }

    StepDurability getDurability() {
        return durability;
    }

    List<AttributeLock> getLocks() {
        return locks;
    }

    HandlerStateLoads getStateLoads() {
        return stateLoads;
    }

    /** Builds immutable {@link FlowTimeoutHandlerOptions} values. */
    public static final class Builder {
        private Duration methodTimeout;
        private Duration heartbeatTimeout;
        private RetryPolicy retry;
        private FailureTarget failureTarget;
        private StepDurability durability = StepDurability.DEFAULT;
        private final List<AttributeLock> locks = new ArrayList<AttributeLock>();
        private final HandlerStateLoads.Builder stateLoads = new HandlerStateLoads.Builder();

        private Builder() {
        }

        /**
         * Sets the maximum duration of one timeout-handler attempt.
         *
         * @param value the timeout, or {@code null} for the server default
         * @return this builder
         */
        public Builder methodTimeout(final Duration value) {
            methodTimeout = value;
            return this;
        }

        /**
         * Sets the heartbeat timeout for regular timeout-handler attempts.
         *
         * @param value a positive whole-second duration, or {@code null} for the server default
         * @return this builder
         */
        public Builder heartbeatTimeout(final Duration value) {
            heartbeatTimeout = value;
            return this;
        }

        /**
         * Sets retry behavior for timeout-handler failures.
         *
         * @param value the retry policy, or {@code null} for server defaults
         * @return this builder
         */
        public Builder retry(final RetryPolicy value) {
            retry = value;
            return this;
        }

        /**
         * Routes exhausted timeout-handler failures to a registered no-input Step.
         *
         * <p>The Step receives {@code null} and reads the final error through
         * {@link Context#getRecoveryError}.
         *
         * @param stepClass the recovery Step class with {@link Void} input
         * @return this builder
         */
        public Builder onFailureProceedTo(final Class<? extends Step<Void>> stepClass) {
            return onFailureProceedTo(stepClass, null);
        }

        /**
         * Routes exhausted failures to a no-input Step with execution overrides.
         *
         * @param stepClass the recovery Step class with {@link Void} input
         * @param options recovery execution options, or {@code null} for registered defaults
         * @return this builder
         */
        public Builder onFailureProceedTo(
                final Class<? extends Step<Void>> stepClass,
                final StepOptions options) {
            failureTarget = new FailureTarget(
                    Objects.requireNonNull(stepClass, "stepClass"), options);
            return this;
        }

        /**
         * Overrides durability for timeout-handler side effects.
         *
         * @param value the durability mode; the default is {@link StepDurability#DEFAULT}
         * @return this builder
         */
        public Builder durability(final StepDurability value) {
            durability = Objects.requireNonNull(value, "durability");
            return this;
        }

        /**
         * Adds an Attribute lock held for the handler invocation.
         *
         * @param value the lock definition
         * @return this builder
         */
        public Builder addLock(final AttributeLock value) {
            locks.add(value);
            return this;
        }

        /**
         * Loads every current instance of an AttributeMap.
         *
         * @param value an AttributeMap registered by the Flow
         * @return this builder
         */
        public Builder addLoadAttributeMap(final AttributeMap<?> value) {
            stateLoads.addAttributeMap(value);
            return this;
        }

        /**
         * Loads one AttributeMap instance.
         *
         * @param value an AttributeMap registered by the Flow
         * @param instance the slash-free instance key
         * @return this builder
         */
        public Builder addLoadAttributeMapInstance(
                final AttributeMap<?> value,
                final String instance) {
            stateLoads.addAttributeMapInstance(value, instance);
            return this;
        }

        /**
         * Loads pending messages from one Channel.
         *
         * @param value a Channel registered by the Flow
         * @return this builder
         */
        public Builder addLoadChannel(final Channel<?> value) {
            stateLoads.addChannel(value);
            return this;
        }

        /**
         * Loads pending messages from every current ChannelMap instance.
         *
         * @param value a ChannelMap registered by the Flow
         * @return this builder
         */
        public Builder addLoadChannelMap(final ChannelMap<?> value) {
            stateLoads.addChannelMap(value);
            return this;
        }

        /**
         * Loads pending messages from one ChannelMap instance.
         *
         * @param value a ChannelMap registered by the Flow
         * @param instance the slash-free instance key
         * @return this builder
         */
        public Builder addLoadChannelMapInstance(
                final ChannelMap<?> value,
                final String instance) {
            stateLoads.addChannelMapInstance(value, instance);
            return this;
        }

        /**
         * Builds immutable timeout-handler options.
         *
         * @return the configured options
         */
        public FlowTimeoutHandlerOptions build() {
            return new FlowTimeoutHandlerOptions(this);
        }
    }

    static final class FailureTarget {
        private final Class<? extends Step<Void>> stepClass;
        private final StepOptions options;

        private FailureTarget(
                final Class<? extends Step<Void>> stepClass,
                final StepOptions options) {
            this.stepClass = stepClass;
            this.options = options;
        }

        Class<? extends Step<Void>> getStepClass() {
            return stepClass;
        }

        StepOptions getOptions() {
            return options;
        }
    }
}
