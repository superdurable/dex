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

import com.fasterxml.jackson.databind.ObjectMapper;

public final class ClientOptions {
    private final String flowServiceAddress;
    private final WorkerTarget workerTarget;
    private final ObjectMapper objectMapper;

    public ClientOptions() {
        this("localhost:8801", null, new ObjectMapper());
    }

    public ClientOptions(final String flowServiceAddress) {
        this(flowServiceAddress, null, new ObjectMapper());
    }

    public ClientOptions(
            final String flowServiceAddress,
            final WorkerTarget workerTarget) {
        this(flowServiceAddress, workerTarget, new ObjectMapper());
    }

    public ClientOptions(
            final String flowServiceAddress,
            final WorkerTarget workerTarget,
            final ObjectMapper objectMapper) {
        if (objectMapper == null) {
            throw new IllegalArgumentException("objectMapper is required");
        }
        this.flowServiceAddress = flowServiceAddress;
        this.workerTarget = workerTarget;
        this.objectMapper = objectMapper;
    }

    String getFlowServiceAddress() {
        return flowServiceAddress;
    }

    WorkerTarget getWorkerTarget() {
        return workerTarget;
    }

    ObjectMapper getObjectMapper() {
        return objectMapper;
    }
}
