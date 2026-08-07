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

    public static final class Builder {
        private Duration initialInterval;
        private double backoffCoefficient;
        private Duration maximumInterval;
        private int maximumAttempts;
        private Duration totalDuration;

        private Builder() {
        }

        public Builder initialInterval(final Duration value) {
            initialInterval = value;
            return this;
        }

        public Builder backoffCoefficient(final double value) {
            backoffCoefficient = value;
            return this;
        }

        public Builder maximumInterval(final Duration value) {
            maximumInterval = value;
            return this;
        }

        public Builder maximumAttempts(final int value) {
            maximumAttempts = value;
            return this;
        }

        public Builder totalDuration(final Duration value) {
            totalDuration = value;
            return this;
        }

        public RetryPolicy build() {
            return new RetryPolicy(this);
        }
    }
}
