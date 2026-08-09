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

/**
 * Configures how {@link Client#stopFlow} ends a running Flow.
 *
 * <p>The default constructor requests cancellation without a reason. Use the explicit constructor
 * to terminate or fail a Flow and to attach a human-readable reason.
 *
 * <pre>{@code
 * client.stopFlow(
 *         "order-123",
 *         new StopFlowOptions(StopType.TERMINATE, "operator request"));
 * }</pre>
 */
public final class StopFlowOptions {
    private final StopType type;
    private final String reason;

    /** Creates cancellation options without an explicit reason. */
    public StopFlowOptions() {
        this(StopType.CANCEL, null);
    }

    /**
     * Creates stop options with an explicit mode and reason.
     *
     * @param type the requested terminal behavior
     * @param reason the user-visible reason, or {@code null}
     */
    public StopFlowOptions(final StopType type, final String reason) {
        this.type = type;
        this.reason = reason;
    }

    StopType getType() {
        return type;
    }

    String getReason() {
        return reason;
    }
}
