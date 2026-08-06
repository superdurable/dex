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

public final class StopFlowOptions {
    private final StopType type;
    private final String reason;

    public StopFlowOptions() {
        this(StopType.CANCEL, null);
    }

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
