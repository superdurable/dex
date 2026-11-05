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
 * Configures a Worker that opens outbound task streams to Dex.
 *
 * <p>Pass these options to {@link WorkerOptions.Builder#pull(PullWorkerOptions)}. Each slot serves
 * the Worker identity and every registered Flow type. The worker does not open a listener.
 * Zero task count and a zero timeout select defaults. An empty Worker ID is generated per Worker.
 *
 * <pre>{@code
 * PullWorkerOptions pull = PullWorkerOptions.newBuilder()
 *         .maxConcurrentTasks(8)
 *         .stopDrainTimeout(Duration.ofSeconds(45))
 *         .build();
 * WorkerOptions options = WorkerOptions.newBuilder().pull(pull).build();
 * }</pre>
 */
public final class PullWorkerOptions {
    private final int maxConcurrentTasks;
    private final int reservedRpcTasks;
    private final boolean shouldWarnRpcCapacity;
    private final String appWorkerID;
    private final Duration stopDrainTimeout;
    private final boolean stopOnPermanentStreamRejection;

    private PullWorkerOptions(final Builder builder) {
        this.stopOnPermanentStreamRejection = builder.stopOnPermanentStreamRejection;
        this.maxConcurrentTasks = builder.maxConcurrentTasks == 0
                ? Runtime.getRuntime().availableProcessors() : builder.maxConcurrentTasks;
        this.shouldWarnRpcCapacity = builder.reservedRpcTasks == null && maxConcurrentTasks == 1;
        this.reservedRpcTasks = builder.reservedRpcTasks == null
                ? (maxConcurrentTasks >= 2 ? 1 : 0) : builder.reservedRpcTasks;
        if (reservedRpcTasks < 0 || reservedRpcTasks >= maxConcurrentTasks) {
            throw new IllegalArgumentException("reservedRpcTasks must be nonnegative and less than maxConcurrentTasks");
        }
        this.appWorkerID = builder.appWorkerID;
        this.stopDrainTimeout = builder.stopDrainTimeout.isZero()
                ? Duration.ofSeconds(30) : builder.stopDrainTimeout;
    }

    /**
     * Creates a builder with CPU-count execution slots and a 30-second drain.
     *
     * @return a mutable pull options builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    int getMaxConcurrentTasks() {
        return maxConcurrentTasks;
    }

    boolean shouldWarnRpcCapacity() {
        return shouldWarnRpcCapacity;
    }

    int getReservedRpcTasks() {
        return reservedRpcTasks;
    }

    String getAppWorkerID() {
        return appWorkerID;
    }

    boolean isStopOnPermanentStreamRejection() {
        return stopOnPermanentStreamRejection;
    }

    Duration getStopDrainTimeout() {
        return stopDrainTimeout;
    }

    /**
     * Builds immutable outbound polling options.
     *
     * <p>Obtain a builder from {@link PullWorkerOptions#newBuilder()}. Zero values use the
     * defaults documented by each setter. The enclosing type shows a complete Worker example.
     */
    public static final class Builder {
        private int maxConcurrentTasks;
        private Integer reservedRpcTasks;
        private String appWorkerID = "";
        private Duration stopDrainTimeout = Duration.ZERO;
        private boolean stopOnPermanentStreamRejection;

        private Builder() {
        }

        /**
         * Sets the number of long-lived execution-slot streams for Step and RPC tasks.
         *
         * <p>This also sizes the Worker's handler pool. Worker start fails when this exceeds
         * the Server's per-Worker limit, matching.maxConcurrentTasksCap. Every received task
         * executes without a capacity rejection.
         *
         * @param value a nonnegative count; zero uses the available CPU count
         * @return this builder
         * @throws IllegalArgumentException if value is negative
         */
        public Builder maxConcurrentTasks(final int value) {
            if (value < 0) {
                throw new IllegalArgumentException("maxConcurrentTasks must not be negative");
            }
            this.maxConcurrentTasks = value;
            return this;
        }

        /**
         * Reserves streams that accept only RPC tasks to protect RPC latency from long-running Steps.
         *
         * <p>Unset reserves one slot when the resolved task limit is at least two, otherwise zero.
         * Set zero to opt out. With one slot and no explicit reserve, Worker start logs a warning
         * about RPC starvation. The remaining streams accept Steps and RPCs. The Server routes
         * Steps only to those streams, and the Worker never declines a Step for capacity.
         *
         * <pre>{@code
         * PullWorkerOptions pull = PullWorkerOptions.newBuilder()
         *         .maxConcurrentTasks(8).reservedRpcTasks(2).build();
         * }</pre>
         *
         * @param value nonnegative reserved capacity below the resolved limit; null selects the default
         * @return this builder
         * @throws IllegalArgumentException from build if value is negative or reaches the task limit
         */
        public Builder reservedRpcTasks(final Integer value) {
            this.reservedRpcTasks = value;
            return this;
        }

        /**
         * Retains the legacy idle-stream option for source compatibility.
         *
         * <p>This value has no effect. Each execution slot has exactly one long-lived stream.
         *
         * @param value a nonnegative legacy count
         * @return this builder
         * @throws IllegalArgumentException if value is negative
         * @deprecated execution slots determine the stream count
         */
        @Deprecated
        public Builder idleStreamsPerQueue(final int value) {
            if (value < 0) {
                throw new IllegalArgumentException("idleStreamsPerQueue must not be negative");
            }
            return this;
        }

        /**
         * Sets the process identity used for sticky task routing.
         *
         * <p>The ID has 1-63 ASCII letters, digits, periods, underscores, or hyphens. It must
         * differ from every registered Flow type. Empty selects a hostname-derived random ID.
         *
         * @param value the Worker ID, or empty to generate one
         * @return this builder
         * @throws IllegalArgumentException if value is null or malformed
         */
        public Builder appWorkerID(final String value) {
            if (value == null || (!value.isEmpty() && !value.matches("[A-Za-z0-9._-]{1,63}"))) {
                throw new IllegalArgumentException("appWorkerID must contain 1-63 ASCII letters, digits, periods, underscores, or hyphens");
            }
            this.appWorkerID = value;
            return this;
        }

        /**
         * Sets how long Stop waits for active pull tasks before canceling them.
         *
         * <p>A canceled handler must return before Stop completes. A stream whose final output is
         * already queued gets a five-second flush grace after the timeout before it is reset.
         *
         * @param value a nonnegative duration; zero uses 30 seconds
         * @return this builder
         * @throws IllegalArgumentException if value is null or negative
         */
        public Builder stopDrainTimeout(final Duration value) {
            if (value == null || value.isNegative()) {
                throw new IllegalArgumentException("stopDrainTimeout must not be negative");
            }
            this.stopDrainTimeout = value;
            return this;
        }

        /**
         * Stops the Worker on a permanent rejection leaving a required slot kind without a registered stream.
         *
         * <p>Defaults to false, which retries indefinitely. When enabled, the first such rejection
         * before or after readiness drains within stopDrainTimeout, then makes Worker.start throw
         * PullRegistrationError. Other registered streams of that slot kind prevent this shutdown.
         * See the enclosing type for a complete options example.
         *
         * @param value whether to stop on an uncovered permanent stream rejection
         * @return this builder
         */
        public Builder stopOnPermanentStreamRejection(final boolean value) {
            this.stopOnPermanentStreamRejection = value;
            return this;
        }

        /**
         * Builds pull options for a Worker.
         *
         * @return immutable pull options
         */
        public PullWorkerOptions build() {
            return new PullWorkerOptions(this);
        }
    }
}
