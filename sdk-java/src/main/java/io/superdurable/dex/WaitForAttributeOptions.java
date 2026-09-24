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
 * Temporal permits 10 in-flight Updates per Workflow Execution. The internal handler timeout
 * reclaims accepted waits that outlive callers and could consume those slots. An active caller
 * transparently starts another generation, which adds another Update to history. Leave it at zero
 * unless abandoned waits can approach the limit. Prefer a value longer than normal request
 * timeouts and reconnect gaps.
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
         * Set a positive value only to reclaim accepted waits left in flight after callers exit.
         * Temporal permits 10 in-flight Updates per Workflow Execution. Active callers
         * transparently start another generation, which counts toward Temporal's 2,000-Update
         * history limit. Zero disables rollover. Prefer a value longer than normal request
         * timeouts and reconnect gaps.
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
