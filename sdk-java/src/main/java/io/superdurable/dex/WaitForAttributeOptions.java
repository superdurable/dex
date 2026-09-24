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
 * The request timeout bounds the caller-visible call across transparent transport reattachments.
 * The internal handler timeout controls Temporal Update generation rollover and does not end the
 * caller-visible request. Leave both values at zero for normal indefinite waiting.
 */
public final class WaitForAttributeOptions {
    private final String requestId;
    private final Duration requestTimeout;
    private final Duration internalHandlerTimeout;

    private WaitForAttributeOptions(final Builder builder) {
        requestId = builder.requestId;
        requestTimeout = builder.requestTimeout;
        internalHandlerTimeout = builder.internalHandlerTimeout;
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

    Duration getRequestTimeout() {
        return requestTimeout;
    }

    Duration getInternalHandlerTimeout() {
        return internalHandlerTimeout;
    }

    /** Builds immutable Attribute wait options. */
    public static final class Builder {
        private String requestId;
        private Duration requestTimeout = Duration.ZERO;
        private Duration internalHandlerTimeout = Duration.ZERO;

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
         * Bounds one internal Temporal Update handler generation.
         * Zero disables time-based generation rollover. Short positive values can add many Temporal
         * Update events to Workflow history.
         *
         * @param value a nonnegative whole-second duration within the protocol range
         * @return this builder
         */
        public Builder internalHandlerTimeout(final Duration value) {
            internalHandlerTimeout = value;
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
