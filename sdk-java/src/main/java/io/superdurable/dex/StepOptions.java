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

public final class StepOptions {
    private final Duration waitForMethodTimeout;
    private final Duration executeMethodTimeout;
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
        this.waitForRetry = builder.waitForRetry;
        this.executeRetry = builder.executeRetry;
        this.waitForFailure = builder.waitForFailure;
        this.executeFailureTarget = builder.executeFailureTarget;
        this.waitForDurability = builder.waitForDurability;
        this.executeDurability = builder.executeDurability;
        this.waitForLocks = immutable(builder.waitForLocks);
        this.executeLocks = immutable(builder.executeLocks);
    }

    public static Builder newBuilder() {
        return new Builder();
    }

    Duration getWaitForMethodTimeout() {
        return waitForMethodTimeout;
    }

    Duration getExecuteMethodTimeout() {
        return executeMethodTimeout;
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

    public static final class Builder {
        private Duration waitForMethodTimeout;
        private Duration executeMethodTimeout;
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

        public Builder waitForMethodTimeout(final Duration value) {
            this.waitForMethodTimeout = value;
            return this;
        }

        public Builder executeMethodTimeout(final Duration value) {
            this.executeMethodTimeout = value;
            return this;
        }

        public Builder waitForRetry(final RetryPolicy value) {
            waitForRetry = value;
            return this;
        }

        public Builder executeRetry(final RetryPolicy value) {
            executeRetry = value;
            return this;
        }

        public Builder waitForFailure(final WaitForFailurePolicy value) {
            waitForFailure = value;
            return this;
        }

        public <I> Builder onExecuteFailureProceedTo(final Step<I> step) {
            return onExecuteFailureProceedTo(step, null);
        }

        public <I> Builder onExecuteFailureProceedTo(
                final Step<I> step,
                final StepOptions options) {
            executeFailureTarget = new ExecuteFailureTarget(
                    Objects.requireNonNull(step, "step"),
                    options);
            return this;
        }

        public Builder waitForDurability(final StepDurability value) {
            waitForDurability = value;
            return this;
        }

        public Builder executeDurability(final StepDurability value) {
            executeDurability = value;
            return this;
        }

        public Builder addWaitForLock(final AttributeLock value) {
            waitForLocks.add(value);
            return this;
        }

        public Builder addExecuteLock(final AttributeLock value) {
            executeLocks.add(value);
            return this;
        }

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
