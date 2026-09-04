/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * Configures timeout, retry, durability, locking, and failure behavior for a Step.
 *
 * <p>Return an instance from {@link Step#getStepOptions} to configure every execution, or attach one
 * to a {@link StepMovement} for a single transition. Wait-for and execute settings are independent.
 * Durations describe method timeouts, not the potentially much longer time spent waiting for
 * durable timer or Channel conditions.
 *
 * <pre>{@code
 * public StepOptions getStepOptions() {
 *     return StepOptions.newBuilder()
 *             .waitForMethodTimeout(Duration.ofSeconds(5))
 *             .executeMethodTimeout(Duration.ofSeconds(30))
 *             .executeRetry(RetryPolicy.newBuilder().maximumAttempts(3).build())
 *             .addExecuteLock(AttributeLock.of(balance))
 *             .build();
 * }
 * }</pre>
 */
public final class StepOptions {
    private final Duration waitForMethodTimeout;
    private final Duration executeMethodTimeout;
    private final Duration heartbeatTimeout;
    private final RetryPolicy waitForRetry;
    private final RetryPolicy executeRetry;
    private final WaitForFailurePolicy waitForFailure;
    private final ExecuteFailureTarget executeFailureTarget;
    private final StepDurability waitForDurability;
    private final StepDurability executeDurability;
    private final List<AttributeLock> waitForLocks;
    private final List<AttributeLock> executeLocks;
    private final HandlerStateLoads waitForStateLoads;
    private final HandlerStateLoads executeStateLoads;

    private StepOptions(final Builder builder) {
        this.waitForMethodTimeout = builder.waitForMethodTimeout;
        this.executeMethodTimeout = builder.executeMethodTimeout;
        this.heartbeatTimeout = builder.heartbeatTimeout;
        this.waitForRetry = builder.waitForRetry;
        this.executeRetry = builder.executeRetry;
        this.waitForFailure = builder.waitForFailure;
        this.executeFailureTarget = builder.executeFailureTarget;
        this.waitForDurability = builder.waitForDurability;
        this.executeDurability = builder.executeDurability;
        this.waitForLocks = immutable(builder.waitForLocks);
        this.executeLocks = immutable(builder.executeLocks);
        this.waitForStateLoads = builder.waitForStateLoads.build();
        this.executeStateLoads = builder.executeStateLoads.build();
    }

    /**
     * Creates a builder initialized with Dex defaults.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    Duration getWaitForMethodTimeout() {
        return waitForMethodTimeout;
    }

    Duration getExecuteMethodTimeout() {
        return executeMethodTimeout;
    }

    Duration getHeartbeatTimeout() {
        return heartbeatTimeout;
    }

    RetryPolicy getWaitForRetry() {
        return waitForRetry;
    }

    RetryPolicy getExecuteRetry() {
        return executeRetry;
    }

    WaitForFailurePolicy getWaitForFailure() {
        return waitForFailure;
    }

    ExecuteFailureTarget getExecuteFailureTarget() {
        return executeFailureTarget;
    }

    StepDurability getWaitForDurability() {
        return waitForDurability;
    }

    StepDurability getExecuteDurability() {
        return executeDurability;
    }

    List<AttributeLock> getWaitForLocks() {
        return waitForLocks;
    }

    List<AttributeLock> getExecuteLocks() {
        return executeLocks;
    }

    HandlerStateLoads getWaitForStateLoads() {
        return waitForStateLoads;
    }

    HandlerStateLoads getExecuteStateLoads() {
        return executeStateLoads;
    }

    private static List<AttributeLock> immutable(final List<AttributeLock> values) {
        return Collections.unmodifiableList(new ArrayList<AttributeLock>(values));
    }

    /** Builds immutable {@link StepOptions} values. */
    public static final class Builder {
        private Duration waitForMethodTimeout;
        private Duration executeMethodTimeout;
        private Duration heartbeatTimeout;
        private RetryPolicy waitForRetry;
        private RetryPolicy executeRetry;
        private WaitForFailurePolicy waitForFailure = WaitForFailurePolicy.FAIL_FLOW;
        private ExecuteFailureTarget executeFailureTarget;
        private StepDurability waitForDurability = StepDurability.DEFAULT;
        private StepDurability executeDurability = StepDurability.DEFAULT;
        private final List<AttributeLock> waitForLocks = new ArrayList<AttributeLock>();
        private final List<AttributeLock> executeLocks = new ArrayList<AttributeLock>();
        private final HandlerStateLoads.Builder waitForStateLoads =
                new HandlerStateLoads.Builder();
        private final HandlerStateLoads.Builder executeStateLoads =
                new HandlerStateLoads.Builder();

        private Builder() {
        }

        /**
         * Sets the maximum duration of one wait-for method attempt.
         *
         * <p>A regular activity attempt defaults to two hours. An asynchronous local activity
         * ignores this setting during its seven-second optimization window; fallback regular
         * activity attempts apply it.
         *
         * @param value the method timeout, or {@code null} for the server default
         * @return this builder
         */
        public Builder waitForMethodTimeout(final Duration value) {
            this.waitForMethodTimeout = value;
            return this;
        }

        /**
         * Sets the maximum duration of one execute method attempt.
         *
         * <p>A regular activity attempt defaults to two hours. An asynchronous local activity
         * ignores this setting during its seven-second optimization window; fallback regular
         * activity attempts apply it.
         *
         * @param value the method timeout, or {@code null} for the server default
         * @return this builder
         */
        public Builder executeMethodTimeout(final Duration value) {
            this.executeMethodTimeout = value;
            return this;
        }

        /**
         * Sets the heartbeat timeout for regular wait-for and execute activities.
         *
         * <p>Dex sends no automatic heartbeat. Application code must call
         * {@link Context#recordHeartbeat} or write a {@link Stream} often enough to make progress.
         * {@code null} and {@link Duration#ZERO} use the one-minute server default. Explicit values
         * must be whole seconds within the signed int32 range and satisfy the server-configured
         * minimum, which defaults to ten seconds. Local activities ignore this setting; a fallback
         * regular activity applies it. Once cancellation reaches the Java Worker, Dex interrupts the
         * handler thread.
         *
         * @param value the regular activity heartbeat timeout, or {@code null} for one minute
         * @return this builder
         */
        public Builder heartbeatTimeout(final Duration value) {
            heartbeatTimeout = value;
            return this;
        }

        /**
         * Sets the retry policy for wait-for method failures.
         *
         * @param value the retry policy, or {@code null} for the server default
         * @return this builder
         */
        public Builder waitForRetry(final RetryPolicy value) {
            waitForRetry = value;
            return this;
        }

        /**
         * Sets the retry policy for execute method failures.
         *
         * @param value the retry policy, or {@code null} for the server default
         * @return this builder
         */
        public Builder executeRetry(final RetryPolicy value) {
            executeRetry = value;
            return this;
        }

        /**
         * Sets the action taken after wait-for retries are exhausted.
         *
         * <p>This policy does not change durability selection. The method override takes precedence
         * over {@link FlowConfig}'s default, followed by the synchronous server default.
         *
         * @param value the failure policy; the default is {@link WaitForFailurePolicy#FAIL_FLOW}
         * @return this builder
         */
        public Builder waitForFailure(final WaitForFailurePolicy value) {
            waitForFailure = value;
            return this;
        }

        /**
         * Continues to a recovery Step after execute retries are exhausted.
         *
         * <p>The recovery Step receives {@code null} input. Its input type should therefore accept
         * {@code null}, commonly {@link Void}.
         *
         * <p>This policy does not change durability selection. The method override takes precedence
         * over {@link FlowConfig}'s default, followed by the synchronous server default.
         *
         * @param stepClass the nonnull recovery Step class
         * @param <I> the recovery Step input type
         * @return this builder
         * @throws NullPointerException if {@code stepClass} is {@code null}
         */
        public <I> Builder onExecuteFailureProceedTo(
                final Class<? extends Step<I>> stepClass) {
            return onExecuteFailureProceedTo(stepClass, null);
        }

        /**
         * Continues to a recovery Step with per-execution options after execute retries fail.
         *
         * <p>The same durability guidance as
         * {@link #onExecuteFailureProceedTo(Class)} applies. The {@code options} parameter configures
         * the recovery Step execution; configure the failing Step's execute durability on the builder
         * receiving this method call.
         *
         * @param stepClass the nonnull recovery Step class
         * @param options recovery execution options, or {@code null} for the Step defaults
         * @param <I> the recovery Step input type
         * @return this builder
         * @throws NullPointerException if {@code stepClass} is {@code null}
         */
        public <I> Builder onExecuteFailureProceedTo(
                final Class<? extends Step<I>> stepClass,
                final StepOptions options) {
            executeFailureTarget = new ExecuteFailureTarget(
                    Objects.requireNonNull(stepClass, "stepClass"),
                    options);
            return this;
        }

        /**
         * Overrides durability for this Step's wait-for method result.
         *
         * <p>This method-level value takes precedence over {@link FlowConfig}'s default and the
         * synchronous server default. Failure policy does not alter that ordering. See
         * {@link StepDurability} for the asynchronous replay tradeoff.
         *
         * @param value the durability mode; the default is {@link StepDurability#DEFAULT}
         * @return this builder
         */
        public Builder waitForDurability(final StepDurability value) {
            waitForDurability = value;
            return this;
        }

        /**
         * Overrides durability for this Step's execute method result.
         *
         * <p>This method-level value takes precedence over {@link FlowConfig}'s default and the
         * synchronous server default. Failure policy does not alter that ordering. See
         * {@link StepDurability} for the asynchronous replay tradeoff.
         *
         * @param value the durability mode; the default is {@link StepDurability#DEFAULT}
         * @return this builder
         */
        public Builder executeDurability(final StepDurability value) {
            executeDurability = value;
            return this;
        }

        /**
         * Adds an Attribute lock held while the wait-for method runs.
         *
         * @param value the lock definition
         * @return this builder
         */
        public Builder addWaitForLock(final AttributeLock value) {
            waitForLocks.add(value);
            return this;
        }

        /**
         * Adds an Attribute lock held while the execute method runs.
         *
         * @param value the lock definition
         * @return this builder
         */
        public Builder addExecuteLock(final AttributeLock value) {
            executeLocks.add(value);
            return this;
        }

        /**
         * Loads every current instance of an AttributeMap for WaitFor.
         *
         * @param value an AttributeMap registered by the Step's Flow
         * @return this builder
         * @throws NullPointerException during mapping if {@code value} is {@code null}
         */
        public Builder addWaitForLoadAttributeMap(final AttributeMap<?> value) {
            waitForStateLoads.addAttributeMap(value);
            return this;
        }

        /**
         * Loads one AttributeMap instance for WaitFor.
         *
         * @param value an AttributeMap registered by the Step's Flow
         * @param instance the slash-free instance key
         * @return this builder
         */
        public Builder addWaitForLoadAttributeMapInstance(
                final AttributeMap<?> value,
                final String instance) {
            waitForStateLoads.addAttributeMapInstance(value, instance);
            return this;
        }

        /**
         * Loads pending messages from one Channel for WaitFor.
         *
         * @param value a Channel registered by the Step's Flow
         * @return this builder
         */
        public Builder addWaitForLoadChannel(final Channel<?> value) {
            waitForStateLoads.addChannel(value);
            return this;
        }

        /**
         * Loads pending messages from every current instance of a ChannelMap for WaitFor.
         *
         * @param value a ChannelMap registered by the Step's Flow
         * @return this builder
         */
        public Builder addWaitForLoadChannelMap(final ChannelMap<?> value) {
            waitForStateLoads.addChannelMap(value);
            return this;
        }

        /**
         * Loads pending messages from one ChannelMap instance for WaitFor.
         *
         * @param value a ChannelMap registered by the Step's Flow
         * @param instance the slash-free instance key
         * @return this builder
         */
        public Builder addWaitForLoadChannelMapInstance(
                final ChannelMap<?> value,
                final String instance) {
            waitForStateLoads.addChannelMapInstance(value, instance);
            return this;
        }

        /**
         * Loads every current instance of an AttributeMap for Execute.
         *
         * @param value an AttributeMap registered by the Step's Flow
         * @return this builder
         */
        public Builder addExecuteLoadAttributeMap(final AttributeMap<?> value) {
            executeStateLoads.addAttributeMap(value);
            return this;
        }

        /**
         * Loads one AttributeMap instance for Execute.
         *
         * @param value an AttributeMap registered by the Step's Flow
         * @param instance the slash-free instance key
         * @return this builder
         */
        public Builder addExecuteLoadAttributeMapInstance(
                final AttributeMap<?> value,
                final String instance) {
            executeStateLoads.addAttributeMapInstance(value, instance);
            return this;
        }

        /**
         * Loads pending messages from one Channel for Execute.
         *
         * @param value a Channel registered by the Step's Flow
         * @return this builder
         */
        public Builder addExecuteLoadChannel(final Channel<?> value) {
            executeStateLoads.addChannel(value);
            return this;
        }

        /**
         * Loads pending messages from every current ChannelMap instance for Execute.
         *
         * @param value a ChannelMap registered by the Step's Flow
         * @return this builder
         */
        public Builder addExecuteLoadChannelMap(final ChannelMap<?> value) {
            executeStateLoads.addChannelMap(value);
            return this;
        }

        /**
         * Loads pending messages from one ChannelMap instance for Execute.
         *
         * @param value a ChannelMap registered by the Step's Flow
         * @param instance the slash-free instance key
         * @return this builder
         */
        public Builder addExecuteLoadChannelMapInstance(
                final ChannelMap<?> value,
                final String instance) {
            executeStateLoads.addChannelMapInstance(value, instance);
            return this;
        }

        /**
         * Builds immutable Step options from the current values.
         *
         * @return the configured Step options
         */
        public StepOptions build() {
            return new StepOptions(this);
        }
    }

    static final class ExecuteFailureTarget {
        private final Class<? extends Step<?>> stepClass;
        private final StepOptions options;

        private ExecuteFailureTarget(
                final Class<? extends Step<?>> stepClass,
                final StepOptions options) {
            this.stepClass = stepClass;
            this.options = options;
        }

        Class<? extends Step<?>> getStepClass() {
            return stepClass;
        }

        StepOptions getOptions() {
            return options;
        }
    }
}
