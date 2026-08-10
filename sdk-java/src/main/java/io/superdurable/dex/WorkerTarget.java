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
 * Identifies the worker endpoint to which Dex routes Flow invocations.
 *
 * <p>Use the same target in {@link WorkerOptions} and in the client-side Flow configuration. A
 * headless target is resolved by Dex as a headless service address; a non-headless target is used
 * as a directly reachable worker address.
 *
 * <pre>{@code
 * WorkerTarget target = new WorkerTarget("orders-worker:8803", false);
 * WorkerOptions workerOptions = WorkerOptions.newBuilder()
 *         .workerTarget(target)
 *         .build();
 * }</pre>
 */
public final class WorkerTarget {
    private final String address;
    private final boolean headless;

    /**
     * Creates a worker routing target.
     *
     * @param address the worker address advertised to Dex; must not be blank
     * @param headless whether the address represents a headless service
     * @throws IllegalArgumentException if {@code address} is {@code null} or blank
     */
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
