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

        private Builder() {
        }

        /**
         * Sets the maximum duration of one wait-for method attempt.
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
         * <p>Dex automatically heartbeats while the Java handler is running so cancellation reaches
         * the worker promptly. Local activities ignore this setting; an asynchronous local activity
         * that falls back to a regular activity uses it. {@code null} and {@link Duration#ZERO}
         * disable heartbeats. Positive values must be whole seconds within the signed int32 range.
         * Long-running handlers should also poll {@link Context#isCancellationRequested()} and stop
         * before producing non-cancelable external side effects.
         *
         * @param value the regular activity heartbeat timeout, or {@code null} to disable it
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
         * <p>When {@link WaitForFailurePolicy#PROCEED} is selected, Dex defaults to
         * {@link StepDurability#SYNC} for the wait-for method so the recorded failure is not lost after
         * the execute method begins. The Flow-wide durability setting does not override this safety
         * choice. The application may still choose {@link StepDurability#ASYNC} explicitly through
         * {@link #waitForDurability} when it accepts that an extreme failure can cause the wait-for
         * method to run again after execution has already begun.
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
         * <p>Dex defaults to {@link StepDurability#SYNC} for the execute method that owns this policy. The
         * Flow-wide durability setting does not override this safety choice. The application may still
         * select {@link StepDurability#ASYNC} explicitly through {@link #executeDurability} when it
         * accepts that an extreme failure after the recovery Step begins can cause Dex to execute the
         * earlier Step again.
         *
         * @param step the nonnull recovery Step
         * @param <I> the recovery Step input type
         * @return this builder
         * @throws NullPointerException if {@code step} is {@code null}
         */
        public <I> Builder onExecuteFailureProceedTo(final Step<I> step) {
            return onExecuteFailureProceedTo(step, null);
        }

        /**
         * Continues to a recovery Step with per-execution options after execute retries fail.
         *
         * <p>The same durability guidance as
         * {@link #onExecuteFailureProceedTo(Step)} applies. The {@code options} parameter configures
         * the recovery Step execution; configure the failing Step's execute durability on the builder
         * receiving this method call.
         *
         * @param step the nonnull recovery Step
         * @param options recovery execution options, or {@code null} for the Step defaults
         * @param <I> the recovery Step input type
         * @return this builder
         * @throws NullPointerException if {@code step} is {@code null}
         */
        public <I> Builder onExecuteFailureProceedTo(
                final Step<I> step,
                final StepOptions options) {
            executeFailureTarget = new ExecuteFailureTarget(
                    Objects.requireNonNull(step, "step"),
                    options);
            return this;
        }

        /**
         * Overrides durability for this Step's wait-for method result.
         *
         * <p>This method-level value takes precedence over {@link FlowConfig}'s default and Dex's
         * {@link StepDurability#SYNC} default when {@link #waitForFailure} selects
         * {@link WaitForFailurePolicy#PROCEED}. Asynchronous durability reduces latency and improves
         * server persistence batching, but an unpersisted result can be lost during an extreme failure
         * and the wait-for method can run again after execution has already begun. See
         * {@link StepDurability} for the full tradeoff.
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
         * <p>This method-level value takes precedence over {@link FlowConfig}'s default and Dex's
         * {@link StepDurability#SYNC} default when this builder also uses
         * {@link #onExecuteFailureProceedTo(Step)}. The application may choose
         * {@link StepDurability#ASYNC} when it accepts that an extreme failure after recovery begins
         * can cause the earlier Step to execute again. See {@link StepDurability} for latency and
         * throughput details.
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
         * Builds immutable Step options from the current values.
         *
         * @return the configured Step options
         */
        public StepOptions build() {
            return new StepOptions(this);
        }
    }

    static final class ExecuteFailureTarget {
        private final Step<?> step;
        private final StepOptions options;

        private ExecuteFailureTarget(final Step<?> step, final StepOptions options) {
            this.step = step;
            this.options = options;
        }

        Step<?> getStep() {
            return step;
        }

        StepOptions getOptions() {
            return options;
        }
    }
}
