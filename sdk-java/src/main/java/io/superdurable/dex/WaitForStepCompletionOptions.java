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
 * Configures one durable Step completion wait.
 * The server derives a stable Request ID from the Step execution when none is supplied.
 * Reuse an override only for the same logical Step wait.
 * Leave the maximum wait time at zero to wait indefinitely.
 * Positive values are an exceptional safety valve. Short budgets can add many Temporal Update events
 * to Workflow history, so prefer at least one minute when nonzero.
 * A positive value bounds the caller-visible wait.
 */
public final class WaitForStepCompletionOptions {
    private final String requestId;
    private final Duration maximumWaitTime;

    private WaitForStepCompletionOptions(final Builder builder) {
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

    /** Builds immutable Step completion wait options. */
    public static final class Builder {
        private String requestId;
        private Duration maximumWaitTime = Duration.ZERO;

        private Builder() {
        }

        /**
         * Overrides the stable Request ID derived from the Step execution.
         *
         * @param value the non-empty Request ID
         * @return this builder
         */
        public Builder requestId(final String value) {
            requestId = value;
            return this;
        }

        /**
         * Bounds the caller-visible wait. Zero waits indefinitely.
         * Positive values are rare. Prefer at least one minute to limit Temporal Update history growth.
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
         * @return immutable Step wait options
         */
        public WaitForStepCompletionOptions build() {
            return new WaitForStepCompletionOptions(this);
        }
    }
}
