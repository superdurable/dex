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

/**
 * Configures exponential-backoff retries for Flow or Step operations.
 *
 * <p>Only values supplied to the builder override server defaults. An operation stops retrying when
 * either its maximum attempts or total duration limit is reached. Durations use
 * {@link java.time.Duration}; the receiving API determines whether subsecond precision is accepted.
 * With asynchronous Step durability, local and regular execution share the same attempt and elapsed
 * duration budgets. Fallback starts immediately, while later regular retries continue the cumulative
 * exponential-backoff sequence.
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
