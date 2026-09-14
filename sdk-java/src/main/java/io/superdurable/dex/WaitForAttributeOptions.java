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
 * Configures one durable Attribute match wait.
 * The server derives a stable Request ID from the Attribute condition when none is supplied.
 * Reuse an override only for the same logical predicate.
 * Leave the maximum wait time at its zero default for ordinary infinite waits.
 * A positive value releases per-Flow in-flight Update capacity for abandoned or rarely matching waits.
 * It spans transport reattachments and Continue-as-New, unlike a caller or transport deadline.
 * Retrying after expiry creates a new Update generation.
 */
public final class WaitForAttributeOptions {
    private final String requestId;
    private final Duration maximumWaitTime;

    private WaitForAttributeOptions(final Builder builder) {
        requestId = builder.requestId;
        maximumWaitTime = builder.maximumWaitTime;
    }

    /**
     * Creates an empty builder.
     * The server derives a stable Request ID when none is configured.
     *
     * @return a new builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    String getRequestId() {
        return requestId;
    }

    Duration getMaximumWaitTime() {
        return maximumWaitTime;
    }

    /** Builds immutable Attribute wait options. */
    public static final class Builder {
        private String requestId;
        private Duration maximumWaitTime = Duration.ZERO;

        private Builder() {
        }

        /**
         * Overrides the stable Request ID derived from the Attribute condition.
         *
         * @param value the non-empty Request ID override
         * @return this builder
         */
        public Builder requestId(final String value) {
            requestId = value;
            return this;
        }

        /**
         * Limits how long the accepted Temporal Update handler remains in flight.
         * Zero waits indefinitely and is recommended for ordinary waits.
         * A positive value releases per-Flow in-flight capacity, but retrying after expiry creates a new Update generation.
         *
         * @param value a nonnegative whole-second duration within the protocol range
         * @return this builder
         */
        public Builder maximumWaitTime(final Duration value) {
            maximumWaitTime = value;
            return this;
        }

        /**
         * Builds the options. The Client validates the duration at call time.
         *
         * @return immutable Attribute wait options
         */
        public WaitForAttributeOptions build() {
            return new WaitForAttributeOptions(this);
        }
    }
}
