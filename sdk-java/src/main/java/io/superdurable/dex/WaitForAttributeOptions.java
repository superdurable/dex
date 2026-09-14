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
 * The maximum wait time defaults to zero, which waits indefinitely.
 * An abandoned infinite wait remains in flight until it matches or the Flow closes.
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
         * Sets the total handler wait budget. Zero waits indefinitely.
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
