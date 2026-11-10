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
 * The Server does not read the request ID for this wait.
 * Request timeout bounds the caller-visible call across transport reattachments.
 * Disconnecting releases the wait slot.
 */
public final class WaitForAttributeOptions {
    private final String requestId;
    private final Duration requestTimeout;

    private WaitForAttributeOptions(final Builder builder) {
        requestId = builder.requestId;
        requestTimeout = builder.requestTimeout;
    }

    /**
     * Creates an empty builder.
     * The Server does not read the request ID for this wait.
     *
     * @return a new builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    String getRequestId() {
        return requestId;
    }

    Duration getRequestTimeout() {
        return requestTimeout;
    }

    /** Builds immutable Attribute wait options. */
    public static final class Builder {
        private String requestId;
        private Duration requestTimeout = Duration.ZERO;

        private Builder() {
        }

        /**
         * Sets an optional request ID; the Server does not read it.
         *
         * @param value the non-empty Request ID override
         * @return this builder
         */
        public Builder requestId(final String value) {
            requestId = value;
            return this;
        }

        /**
         * Bounds the caller-visible request across transparent transport reattachments.
         * Zero waits indefinitely.
         *
         * @param value a nonnegative whole-second duration within the protocol range
         * @return this builder
         */
        public Builder requestTimeout(final Duration value) {
            requestTimeout = value;
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
