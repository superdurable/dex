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

/** Controls inclusion of earlier Continue-as-New runs in visibility searches. */
public final class SearchFlowsOptions {
    private final boolean includeContinuedAsNew;

    private SearchFlowsOptions(final Builder builder) {
        includeContinuedAsNew = builder.includeContinuedAsNew;
    }

    /** Creates a builder.
     * @return options excluding earlier Continue-as-New runs by default
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    /** Reports inclusion.
     * @return whether to disable default exclusion while retaining explicit query filters
     */
    public boolean getIncludeContinuedAsNew() {
        return includeContinuedAsNew;
    }

    /** Builds immutable search options. */
    public static final class Builder {
        private boolean includeContinuedAsNew;

        private Builder() {}

        /**
         * Sets whether to include earlier Continue-as-New runs.
         * @param value inclusion flag
         * @return this builder
         */
        public Builder setIncludeContinuedAsNew(final boolean value) {
            includeContinuedAsNew = value;
            return this;
        }

        /** Builds options.
         * @return immutable search options
         */
        public SearchFlowsOptions build() {
            return new SearchFlowsOptions(this);
        }
    }
}
