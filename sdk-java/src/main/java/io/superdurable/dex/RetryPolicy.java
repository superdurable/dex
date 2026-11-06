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

/**
 * Configures exponential-backoff retries for Flow or Step operations.
 *
 * <p>Only values supplied to the builder override server defaults. An operation stops retrying when
 * either its maximum attempts or total duration limit is reached. Durations use
 * {@link java.time.Duration}; the receiving API determines whether subsecond precision is accepted.
 * Step retry total duration defaults to four hours and starts when the Step is first handed to a
 * Worker. After an attempt fails, Dex uses the duration to decide whether to schedule another retry.
 * It does not end an attempt that is still running.
 *
 * <p>Step defaults are a one-second initial interval, coefficient 2, a 100-second maximum
 * interval, and no attempt-count limit within the four-hour retry duration. Maximum attempts
 * includes the initial attempt; set it to one to disable retries. A positive
 * {@link io.superdurable.dex.exceptions.RetryAfterException} delay overrides the next backoff.
 * Flow retry policies use the interval and attempt settings but ignore total duration.
 * Durations sent by this SDK must be nonnegative whole seconds within the signed int32 range;
 * invalid durations fail when the policy is mapped for a request or Worker result.
 *
 * <p>Changed in 1.35.0: Step retry duration starts at the first Worker hand-out rather than
 * while the Step waits for a Worker. It gates scheduling a retry and never shortens an attempt's
 * method timeout.
 *
 * <pre>{@code
 * RetryPolicy retry = RetryPolicy.newBuilder()
 *         .initialInterval(Duration.ofSeconds(1))
 *         .backoffCoefficient(2.0)
 *         .maximumInterval(Duration.ofSeconds(30))
 *         .maximumAttempts(5)
 *         .build();
 * }</pre>
 */
public final class RetryPolicy {
    private final Duration initialInterval;
    private final double backoffCoefficient;
    private final Duration maximumInterval;
    private final int maximumAttempts;
    private final Duration totalDuration;

    private RetryPolicy(final Builder builder) {
        this.initialInterval = builder.initialInterval;
        this.backoffCoefficient = builder.backoffCoefficient;
        this.maximumInterval = builder.maximumInterval;
        this.maximumAttempts = builder.maximumAttempts;
        this.totalDuration = builder.totalDuration;
    }

    /**
     * Creates a builder whose unset values use server defaults.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    Duration getInitialInterval() {
        return initialInterval;
    }

    double getBackoffCoefficient() {
        return backoffCoefficient;
    }

    Duration getMaximumInterval() {
        return maximumInterval;
    }

    int getMaximumAttempts() {
        return maximumAttempts;
    }

    Duration getTotalDuration() {
        return totalDuration;
    }

    /** Builds immutable {@link RetryPolicy} values. */
    public static final class Builder {
        private Duration initialInterval;
        private double backoffCoefficient;
        private Duration maximumInterval;
        private int maximumAttempts;
        private Duration totalDuration;

        private Builder() {
        }

        /**
         * Sets the delay before the first retry.
         *
         * <p>The Step default is one second. Zero also selects the server default.
         *
         * @param value the initial retry interval, or {@code null} for the server default
         * @return this builder
         */
        public Builder initialInterval(final Duration value) {
            initialInterval = value;
            return this;
        }

        /**
         * Sets the multiplier applied to each successive retry interval.
         *
         * <p>The Step default is 2. Zero selects the server default.
         *
         * @param value the exponential-backoff coefficient
         * @return this builder
         */
        public Builder backoffCoefficient(final double value) {
            backoffCoefficient = value;
            return this;
        }

        /**
         * Caps the delay between attempts.
         *
         * <p>The Step default is 100 seconds. Zero also selects the server default.
         *
         * @param value the maximum retry interval, or {@code null} for the server default
         * @return this builder
         */
        public Builder maximumInterval(final Duration value) {
            maximumInterval = value;
            return this;
        }

        /**
         * Limits the total number of attempts, including the initial attempt.
         *
         * <p>The Step default is unlimited attempts within total duration. One permits only the
         * initial attempt.
         *
         * @param value the maximum attempt count; zero leaves the server default
         * @return this builder
         */
        public Builder maximumAttempts(final int value) {
            maximumAttempts = value;
            return this;
        }

        /**
         * Limits the total elapsed retry duration.
         *
         * <p>Step method retries default to four hours when this value is unset. An explicit value
         * may be shorter or longer. It starts at the first Worker hand-out and is checked only after
         * an attempt fails to decide whether to retry. It never ends a running attempt.
         *
         * <p>If attempt one is returned without running, the clock resets. Initial queue and lock
         * waits do not start the clock. Once it starts, backoff and later queue waits count toward
         * it. Dex checks whether the next backoff fits within the budget when scheduling a retry;
         * it does not check the budget again at dispatch. A queued retry may therefore start or end
         * after the budget expires. Set the method timeout to bound each running attempt.
         * Zero selects the four-hour default. Flow retry policies ignore this field.
         *
         * <p>Changed in 1.35.0: this budget only gates scheduling another retry; it no longer
         * ends or shortens a running Step attempt.
         *
         * @param value the total retry duration, or {@code null} for the server default
         * @return this builder
         */
        public Builder totalDuration(final Duration value) {
            totalDuration = value;
            return this;
        }

        /**
         * Builds an immutable retry policy from the current values.
         *
         * @return the configured retry policy
         */
        public RetryPolicy build() {
            return new RetryPolicy(this);
        }
    }
}
