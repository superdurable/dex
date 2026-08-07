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

public final class WorkerTarget {
    private final String address;
    private final boolean headless;

    public WorkerTarget(final String address, final boolean headless) {
        if (address == null || address.trim().isEmpty()) {
            throw new IllegalArgumentException("Worker target address is required");
        }
        this.address = address;
        this.headless = headless;
    }

    String getAddress() {
        return address;
    }

    boolean isHeadless() {
        return headless;
    }
}
